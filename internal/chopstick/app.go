package chopstick

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"wingman.com/chopstick/internal/edgar/ingest"
	pipeline "wingman.com/chopstick/internal/edgar/pipeline"
	"wingman.com/chopstick/internal/ingest/sec"
	"wingman.com/chopstick/internal/platform/appdata"
	"wingman.com/chopstick/internal/platform/ctxlog"
	"wingman.com/chopstick/internal/platform/outbound"
	"wingman.com/chopstick/internal/platform/transport"
)

func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	if err := config.Validate(); err != nil {
		return err
	}

	if err := ensureParentDirectories(config); err != nil {
		return err
	}

	lock, err := appdata.AcquireLock(appdata.Path("chopd.lock"))
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()

	bootstrap, err := pipeline.Connect(ctx, config.NATSURL)
	if err != nil {
		return err
	}

	if err := bootstrap.Close(); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	requestGate := sec.NewRequestGate()

	startedAt := time.Now().UTC()
	admin := &adminHandler{
		config: config, startedAt: startedAt, logger: logger,
	}

	commandServer := transport.NewServer(config.CommandAddress)

	reconcileLogger := logger.With("role", "reconcile")
	go reconcileAtStartup(ctx, config, reconcileLogger, requestGate)

	for _, role := range []string{
		"atom", "selection", "submission", "extraction", "ownership",
	} {
		go superviseRole(ctx, role, logger, func() error {
			return runRole(ctx, role, config, logger, requestGate)
		})
	}

	errorsCh := make(chan error, 2)
	go func() {
		errorsCh <- runRole(ctx, "web", config, logger, requestGate)
	}()
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
	logger *slog.Logger, requestGate *sec.RequestGate,
) {
	client := outbound.NewClient(30 * time.Second)
	if err := ensureMasterIndex(ctx, client, config, logger,
		requestGate); err != nil &&
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
	logger *slog.Logger, requestGate *sec.RequestGate,
) error {
	logger = logger.With("role", role)
	ctx = ctxlog.WithLogger(ctx, logger)

	bus, err := pipeline.Connect(ctx, config.NATSURL)
	if err != nil {
		return err
	}
	defer func() { _ = bus.Close() }()

	switch role {
	case "atom":
		return ingest.RunAtom(ctx, ingest.AtomConfig{
			Interval: config.Interval, DBPath: config.PollingDB,
			UserAgent: config.UserAgent, Publisher: bus,
			RequestGate: requestGate,
		})
	case "selection":
		return pipeline.RunSelection(ctx, bus, pipeline.SelectionConfig{
			SetName: config.SetName, CIKs: config.CIKs,
			FormTypes: config.FormTypes,
		}, logger)
	case "submission":
		failures, openErr := pipeline.Open(
			ctx, config.PollingDB,
		)
		if openErr != nil {
			return openErr
		}
		defer func() { _ = failures.Close() }()

		return pipeline.RunSubmission(ctx, bus, pipeline.SubmissionConfig{
			Directory: config.FilingsDirectory, UserAgent: config.UserAgent,
			BaseURL: config.BaseURL, MaxAttempts: config.MaxAttempts,
			Failures: failures, RequestGate: requestGate,
		}, logger)
	case "extraction":
		failures, openErr := pipeline.Open(
			ctx, config.PollingDB,
		)
		if openErr != nil {
			return openErr
		}
		defer func() { _ = failures.Close() }()

		return pipeline.RunExtraction(ctx, bus, pipeline.ExtractionConfig{
			FilingsDirectory: config.FilingsDirectory,
			ParsedDirectory:  config.ParsedDirectory,
			AnalysisDBPath:   config.AnalysisDB,
			MaxAttempts:      config.MaxAttempts, Failures: failures,
		}, logger)
	case "ownership":
		failures, openErr := pipeline.Open(
			ctx, config.PollingDB,
		)
		if openErr != nil {
			return openErr
		}
		defer func() { _ = failures.Close() }()

		return pipeline.RunOwnershipConsumer(ctx, bus, pipeline.OwnershipConfig{
			FilingsDirectory: config.FilingsDirectory,
			DatabasePath:     config.OwnershipDB, UserAgent: config.UserAgent,
			BaseURL: config.BaseURL, MaxAttempts: config.MaxAttempts,
			Failures: failures, RequestGate: requestGate,
		}, logger)
	case "web":
		return runWeb(ctx, bus, config, logger)
	default:
		return fmt.Errorf("unknown application role %q", role)
	}
}
