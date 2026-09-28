package pollingworker

import (
	"context"
	"time"

	"wingman.com/fetch-ecb/internal/ctxlog"
)

type Fetcher interface {
	FetchLatest(ctx context.Context) ([]FilingEvent, error)
}

type Deduper interface {
	FilterNew(ctx context.Context, events []FilingEvent) ([]FilingEvent, error)
}

type DeadLetterSink interface {
	SaveDeadLetter(ctx context.Context, letter DeadLetter) error
}

type DeadLetterProvider interface {
	DeadLetters() []DeadLetter
}

type Worker struct {
	fetcher  Fetcher
	deduper  Deduper
	out      chan<- FilingEvent
	interval time.Duration
}

func NewWorker(
	fetcher Fetcher,
	deduper Deduper,
	out chan<- FilingEvent,
	interval time.Duration,
) *Worker {
	if interval <= 0 {
		interval = time.Hour
	}

	return &Worker{
		fetcher:  fetcher,
		deduper:  deduper,
		out:      out,
		interval: interval,
	}
}

func (w *Worker) Run(ctx context.Context) error {
	if err := w.RunOnce(ctx); err != nil {
		ctxlog.FromContext(ctx).Warn("polling cycle failed", "error", err)
	}

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := w.RunOnce(ctx); err != nil {
				ctxlog.FromContext(ctx).Warn("polling cycle failed", "error", err)
			}
		}
	}
}

func (w *Worker) RunOnce(ctx context.Context) error {
	logger := ctxlog.FromContext(ctx)
	logger.Debug("polling cycle started")

	events, err := w.fetcher.FetchLatest(ctx)
	if err != nil {
		return err
	}

	if provider, ok := w.fetcher.(DeadLetterProvider); ok {
		if sink, ok := w.deduper.(DeadLetterSink); ok {
			for _, letter := range provider.DeadLetters() {
				if err := sink.SaveDeadLetter(ctx, letter); err != nil {
					return err
				}
			}
		}
	}

	logger.Debug("polling feed fetched", "events", len(events))

	fresh, err := w.deduper.FilterNew(ctx, events)
	if err != nil {
		return err
	}

	logger.Debug("polling events deduplicated", "new_events", len(fresh))

	for _, ev := range fresh {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case w.out <- ev:
			logger.Debug("polling event emitted", "accession", ev.AccessionNumber)
		}
	}

	return nil
}
