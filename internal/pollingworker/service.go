package pollingworker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"wingman.com/fetch-ecb/internal/ctxlog"
	"wingman.com/fetch-ecb/internal/eventbus"
)

type ServiceConfig struct {
	Interval  time.Duration
	DBPath    string
	UserAgent string
	Publisher eventbus.Publisher
}

func Run(ctx context.Context, cfg ServiceConfig) error {
	if cfg.Interval <= 0 {
		return errors.New("poll interval must be positive")
	}

	logger := ctxlog.FromContext(ctx)
	out := make(chan FilingEvent, 256)

	go emitLogs(ctx, logger, out)

	store, err := NewDedupeServiceContext(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	worker := NewWorker(NewSECFetcher(cfg.UserAgent), store, out, cfg.Interval)
	if err := runCycle(ctx, worker, store, cfg.Publisher); err != nil {
		logger.Warn("polling cycle failed", "error", err)
	}

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if !errors.Is(ctx.Err(), context.Canceled) {
				return ctx.Err()
			}

			return nil
		case <-ticker.C:
			if err := runCycle(ctx, worker, store, cfg.Publisher); err != nil {
				logger.Warn("polling cycle failed", "error", err)
			}
		}
	}
}

func runCycle(ctx context.Context, worker *Worker, store *DedupeService,
	publisher eventbus.Publisher,
) error {
	if err := store.PublishPending(ctx, publisher); err != nil {
		return err
	}

	if err := worker.RunOnce(ctx); err != nil {
		return err
	}

	return store.PublishPending(ctx, publisher)
}

func emitLogs(ctx context.Context, logger *slog.Logger, out <-chan FilingEvent) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-out:
			logger.Debug("polling event emitted", "accession", event.AccessionNumber,
				"cik", event.CIK, "form", event.FormType, "date", event.FilingDate)
		}
	}
}
