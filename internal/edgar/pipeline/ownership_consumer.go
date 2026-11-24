package pipeline

import (
	"context"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"wingman.com/chopstick/internal/edgar/ownership"
	ownershipimporter "wingman.com/chopstick/internal/edgar/ownership/importer"
	ownershipstore "wingman.com/chopstick/internal/edgar/ownership/store"
	"wingman.com/chopstick/internal/ingest/sec"
	"wingman.com/chopstick/internal/platform/outbound"
)

// RunOwnershipConsumer imports recognized ownership filings independently.
func RunOwnershipConsumer(ctx context.Context, bus *NATS, cfg OwnershipConfig,
	logger *slog.Logger,
) error {
	timings := &ownershipTimings{}

	open := ownershipstore.Open
	if cfg.ExistingDatabase {
		open = ownershipstore.OpenExisting
	}

	store, err := open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	downloader, err := sec.NewFilingDownloader(
		outbound.NewClient(30*time.Second), sec.FilingDownloadConfig{
			Directory: cfg.FilingsDirectory, UserAgent: cfg.UserAgent,
			BaseURL: cfg.BaseURL, RequestGate: cfg.RequestGate,
		})
	if err != nil {
		return err
	}

	subscription, err := pullSubscription(bus,
		DiscoverySubject+".>", OwnershipDurable)
	if err != nil {
		return err
	}

	return consume(ctx, subscription, logger, "ownership", cfg.Drain,
		cfg.MaxAttempts, cfg.Failures, timings.fields, func(message *nats.Msg,
			event Event,
		) (string, error) {
			if !ownership.RecognizesForm(event.FormType) {
				return "ignored", message.Ack()
			}

			decision, measured, err := processOwnershipEvent(ctx, store, downloader,
				cfg.FilingsDirectory, event)
			timings.add(measured)

			if err != nil {
				return "", err
			}

			if decision != ownershipimporter.DecisionFailed {
				logger.Debug("ownership filing handled",
					"accession", event.AccessionNumber,
					"decision", decision.String())
			}

			return decision.String(), message.Ack()
		})
}

func processOwnershipEvent(ctx context.Context, store *ownershipstore.Store,
	downloader *sec.FilingDownloader, filingsDirectory string,
	event Event,
) (ownershipimporter.Decision, ownershipimporter.Timings, error) {
	filed, err := time.Parse("2006-01-02", event.FilingDate)
	if err != nil {
		return ownershipimporter.DecisionFailed, ownershipimporter.Timings{}, err
	}

	record := sec.EdgarIndex{
		CIK: event.CIK, CompanyName: event.CompanyName,
		FormType: event.FormType, DateFiled: filed,
		FilingPath: event.FilingPath,
	}

	return ownershipimporter.ProcessOneMeasured(ctx, store, downloader,
		ownershipimporter.Config{
			Download: sec.FilingDownloadConfig{
				Directory: filingsDirectory,
			},
		}, record)
}

type ownershipTimings struct {
	lookup   time.Duration
	download time.Duration
	parse    time.Duration
	store    time.Duration
}

func (t *ownershipTimings) add(value ownershipimporter.Timings) {
	t.lookup += value.Lookup
	t.download += value.Download
	t.parse += value.Parse
	t.store += value.Store
}

func (t *ownershipTimings) fields() []any {
	return []any{
		"lookup_time", t.lookup,
		"download_time", t.download,
		"parse_time", t.parse,
		"store_time", t.store,
	}
}
