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

	"wingman.com/fetch-ecb/internal/appdata"
	"wingman.com/fetch-ecb/internal/ctxlog"
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
	filingsAddress   string
	interval         time.Duration
	filter           edgar.IndexFilter
	setName          string
	year             string
	fromYear         int
	toYear           int
	drain            bool
	maxAttempts      int
}

//nolint:maintidx // The switch keeps each role's CLI flags visible together.
func parseEventCommandConfig(command string, args []string) (appConfig, error) {
	configuredForms, err := configuredFormTypes()
	if err != nil {
		return appConfig{}, err
	}

	formTypes := stringList{}
	cfg := appConfig{command: command}
	cfg.events = eventCommandConfig{
		natsURL:          environmentValue(edgarNATSURLEnv, eventbus.DefaultURL),
		pollingDB:        appdata.Path("polling.db"),
		newsDB:           appdata.Path("feed.db"),
		filingsDirectory: appdata.Path("cache", "filings"),
		parsedDirectory:  appdata.Path("parsed"),
		analysisDB:       appdata.Path("analysis.db"),
		masterPath:       appdata.Path("cache", "indexes", "master.tsv"),
		userAgent:        environmentValue(edgarUserAgentEnv, defaultUserAgent),
		baseURL:          environmentValue(edgarBaseURLEnv, ""),
		address: environmentValue(edgarNewsAddressEnv,
			"127.0.0.1:8081"),
		filingsAddress: environmentValue(edgarWebAddressEnv,
			"127.0.0.1:8083"),
		interval:    time.Minute,
		setName:     environmentValue(edgarSetEnv, ""),
		maxAttempts: 5,
	}
	cfg.events.filter.FormTypes = configuredForms

	if command == "discover" {
		fromYear, err := environmentFromYear(0)
		if err != nil {
			return appConfig{}, err
		}

		cfg.events.fromYear = fromYear
	}

	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.StringVar(&cfg.events.natsURL, "nats-url", cfg.events.natsURL,
		"NATS server URL")

	options := []helpOption{
		{"--nats-url <url>", "NATS server; default nats://127.0.0.1:4222."},
	}

	switch command {
	case "atom":
		flags.StringVar(&cfg.events.pollingDB, "polling-db",
			cfg.events.pollingDB, "polling and outbox database")
		flags.StringVar(&cfg.events.userAgent, "user-agent",
			cfg.events.userAgent, "SEC user agent")
		flags.DurationVar(&cfg.events.interval, "interval",
			cfg.events.interval, "Atom polling interval")

		options = append(options,
			helpOption{"--polling-db <path>", "Accession and outbox database."},
			helpOption{"--user-agent <value>", "SEC user agent."},
			helpOption{"--interval <duration>", "Atom polling interval."},
		)
	case "discover":
		flags.StringVar(&cfg.events.pollingDB, "polling-db",
			cfg.events.pollingDB, "polling and outbox database")
		flags.StringVar(&cfg.events.masterPath, "master", cfg.events.masterPath,
			"stitched master index")
		flags.StringVar(&cfg.events.filter.CIK, "cik", "",
			"select filings for one CIK")
		flags.StringVar(&cfg.events.setName, "set", "",
			"select filings for a constituent set")
		flags.Var(&formTypes, "form-type",
			"select this form type; may be repeated")
		flags.StringVar(&cfg.events.year, "year", "",
			"select one filing year")
		flags.IntVar(&cfg.events.fromYear, "from-year", 0,
			"first filing year to include")
		flags.IntVar(&cfg.events.toYear, "to-year", 0,
			"last filing year to include")

		options = append(options,
			helpOption{"--polling-db <path>", "Accession and outbox database."},
			helpOption{"--master <path>", "Stitched master index."},
			helpOption{"--cik <cik>", "Select filings for one CIK."},
			helpOption{"--set <name>", "Select a set from the data directory."},
			helpOption{"--form-type <type>", "Select a form; repeatable."},
			helpOption{"--year <year>", "Select one filing year."},
			helpOption{"--from-year <year>", "First year; default 2016."},
			helpOption{"--to-year <year>", "Last filing year."},
		)
	case "select":
		flags.StringVar(&cfg.events.pollingDB, "polling-db",
			cfg.events.pollingDB, "pipeline state database")
		flags.StringVar(&cfg.events.setName, "set", cfg.events.setName,
			"select filings for a constituent set")
		flags.Var(&formTypes, "form-type",
			"select this supported form type; may be repeated")
		flags.BoolVar(&cfg.events.drain, "drain", false,
			"exit after the current event backlog is empty")

		options = append(options,
			helpOption{"--polling-db <path>", "Pipeline state database."},
			helpOption{"--set <name>", "Select a set from the data directory."},
			helpOption{"--form-type <type>",
				"Select a supported form; repeatable."},
			helpOption{"--drain", "Exit after the current backlog is empty."},
		)
	case "download":
		flags.StringVar(&cfg.events.pollingDB, "polling-db",
			cfg.events.pollingDB, "pipeline state database")
		flags.StringVar(&cfg.events.filingsDirectory, "filings-dir",
			cfg.events.filingsDirectory, "downloaded filing directory")
		flags.StringVar(&cfg.events.userAgent, "user-agent",
			cfg.events.userAgent, "SEC user agent")
		flags.StringVar(&cfg.events.baseURL, "base-url", cfg.events.baseURL,
			"SEC base URL override")
		flags.BoolVar(&cfg.events.drain, "drain", false,
			"exit after the current event backlog is empty")
		flags.IntVar(&cfg.events.maxAttempts, "max-attempts",
			cfg.events.maxAttempts, "attempts before work is marked failed")

		options = append(options,
			helpOption{"--polling-db <path>", "Pipeline state database."},
			helpOption{"--filings-dir <path>", "Downloaded filing directory."},
			helpOption{"--user-agent <value>", "SEC user agent."},
			helpOption{"--base-url <url>", "SEC base URL override."},
			helpOption{"--drain", "Exit after the current backlog is empty."},
			helpOption{"--max-attempts <count>", "Attempts before work is marked failed."},
		)
	case "process":
		flags.StringVar(&cfg.events.pollingDB, "polling-db",
			cfg.events.pollingDB, "pipeline state database")
		flags.StringVar(&cfg.events.filingsDirectory, "filings-dir",
			cfg.events.filingsDirectory, "downloaded filing directory")
		flags.StringVar(&cfg.events.parsedDirectory, "parsed-dir",
			cfg.events.parsedDirectory, "parsed filing directory")
		flags.StringVar(&cfg.events.analysisDB, "analysis-db",
			cfg.events.analysisDB, "analysis database")
		flags.BoolVar(&cfg.events.drain, "drain", false,
			"exit after the current event backlog is empty")
		flags.IntVar(&cfg.events.maxAttempts, "max-attempts",
			cfg.events.maxAttempts, "attempts before work is marked failed")

		options = append(options,
			helpOption{"--polling-db <path>", "Pipeline state database."},
			helpOption{"--filings-dir <path>", "Downloaded filing directory."},
			helpOption{"--parsed-dir <path>", "Parsed filing directory."},
			helpOption{"--analysis-db <path>", "Analysis database."},
			helpOption{"--drain", "Exit after the current backlog is empty."},
			helpOption{"--max-attempts <count>", "Attempts before work is marked failed."},
		)
	case "news":
		flags.StringVar(&cfg.events.newsDB, "news-db", cfg.events.newsDB,
			"disposable news database")
		flags.StringVar(&cfg.events.address, "address", cfg.events.address,
			"news HTTP listen address")

		options = append(options,
			helpOption{"--news-db <path>", "Disposable news database."},
			helpOption{"--address <host:port>", "News HTTP address."},
		)
	case "run":
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
		flags.StringVar(&cfg.events.userAgent, "user-agent",
			cfg.events.userAgent, "SEC user agent")
		flags.StringVar(&cfg.events.baseURL, "base-url", cfg.events.baseURL,
			"SEC base URL override")
		flags.StringVar(&cfg.events.address, "address", cfg.events.address,
			"news HTTP listen address")
		flags.StringVar(&cfg.events.filingsAddress, "filings-address",
			cfg.events.filingsAddress, "filings web HTTP listen address")
		flags.DurationVar(&cfg.events.interval, "interval",
			cfg.events.interval, "Atom polling interval")
		flags.IntVar(&cfg.events.maxAttempts, "max-attempts",
			cfg.events.maxAttempts, "attempts before work is marked failed")
		flags.StringVar(&cfg.events.setName, "set", cfg.events.setName,
			"automatic processing constituent set")
		flags.Var(&formTypes, "form-type",
			"automatic processing form type; may be repeated")

		options = append(options,
			helpOption{"--polling-db <path>", "Accession and outbox database."},
			helpOption{"--news-db <path>", "Disposable news database."},
			helpOption{"--filings-dir <path>", "Downloaded filing directory."},
			helpOption{"--parsed-dir <path>", "Parsed filing directory."},
			helpOption{"--analysis-db <path>", "Analysis database."},
			helpOption{"--user-agent <value>", "SEC user agent."},
			helpOption{"--base-url <url>", "SEC base URL override."},
			helpOption{"--address <host:port>", "News HTTP address."},
			helpOption{"--filings-address <host:port>",
				"Filings web HTTP address."},
			helpOption{"--interval <duration>", "Atom polling interval."},
			helpOption{"--max-attempts <count>", "Attempts before work is marked failed."},
			helpOption{"--set <name>", "Automatic processing constituent set."},
			helpOption{"--form-type <type>",
				"Automatic processing form; repeatable."},
		)
	}

	flags.Usage = func() {
		printSubcommandHelp(command, eventCommandDescription(command), options)
	}
	if err := flags.Parse(args); err != nil {
		return appConfig{}, flag.ErrHelp
	}

	if flags.NArg() != 0 {
		return subcommandError(flags, errors.New("unexpected positional argument"))
	}

	if cfg.events.natsURL == "" || cfg.events.interval <= 0 ||
		cfg.events.maxAttempts <= 0 {
		return subcommandError(flags,
			errors.New("--nats-url, --interval, and --max-attempts must be valid"))
	}

	if command == "discover" {
		visited := map[string]bool{}

		flags.Visit(func(value *flag.Flag) { visited[value.Name] = true })

		if visited["year"] && !visited["from-year"] {
			cfg.events.fromYear = 0
		}

		cfg.events.filter.FormTypes = formTypes

		if err := applyConstituentSet(&cfg.events.filter,
			cfg.events.setName); err != nil {
			return subcommandError(flags, err)
		}

		if strings.TrimSpace(cfg.events.year) == "" &&
			cfg.events.fromYear == 0 {
			cfg.events.fromYear = edgar.EarliestPipelineYear
		}

		if err := applyYearSelection(&cfg.events.filter, cfg.events.year,
			cfg.events.fromYear, cfg.events.toYear); err != nil {
			return subcommandError(flags, err)
		}

		if cfg.events.filter.Year != 0 &&
			cfg.events.filter.Year < edgar.EarliestPipelineYear {
			return subcommandError(flags, fmt.Errorf(
				"--year must be %d or later", edgar.EarliestPipelineYear))
		}

		if cfg.events.filter.FromYear != 0 &&
			cfg.events.filter.FromYear < edgar.EarliestPipelineYear {
			return subcommandError(flags, fmt.Errorf(
				"--from-year must be %d or later",
				edgar.EarliestPipelineYear))
		}
	}

	if command == "select" || command == "run" {
		formsToValidate := configuredForms
		if len(formTypes) > 0 {
			formsToValidate = formTypes
		}
		forms, err := edgar.ValidateSupportedFormTypes(formsToValidate)
		if err != nil {
			return subcommandError(flags, err)
		}
		if len(forms) == 0 {
			return subcommandError(flags,
				errors.New("at least one supported form type is required"))
		}
		cfg.events.filter.FormTypes = forms

		if err := applyConstituentSet(&cfg.events.filter,
			cfg.events.setName); err != nil {
			return subcommandError(flags, err)
		}
	}

	return cfg, nil
}

func eventCommandDescription(command string) string {
	return map[string]string{
		"atom":     "Discover current SEC filings and publish durable events.",
		"discover": "Select filing discoveries from a local master index.",
		"select":   "Select observed filings for automatic processing.",
		"download": "Download files for discovery events, serially.",
		"process":  "Parse downloaded filings and update aggregates, serially.",
		"news":     "Serve a rebuildable filing news feed.",
		"run":      "Run all continuous roles and HTTP services in one process.",
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

	bootstrap, err := eventbus.Connect(cfg.natsURL)
	if err != nil {
		return err
	}

	if err := bootstrap.Close(); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errorsCh := make(chan error, 5)

	for _, role := range []string{
		"atom", "select", "download", "process", "news", "filings-web",
	} {
		go func() {
			errorsCh <- runRole(ctx, role, cfg, logger)
		}()
	}

	err = <-errorsCh

	cancel()

	return err
}

func runRole(ctx context.Context, role string, cfg eventCommandConfig,
	logger *slog.Logger,
) error {
	logger = logger.With("role", role)
	ctx = ctxlog.WithLogger(ctx, logger)

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
	case "discover":
		return runDiscovery(ctx, bus, cfg, logger)
	case "select":
		logger.Info("starting selection worker", "consumer",
			eventworker.SelectionDurable, "set", cfg.setName,
			"set_members", len(cfg.filter.CIKs),
			"form_types", cfg.filter.FormTypes, "drain", cfg.drain)

		return eventworker.Select(ctx, bus, eventworker.SelectionConfig{
			SetName: cfg.setName, CIKs: cfg.filter.CIKs,
			FormTypes: cfg.filter.FormTypes, Drain: cfg.drain,
		}, logger)
	case "download":
		logger.Info("starting queue worker", "consumer",
			eventworker.DownloadDurable, "selection", "selected filings only",
			"drain", cfg.drain, "max_attempts", cfg.maxAttempts)

		failures, err := pollingworker.NewDedupeServiceContext(ctx,
			cfg.pollingDB)
		if err != nil {
			return err
		}
		defer func() { _ = failures.Close() }()

		return eventworker.Download(ctx, bus, eventworker.DownloadConfig{
			Directory:   cfg.filingsDirectory,
			UserAgent:   cfg.userAgent,
			BaseURL:     cfg.baseURL,
			Drain:       cfg.drain,
			MaxAttempts: cfg.maxAttempts,
			Failures:    failures,
		}, logger)
	case "process":
		logger.Info("starting queue worker", "consumer",
			eventworker.ProcessDurable, "selection", "all queued downloads",
			"drain", cfg.drain, "max_attempts", cfg.maxAttempts)

		failures, err := pollingworker.NewDedupeServiceContext(ctx,
			cfg.pollingDB)
		if err != nil {
			return err
		}
		defer func() { _ = failures.Close() }()

		return eventworker.Process(ctx, bus, eventworker.ProcessConfig{
			FilingsDirectory: cfg.filingsDirectory,
			ParsedDirectory:  cfg.parsedDirectory,
			AnalysisDBPath:   cfg.analysisDB,
			Drain:            cfg.drain,
			MaxAttempts:      cfg.maxAttempts,
			Failures:         failures,
		}, logger)
	case "news":
		return runNews(ctx, bus, cfg, logger)
	case "filings-web":
		logger.Info("starting filings web", "address", cfg.filingsAddress,
			"parsed_directory", cfg.parsedDirectory)

		return runServe(ctx, logger, cfg.filingsAddress, cfg.parsedDirectory,
			"", cfg.analysisDB)
	default:
		return fmt.Errorf("unknown event role %q", role)
	}
}

func runDiscovery(ctx context.Context, bus *eventbus.NATS,
	cfg eventCommandConfig, logger *slog.Logger,
) error {
	logger.Info("starting discovery", "set", cfg.setName,
		"from_year", cfg.filter.FromYear, "to_year", cfg.filter.ToYear,
		"form_types", cfg.filter.FormTypes, "master", cfg.masterPath)

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

	reader := edgar.NewIndexReader(file, cfg.filter)
	batch := make([]pollingworker.FilingEvent, 0, 1000)
	selected := 0
	discovered := 0
	lastReport := time.Now()
	flush := func(final bool) error {
		if len(batch) > 0 {
			newEvents, err := store.SaveBatchContext(ctx, batch)
			if err != nil {
				return err
			}

			discovered += len(newEvents)

			batch = batch[:0]
		}

		if err := store.PublishPending(ctx, bus); err != nil {
			return err
		}

		if final || time.Since(lastReport) >= 5*time.Second {
			logger.Info("discovery progress", "selected", selected,
				"new", discovered, "existing", selected-discovered)

			lastReport = time.Now()
		}

		return nil
	}

	for {
		record, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return flush(true)
		}

		if err != nil {
			return fmt.Errorf("read master index: %w", err)
		}

		accession := strings.TrimSuffix(filepath.Base(record.FilingPath),
			filepath.Ext(record.FilingPath))

		batch = append(batch, pollingworker.FilingEvent{
			EventID:         "filing.discovered:" + accession,
			AccessionNumber: accession,
			CIK:             record.CIK,
			CompanyName:     record.CompanyName,
			FormType:        record.FormType,
			FilingDate:      record.DateFiled.Format("2006-01-02"),
			FilingPath:      record.FilingPath,
			IndexPath:       "",
			FilingURL: "https://www.sec.gov/Archives/" +
				strings.TrimPrefix(record.FilingPath, "/"),
			SourceFeed: "BATCH",
			Timestamp:  time.Now().UTC(),
		})
		selected++

		if len(batch) == cap(batch) {
			if err := flush(false); err != nil {
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
		Handler:           web.NewNewsServer(store, bus, logger),
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

func configuredFormTypes() ([]string, error) {
	value := environmentValue(edgarFormTypesEnv, "10-K,10-Q,8-K")

	return edgar.ValidateSupportedFormTypes(strings.Split(value, ","))
}
