package eventworker

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/nats-io/nats.go"
	"wingman.com/chopstick/internal/edgar"
	"wingman.com/chopstick/internal/eventbus"
	filingwork "wingman.com/chopstick/internal/filing/work"
	"wingman.com/chopstick/internal/ownership"
	ownershippipeline "wingman.com/chopstick/internal/ownership/pipeline"
	ownershipstore "wingman.com/chopstick/internal/ownership/store"
)

func Ownership(ctx context.Context, bus *eventbus.NATS, cfg OwnershipConfig,
	logger *slog.Logger,
) error {
	store, err := ownershipstore.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	downloader, err := edgar.NewFilingDownloader(&http.Client{
		Timeout: 30 * time.Second,
	}, edgar.FilingDownloadConfig{
		Directory: cfg.FilingsDirectory,
		UserAgent: cfg.UserAgent,
		BaseURL:   cfg.BaseURL,
	})
	if err != nil {
		return err
	}

	subscription, err := pullSubscription(bus,
		eventbus.DiscoverySubject+".>", OwnershipDurable)
	if err != nil {
		return err
	}

	return consume(ctx, subscription, logger, "ownership", cfg.Drain,
		cfg.MaxAttempts, cfg.Failures, func(message *nats.Msg,
			event filingwork.Event,
		) error {
			if !ownership.RecognizesForm(event.FormType) {
				return message.Ack()
			}

			filed, err := time.Parse("2006-01-02", event.FilingDate)
			if err != nil {
				return err
			}

			record := edgar.EdgarIndex{
				CIK: event.CIK, CompanyName: event.CompanyName,
				FormType: event.FormType, DateFiled: filed,
				FilingPath: event.FilingPath,
			}

			decision, err := ownershippipeline.ProcessOne(ctx, store,
				downloader, ownershippipeline.Config{}, record)
			if err != nil {
				return err
			}

			logger.Info("ownership filing handled",
				"accession", event.AccessionNumber,
				"decision", decision.String())

			return message.Ack()
		})
}

// Download consumes discovery events and publishes downloaded events only
// after the authoritative raw submission is present.
