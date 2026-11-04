package ingest

import (
	"context"
	"time"

	pipeline "wingman.com/chopstick/internal/chopstick/pipeline"
	"wingman.com/chopstick/internal/ctxlog"
)

type Fetcher interface {
	FetchLatest(ctx context.Context) ([]pipeline.Event, error)
}

type Deduper interface {
	FilterNew(ctx context.Context, events []pipeline.Event) ([]pipeline.Event, error)
}

type DeadLetterSink interface {
	SaveDeadLetter(ctx context.Context, letter pipeline.DeadLetter) error
}

type DeadLetterProvider interface {
	DeadLetters() []pipeline.DeadLetter
}

type Worker struct {
	fetcher  Fetcher
	deduper  Deduper
	out      chan<- pipeline.Event
	interval time.Duration
}

func NewWorker(
	fetcher Fetcher,
	deduper Deduper,
	out chan<- pipeline.Event,
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

// RunOnce performs a single polling cycle: it fetches the latest events,
// processes dead letters, deduplicates the events, and emits the new ones.
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
