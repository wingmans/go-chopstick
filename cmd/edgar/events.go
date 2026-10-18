package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wingman.com/fetch-ecb/internal/edgar"
	"wingman.com/fetch-ecb/internal/eventbus"
	"wingman.com/fetch-ecb/internal/eventworker"
	"wingman.com/fetch-ecb/internal/news"
	"wingman.com/fetch-ecb/internal/pollingworker"
	"wingman.com/fetch-ecb/internal/web"
)

type eventCommandConfig struct {
	natsURL          string
	pollingDB        string
	newsDB           string
	filingsDirectory string
	parsedDirectory  string
	analysisDB       string
	masterPath       string
	userAgent        string
	baseURL          string
	address          string
	interval         time.Duration
}

func parseEventCommandConfig(command string, args []string) (appConfig, error) {
	cfg := appConfig{command: command}
	cfg.events = eventCommandConfig{
		natsURL:          eventbus.DefaultURL,
		pollingDB:        "./data/polling.db",
		newsDB:           "./data/feed.db",
		filingsDirectory: edgar.DefaultFilingsDirectory,
		parsedDirectory:  edgar.DefaultParsedDirectory,
		analysisDB:       "./data/analysis.db",
		masterPath:       "./data/cache/indexes/master.tsv",
		userAgent:        environmentValue(edgarUserAgentEnv, defaultUserAgent),
		baseURL:          environmentValue(edgarBaseURLEnv, ""),
		address:          "127.0.0.1:8081",
		interval:         time.Minute,
	}

	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.StringVar(&cfg.events.natsURL, "nats-url", cfg.events.natsURL,
		"NATS server URL")
	flags.StringVar(&cfg.events.pollingDB, "polling-db",
		cfg.events.pollingDB, "polling and outbox database")
	flags.StringVar(&cfg.events.newsDB, "news-db", cfg.events.newsDB,
		"disposable news database")
	flags.StringVar(&cfg.events.filingsDirectory, "filings-dir",
		cfg.events.filingsDirectory, "downloaded filing directory")
	flags.StringVar(&cfg.events.parsedDirectory, "parsed-dir",
		cfg.events.parsedDirectory, "parsed filing directory")
	flags.StringVar(&cfg.events.analysisDB, "analysis-db",
		cfg.events.analysisDB, "analysis database")
	flags.StringVar(&cfg.events.masterPath, "master", cfg.events.masterPath,
		"stitched master index")
	flags.StringVar(&cfg.events.userAgent, "user-agent",
		cfg.events.userAgent, "SEC user agent")
	flags.StringVar(&cfg.events.baseURL, "base-url", cfg.events.baseURL,
		"SEC base URL override")
	flags.StringVar(&cfg.events.address, "address", cfg.events.address,
		"news HTTP listen address")
	flags.DurationVar(&cfg.events.interval, "interval", cfg.events.interval,
		"Atom polling interval")

	flags.Usage = func() {
		printSubcommandHelp(command, eventCommandDescription(command),
			[]helpOption{
				{"--nats-url <url>", "NATS server; default nats://127.0.0.1:4222."},
				{"--polling-db <path>", "Accession and outbox database."},
				{"--news-db <path>", "Disposable news read-model database."},
				{"--filings-dir <path>", "Downloaded filing directory."},
				{"--parsed-dir <path>", "Parsed filing directory."},
				{"--analysis-db <path>", "Aggregate analysis database."},
				{"--master <path>", "Stitched master index for batch discovery."},
				{"--user-agent <value>", "SEC user agent."},
				{"--address <host:port>", "News HTTP address."},
				{"--interval <duration>", "Atom polling interval."},
			})
	}
	if err := flags.Parse(args); err != nil {
		return appConfig{}, flag.ErrHelp
	}

	if flags.NArg() != 0 {
		return subcommandError(flags, errors.New("unexpected positional argument"))
	}

	if cfg.events.natsURL == "" || cfg.events.interval <= 0 {
		return subcommandError(flags,
			errors.New("--nats-url and a positive --interval are required"))
	}

	return cfg, nil
}

func eventCommandDescription(command string) string {
	return map[string]string{
		"atom":     "Discover current SEC filings and publish durable events.",
		"batch":    "Reconcile filing discoveries from a local master index.",
		"download": "Download files for discovery events, serially.",
		"process":  "Parse downloaded filings and update aggregates, serially.",
		"news":     "Serve a rebuildable filing news feed.",
		"run":      "Run the four continuous roles in one process.",
	}[command]
}

func runEventCommand(ctx context.Context, command string,
	cfg eventCommandConfig, logger *slog.Logger,
) error {
	if err := ensureParentDirectories(cfg); err != nil {
		return err
	}

	if command != "run" {
		return runRole(ctx, command, cfg, logger)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errorsCh := make(chan error, 4)

	for _, role := range []string{"atom", "download", "process", "news"} {
		go func() {
			errorsCh <- runRole(ctx, role, cfg, logger)
		}()
	}

	err := <-errorsCh

	cancel()

	return err
}

func runRole(ctx context.Context, role string, cfg eventCommandConfig,
	logger *slog.Logger,
) error {
	bus, err := eventbus.Connect(cfg.natsURL)
	if err != nil {
		return err
	}
	defer func() { _ = bus.Close() }()

	switch role {
	case "atom":
		return pollingworker.Run(ctx, pollingworker.ServiceConfig{
			Interval:  cfg.interval,
			DBPath:    cfg.pollingDB,
			UserAgent: cfg.userAgent,
			Publisher: bus,
		})
	case "batch":
		return runBatchDiscovery(ctx, bus, cfg)
	case "download":
		return eventworker.Download(ctx, bus, eventworker.DownloadConfig{
			Directory: cfg.filingsDirectory,
			UserAgent: cfg.userAgent,
			BaseURL:   cfg.baseURL,
		}, logger)
	case "process":
		return eventworker.Process(ctx, bus, eventworker.ProcessConfig{
			FilingsDirectory: cfg.filingsDirectory,
			ParsedDirectory:  cfg.parsedDirectory,
			AnalysisDBPath:   cfg.analysisDB,
		}, logger)
	case "news":
		return runNews(ctx, bus, cfg, logger)
	default:
		return fmt.Errorf("unknown event role %q", role)
	}
}

func runBatchDiscovery(ctx context.Context, bus *eventbus.NATS,
	cfg eventCommandConfig,
) error {
	file, err := os.Open(cfg.masterPath)
	if err != nil {
		return fmt.Errorf("open master index: %w", err)
	}
	defer func() { _ = file.Close() }()

	store, err := pollingworker.NewDedupeServiceContext(ctx, cfg.pollingDB)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	reader := edgar.NewIndexReader(file, edgar.IndexFilter{})
	batch := make([]pollingworker.FilingEvent, 0, 1000)
	flush := func() error {
		if len(batch) > 0 {
			if _, err := store.SaveBatchContext(ctx, batch); err != nil {
				return err
			}

			batch = batch[:0]
		}

		return store.PublishPending(ctx, bus)
	}

	for {
		record, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return flush()
		}

		if err != nil {
			return fmt.Errorf("read master index: %w", err)
		}

		accession := strings.TrimSuffix(filepath.Base(record.FilingPath),
			filepath.Ext(record.FilingPath))

		indexPath := record.IndexPath
		if indexPath == "" {
			indexPath = record.FilingPath
		}

		batch = append(batch, pollingworker.FilingEvent{
			EventID:         "filing.discovered:" + accession,
			AccessionNumber: accession,
			CIK:             record.CIK,
			CompanyName:     record.CompanyName,
			FormType:        record.FormType,
			FilingDate:      record.DateFiled.Format("2006-01-02"),
			FilingPath:      record.FilingPath,
			IndexPath:       record.IndexPath,
			FilingURL: "https://www.sec.gov/Archives/" +
				strings.TrimPrefix(indexPath, "/"),
			SourceFeed: "BATCH",
			Timestamp:  time.Now().UTC(),
		})
		if len(batch) == cap(batch) {
			if err := flush(); err != nil {
				return err
			}
		}
	}
}

func runNews(ctx context.Context, bus *eventbus.NATS,
	cfg eventCommandConfig, logger *slog.Logger,
) error {
	store, replay, err := news.Open(ctx, cfg.newsDB)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	consumerErr := make(chan error, 1)
	go func() {
		consumerErr <- news.Consume(ctx, bus, store, replay, logger)
	}()

	server := &http.Server{
		Addr:              cfg.address,
		Handler:           web.NewNewsServer(store, logger),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() { serverErr <- server.ListenAndServe() }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx),
			5*time.Second)
		defer cancel()

		return server.Shutdown(shutdownCtx)
	case err := <-consumerErr:
		return err
	case err := <-serverErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		return err
	}
}

func ensureParentDirectories(cfg eventCommandConfig) error {
	for _, path := range []string{cfg.pollingDB, cfg.newsDB, cfg.analysisDB} {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return fmt.Errorf("create directory for %s: %w", path, err)
		}
	}

	return nil
}
