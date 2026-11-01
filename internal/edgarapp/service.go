// Package edgarapp composes the long-running EDGAR monolith.
package edgarapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"wingman.com/chopstick/internal/appdata"
	"wingman.com/chopstick/internal/applock"
	"wingman.com/chopstick/internal/constituents"
	"wingman.com/chopstick/internal/ctxlog"
	"wingman.com/chopstick/internal/edgar"
	"wingman.com/chopstick/internal/eventbus"
	"wingman.com/chopstick/internal/eventworker"
	"wingman.com/chopstick/internal/news"
	"wingman.com/chopstick/internal/pollingworker"
	"wingman.com/chopstick/internal/transport"
	"wingman.com/chopstick/internal/web"
)

const (
	DefaultUserAgent      = "wingman paul@wingmen.io"
	initialRoleRetryDelay = time.Second
	maximumRoleRetryDelay = 30 * time.Second
	stableRoleRun         = time.Minute
	newsShutdownTimeout   = 5 * time.Second
)

// Config contains the typed configuration of the long-running application.
type Config struct {
	NATSURL           string
	CommandAddress    string
	PollingDB         string
	NewsDB            string
	FilingsDirectory  string
	ParsedDirectory   string
	AnalysisDB        string
	OwnershipDB       string
	MasterPath        string
	IndexDirectory    string
	IndexZipDirectory string
	UserAgent         string
	BaseURL           string
	HTTPAddress       string
	Interval          time.Duration
	SetName           string
	CIKs              []string
	FormTypes         []string
	FromYear          int
	MaxAttempts       int
}

// ConfigFromEnvironment returns service configuration without involving CLI
// parsing. Command-line adapters may override the returned typed values.
func ConfigFromEnvironment() (Config, error) {
	fromYear, err := environmentInt("EDGAR_FROM_YEAR",
		edgar.EarliestPipelineYear)
	if err != nil {
		return Config{}, err
	}

	forms, err := edgar.ValidateSupportedFormTypes(strings.Split(
		environmentValue("EDGAR_FORM_TYPES", "10-K,10-Q,8-K"), ",",
	))
	if err != nil {
		return Config{}, err
	}

	config := Config{
		NATSURL:           environmentValue("EDGAR_NATS_URL", eventbus.DefaultURL),
		CommandAddress:    environmentValue("CHOP_COMMAND_ADDRESS", transport.DefaultAddress),
		PollingDB:         appdata.Path("polling.db"),
		NewsDB:            appdata.Path("feed.db"),
		FilingsDirectory:  appdata.Path("cache", "filings"),
		ParsedDirectory:   appdata.Path("parsed"),
		AnalysisDB:        appdata.Path("analysis.db"),
		OwnershipDB:       appdata.Path("ownership.db"),
		MasterPath:        appdata.Path("cache", "indexes", "master.tsv"),
		IndexDirectory:    appdata.Path("cache", "indexes", "quarterly"),
		IndexZipDirectory: appdata.Path("cache", "index-zips"),
		UserAgent:         environmentValue("EDGAR_USER_AGENT", DefaultUserAgent),
		BaseURL:           environmentValue("EDGAR_BASE_URL", ""),
		HTTPAddress:       webAddressFromEnvironment(),
		Interval:          time.Minute,
		SetName:           environmentValue("EDGAR_SET", ""),
		CIKs:              nil,
		FormTypes:         forms,
		FromYear:          fromYear,
		MaxAttempts:       5,
	}

	if err := config.SetConstituentSet(config.SetName); err != nil {
		return Config{}, err
	}

	return config, config.Validate()
}

// SetConstituentSet resolves a logical set name into the service's CIK scope.
func (c *Config) SetConstituentSet(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		c.SetName = ""
		c.CIKs = nil

		return nil
	}

	if filepath.Base(name) != name || filepath.Ext(name) != "" {
		return errors.New("set must be a name, not a path")
	}

	set, err := constituents.LoadJSON(appdata.Path("sets", name+".json"))
	if err != nil {
		return err
	}

	c.SetName = name
	c.CIKs = set.CIKs()

	return nil
}

// Validate checks service configuration independently of a command parser.
func (c *Config) Validate() error {
	switch {
	case strings.TrimSpace(c.NATSURL) == "":
		return errors.New("NATS URL is required")
	case strings.TrimSpace(c.CommandAddress) == "":
		return errors.New("command address is required")
	case strings.TrimSpace(c.UserAgent) == "":
		return errors.New("SEC user agent is required")
	case strings.TrimSpace(c.HTTPAddress) == "":
		return errors.New("HTTP address is required")
	case c.Interval <= 0:
		return errors.New("poll interval must be positive")
	case c.MaxAttempts <= 0:
		return errors.New("maximum attempts must be positive")
	case c.FromYear < edgar.EarliestPipelineYear:
		return fmt.Errorf("from year must be %d or later",
			edgar.EarliestPipelineYear)
	case len(c.FormTypes) == 0:
		return errors.New("at least one supported form type is required")
	}

	for name, path := range map[string]string{
		"polling database":   c.PollingDB,
		"news database":      c.NewsDB,
		"filings directory":  c.FilingsDirectory,
		"parsed directory":   c.ParsedDirectory,
		"analysis database":  c.AnalysisDB,
		"ownership database": c.OwnershipDB,
		"master index":       c.MasterPath,
	} {
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("%s path is required", name)
		}
	}

	return nil
}

// Run starts every continuous role, the web application, and the local command
// transport in one foreground process.
func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	if err := config.Validate(); err != nil {
		return err
	}

	if err := ensureParentDirectories(config); err != nil {
		return err
	}

	lock, err := applock.Acquire(appdata.Path("chopd.lock"))
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()

	bootstrap, err := eventbus.Connect(config.NATSURL)
	if err != nil {
		return err
	}

	if err := bootstrap.Close(); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	startedAt := time.Now().UTC()
	admin := &adminHandler{config: config, startedAt: startedAt}

	commandServer := transport.NewServer(config.CommandAddress)

	reconcileLogger := logger.With("role", "reconcile")
	go reconcileAtStartup(ctx, config, reconcileLogger)

	for _, role := range []string{
		"atom", "select", "download", "process", "ownership",
	} {
		go superviseRole(ctx, role, logger, func() error {
			return runRole(ctx, role, config, logger)
		})
	}

	errorsCh := make(chan error, 2)
	go func() { errorsCh <- runRole(ctx, "web", config, logger) }()
	go func() {
		errorsCh <- commandServer.Start(ctx, func(payload []byte) []byte {
			return admin.Handle(ctx, payload)
		})
	}()

	err = <-errorsCh

	cancel()

	if errors.Is(err, context.Canceled) {
		return nil
	}

	return err
}

func reconcileAtStartup(ctx context.Context, config Config,
	logger *slog.Logger,
) {
	client := &http.Client{Timeout: 30 * time.Second}
	if err := ensureMasterIndex(ctx, client, config, logger); err != nil &&
		!errors.Is(err, context.Canceled) {
		logger.Warn("master index bootstrap failed", "error", err)

		return
	}

	if err := reconcileMasterIndex(ctx, config, logger); err != nil &&
		!errors.Is(err, context.Canceled) {
		logger.Warn("master index reconciliation failed", "error", err)
	}
}

func superviseRole(ctx context.Context, role string, logger *slog.Logger,
	run func() error,
) {
	retryDelay := initialRoleRetryDelay
	logger = logger.With("role", role)

	for ctx.Err() == nil {
		started := time.Now()
		err := run()

		if ctx.Err() != nil {
			return
		}

		runDuration := time.Since(started)
		if runDuration >= stableRoleRun {
			retryDelay = initialRoleRetryDelay
		}

		logger.Warn("role stopped; retrying", "error", err,
			"retry_in", retryDelay)

		timer := time.NewTimer(retryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()

			return
		case <-timer.C:
		}

		if runDuration >= stableRoleRun {
			retryDelay = initialRoleRetryDelay
		} else {
			retryDelay = min(retryDelay*2, maximumRoleRetryDelay)
		}
	}
}

func runRole(ctx context.Context, role string, config Config,
	logger *slog.Logger,
) error {
	logger = logger.With("role", role)
	ctx = ctxlog.WithLogger(ctx, logger)

	bus, err := eventbus.Connect(config.NATSURL)
	if err != nil {
		return err
	}
	defer func() { _ = bus.Close() }()

	switch role {
	case "atom":
		return pollingworker.Run(ctx, pollingworker.ServiceConfig{
			Interval: config.Interval, DBPath: config.PollingDB,
			UserAgent: config.UserAgent, Publisher: bus,
		})
	case "select":
		return eventworker.Select(ctx, bus, eventworker.SelectionConfig{
			SetName: config.SetName, CIKs: config.CIKs,
			FormTypes: config.FormTypes,
		}, logger)
	case "download":
		failures, openErr := pollingworker.NewDedupeServiceContext(
			ctx, config.PollingDB,
		)
		if openErr != nil {
			return openErr
		}
		defer func() { _ = failures.Close() }()

		return eventworker.Download(ctx, bus, eventworker.DownloadConfig{
			Directory: config.FilingsDirectory, UserAgent: config.UserAgent,
			BaseURL: config.BaseURL, MaxAttempts: config.MaxAttempts,
			Failures: failures,
		}, logger)
	case "process":
		failures, openErr := pollingworker.NewDedupeServiceContext(
			ctx, config.PollingDB,
		)
		if openErr != nil {
			return openErr
		}
		defer func() { _ = failures.Close() }()

		return eventworker.Process(ctx, bus, eventworker.ProcessConfig{
			FilingsDirectory: config.FilingsDirectory,
			ParsedDirectory:  config.ParsedDirectory,
			AnalysisDBPath:   config.AnalysisDB,
			MaxAttempts:      config.MaxAttempts, Failures: failures,
		}, logger)
	case "ownership":
		failures, openErr := pollingworker.NewDedupeServiceContext(
			ctx, config.PollingDB,
		)
		if openErr != nil {
			return openErr
		}
		defer func() { _ = failures.Close() }()

		return eventworker.Ownership(ctx, bus, eventworker.OwnershipConfig{
			FilingsDirectory: config.FilingsDirectory,
			DatabasePath:     config.OwnershipDB, UserAgent: config.UserAgent,
			BaseURL: config.BaseURL, MaxAttempts: config.MaxAttempts,
			Failures: failures,
		}, logger)
	case "web":
		return runWeb(ctx, bus, config, logger)
	default:
		return fmt.Errorf("unknown application role %q", role)
	}
}

func ensureMasterIndex(ctx context.Context, client *http.Client,
	config Config, logger *slog.Logger,
) error {
	if _, err := os.Stat(config.MasterPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect master index: %w", err)
	}

	logger.Info("starting missing master index bootstrap",
		"from_year", config.FromYear, "path", config.MasterPath)

	err := edgar.DownloadIndex(ctx, client, edgar.Config{
		Directory:     config.IndexDirectory,
		ZipDirectory:  config.IndexZipDirectory,
		MasterPath:    config.MasterPath,
		SinceYear:     config.FromYear,
		UserAgent:     config.UserAgent,
		RefreshLatest: false,
		Stitch:        true,
		BaseURL:       config.BaseURL,
	})
	if err != nil {
		return err
	}

	logger.Info("master index bootstrap complete", "path", config.MasterPath)

	return nil
}

func reconcileMasterIndex(ctx context.Context, config Config,
	logger *slog.Logger,
) error {
	if config.SetName == "" || len(config.CIKs) == 0 {
		logger.Warn("skipping master index reconciliation",
			"reason", "EDGAR_SET is not configured")

		return nil
	}

	bus, err := eventbus.Connect(config.NATSURL)
	if err != nil {
		return err
	}
	defer func() { _ = bus.Close() }()

	return discover(ctx, bus, config, logger)
}

func discover(ctx context.Context, bus *eventbus.NATS, config Config,
	logger *slog.Logger,
) error {
	file, err := os.Open(config.MasterPath)
	if err != nil {
		return fmt.Errorf("open master index: %w", err)
	}
	defer func() { _ = file.Close() }()

	store, err := pollingworker.NewDedupeServiceContext(ctx, config.PollingDB)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	reader := edgar.NewIndexReader(file, edgar.IndexFilter{
		CIKs: config.CIKs, FormTypes: config.FormTypes,
		FromYear: config.FromYear,
	})
	batch := make([]pollingworker.FilingEvent, 0, 1000)
	selected := 0
	discovered := 0
	flush := func() error {
		if len(batch) > 0 {
			newEvents, saveErr := store.SaveBatchContext(ctx, batch)
			if saveErr != nil {
				return saveErr
			}

			discovered += len(newEvents)
			batch = batch[:0]
		}

		return store.PublishPending(ctx, bus)
	}

	for {
		record, readErr := reader.Next()
		if errors.Is(readErr, io.EOF) {
			if err := flush(); err != nil {
				return err
			}

			logger.Info("master index reconciliation complete",
				"selected", selected, "new", discovered)

			return nil
		}

		if readErr != nil {
			return fmt.Errorf("read master index: %w", readErr)
		}

		accession := strings.TrimSuffix(filepath.Base(record.FilingPath),
			filepath.Ext(record.FilingPath))
		batch = append(batch, pollingworker.FilingEvent{
			AccessionNumber: accession, CIK: record.CIK,
			CompanyName: record.CompanyName, FormType: record.FormType,
			FilingDate: record.DateFiled.Format("2006-01-02"),
			FilingPath: record.FilingPath,
			FilingURL: "https://www.sec.gov/Archives/" +
				strings.TrimPrefix(record.FilingPath, "/"),
			SourceFeed: "BATCH", Timestamp: time.Now().UTC(),
		})
		selected++

		if len(batch) == cap(batch) {
			if err := flush(); err != nil {
				return err
			}
		}
	}
}

func runWeb(ctx context.Context, bus *eventbus.NATS, config Config,
	logger *slog.Logger,
) error {
	roleCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	store, replay, err := news.Open(roleCtx, config.NewsDB)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	application, err := web.NewConfiguredServerWithDatabases(roleCtx,
		config.ParsedDirectory, config.AnalysisDB, config.OwnershipDB,
		appdata.Path("sets"), config.SetName, logger)
	if err != nil {
		return err
	}
	defer func() { _ = application.Close() }()

	application.MountNews(web.NewNewsServer(store, bus, logger))

	consumerErr := make(chan error, 1)
	go func() {
		consumerErr <- news.Consume(roleCtx, bus, store, replay, logger)
	}()

	server := &http.Server{
		Addr: config.HTTPAddress, Handler: application,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() { serverErr <- server.ListenAndServe() }()

	var result error

	waitForConsumer := false

	select {
	case <-ctx.Done():
	case result = <-consumerErr:
	case err := <-serverErr:
		waitForConsumer = true

		if !errors.Is(err, http.ErrServerClosed) {
			result = err
		}
	}

	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(
		context.WithoutCancel(ctx), newsShutdownTimeout,
	)
	defer shutdownCancel()

	shutdownErr := server.Shutdown(shutdownCtx)

	if waitForConsumer {
		select {
		case consumerResult := <-consumerErr:
			if result == nil && !errors.Is(consumerResult, context.Canceled) {
				result = consumerResult
			}
		case <-time.After(newsShutdownTimeout):
			bus.CloseImmediately()

			consumerResult := <-consumerErr
			if result == nil && !errors.Is(consumerResult, context.Canceled) {
				result = consumerResult
			}
		}
	} else {
		serverResult := <-serverErr
		if result == nil && ctx.Err() == nil &&
			!errors.Is(serverResult, http.ErrServerClosed) {
			result = serverResult
		}
	}

	if result != nil {
		return errors.Join(result, shutdownErr)
	}

	if shutdownErr != nil && !errors.Is(shutdownErr, http.ErrServerClosed) {
		return shutdownErr
	}

	return nil
}

type adminHandler struct {
	config    Config
	startedAt time.Time
}

// Handle decodes one local CLI command and delegates it to application logic.
func (h *adminHandler) Handle(ctx context.Context, payload []byte) []byte {
	var request transport.Request
	if err := json.Unmarshal(payload, &request); err != nil {
		return encodeCommandResponse(transport.Response{},
			fmt.Errorf("decode command: %w", err))
	}

	response := transport.Response{}

	var err error

	switch request.Command {
	case transport.CommandStatus:
		var status transport.Status

		status, err = h.status(ctx)
		response.Status = &status
	case transport.CommandListFailures:
		response.Failures, err = h.listFailures(ctx, request)
	case transport.CommandRetryFailures:
		response.Retried, err = h.retryFailures(ctx, request.Stage)
	default:
		err = fmt.Errorf("unknown command %q", request.Command)
	}

	return encodeCommandResponse(response, err)
}

func encodeCommandResponse(response transport.Response, responseErr error) []byte {
	if responseErr != nil {
		response.Error = responseErr.Error()
	}

	payload, err := json.Marshal(response)
	if err != nil {
		return []byte(`{"error":"encode command response"}`)
	}

	return payload
}

func (h *adminHandler) status(ctx context.Context) (transport.Status, error) {
	store, err := pollingworker.NewDedupeServiceContext(ctx, h.config.PollingDB)
	if err != nil {
		return transport.Status{}, err
	}
	defer func() { _ = store.Close() }()

	stats, err := store.Stats(ctx)
	if err != nil {
		return transport.Status{}, err
	}

	bus, err := eventbus.Inspect(h.config.NATSURL)
	if err != nil {
		return transport.Status{}, err
	}
	defer func() { _ = bus.Close() }()

	stream, err := bus.JetStream().StreamInfo(eventbus.StreamName)
	if err != nil {
		return transport.Status{}, err
	}

	result := transport.Status{
		StartedAt: h.startedAt, HTTPAddress: h.config.HTTPAddress,
		Pipeline: transport.PipelineStats{
			Accessions: stats.Accessions, OutboxPending: stats.OutboxPending,
			OutboxPublished: stats.OutboxPublished, Failures: stats.Failures,
		},
		StreamMessages: stream.State.Msgs, StreamBytes: stream.State.Bytes,
		Consumers: nil,
	}

	for _, durable := range []string{
		eventworker.SelectionDurable, eventworker.DownloadDurable,
		eventworker.ProcessDurable, eventworker.OwnershipDurable,
		news.DurableName,
	} {
		consumer, consumerErr := bus.JetStream().ConsumerInfo(
			eventbus.StreamName, durable,
		)
		if errors.Is(consumerErr, nats.ErrConsumerNotFound) {
			continue
		}

		if consumerErr != nil {
			return transport.Status{}, consumerErr
		}

		result.Consumers = append(result.Consumers, transport.ConsumerStatus{
			Name: durable, Pending: consumer.NumPending,
			InFlight:    consumer.NumAckPending,
			Redelivered: consumer.NumRedelivered,
		})
	}

	return result, nil
}

func (h *adminHandler) listFailures(ctx context.Context,
	request transport.Request,
) ([]transport.Failure, error) {
	if request.Limit <= 0 || request.Limit > 500 {
		request.Limit = 50
	}

	store, err := pollingworker.NewDedupeServiceContext(ctx, h.config.PollingDB)
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()

	rows, err := store.WorkFailures(ctx, request.Stage)
	if err != nil {
		return nil, err
	}

	if len(rows) > request.Limit {
		rows = rows[:request.Limit]
	}

	result := make([]transport.Failure, 0, len(rows))
	for _, row := range rows {
		result = append(result, transport.Failure{
			Stage: row.Stage, AccessionNumber: row.AccessionNumber,
			Attempts: row.Attempts, LastFailedAt: row.LastFailedAt,
			LastError: row.LastError,
		})
	}

	return result, nil
}

func (h *adminHandler) retryFailures(ctx context.Context,
	stage string,
) (int, error) {
	if stage != "download" && stage != "process" {
		return 0,
			errors.New("stage must be download or process")
	}

	store, err := pollingworker.NewDedupeServiceContext(ctx, h.config.PollingDB)
	if err != nil {
		return 0, err
	}
	defer func() { _ = store.Close() }()

	bus, err := eventbus.Connect(h.config.NATSURL)
	if err != nil {
		return 0, err
	}
	defer func() { _ = bus.Close() }()

	count, err := store.RetryWorkFailures(ctx, stage, bus)
	if err != nil {
		return 0, err
	}

	return count, nil
}

func ensureParentDirectories(config Config) error {
	for _, path := range []string{
		config.PollingDB, config.NewsDB, config.AnalysisDB,
		config.OwnershipDB,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return fmt.Errorf("create directory for %s: %w", path, err)
		}
	}

	return nil
}

func environmentValue(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}

	return fallback
}

func environmentInt(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}

	return value, nil
}

func webAddressFromEnvironment() string {
	for _, name := range []string{
		"EDGAR_HTTP_ADDRESS", "EDGAR_FILINGS_ADDRESS", "EDGAR_NEWS_ADDRESS",
	} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}

	return "127.0.0.1:8083"
}
