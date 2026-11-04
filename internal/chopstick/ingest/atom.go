package ingest

import (
	"context"
	"errors"
	"log/slog"
	"time"

	pipeline "wingman.com/chopstick/internal/chopstick/pipeline"
	"wingman.com/chopstick/internal/ctxlog"
)

// AtomConfig configures the continuous SEC Atom source.
type AtomConfig struct {
	Interval  time.Duration
	DBPath    string
	UserAgent string
	Publisher pipeline.Publisher
}

// RunAtom discovers recent filings until the context is cancelled.
func RunAtom(ctx context.Context, cfg AtomConfig) error {
	if cfg.Interval <= 0 {
		return errors.New("poll interval must be positive")
	}

	logger := ctxlog.FromContext(ctx)
	out := make(chan pipeline.Event, 256)

	go emitLogs(ctx, logger, out)

	store, err := pipeline.Open(ctx, cfg.DBPath)
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

func runCycle(ctx context.Context, worker *Worker, store *pipeline.Store,
	publisher pipeline.Publisher,
) error {
	if err := store.PublishPending(ctx, publisher); err != nil {
		return err
	}

	if err := worker.RunOnce(ctx); err != nil {
		return err
	}

	return store.PublishPending(ctx, publisher)
}

func emitLogs(ctx context.Context, logger *slog.Logger, out <-chan pipeline.Event) {
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
