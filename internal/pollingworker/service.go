package pollingworker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"wingman.com/fetch-ecb/internal/ctxlog"
)

type ServiceConfig struct {
	Interval  time.Duration
	DBPath    string
	UserAgent string
}

func Run(ctx context.Context, cfg ServiceConfig) error {
	logger := ctxlog.FromContext(ctx)
	out := make(chan FilingEvent, 256)

	go emitLogs(ctx, logger, out)

	store, err := NewDedupeServiceContext(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	worker := NewWorker(NewSECFetcher(cfg.UserAgent), store, out, cfg.Interval)
	if err := worker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	return nil
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
