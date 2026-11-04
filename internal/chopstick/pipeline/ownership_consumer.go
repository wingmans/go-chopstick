package pipeline

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/nats-io/nats.go"
	"wingman.com/chopstick/internal/edgar"
	"wingman.com/chopstick/internal/ownership"
	ownershipimporter "wingman.com/chopstick/internal/ownership/importer"
	ownershipstore "wingman.com/chopstick/internal/ownership/store"
)

// RunOwnershipConsumer imports recognized ownership filings independently.
func RunOwnershipConsumer(ctx context.Context, bus *NATS, cfg OwnershipConfig,
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
		DiscoverySubject+".>", OwnershipDurable)
	if err != nil {
		return err
	}

	return consume(ctx, subscription, logger, "ownership", cfg.Drain,
		cfg.MaxAttempts, cfg.Failures, func(message *nats.Msg,
			event Event,
		) (string, error) {
			if !ownership.RecognizesForm(event.FormType) {
				return "ignored", message.Ack()
			}

			decision, err := processOwnershipEvent(ctx, store, downloader,
				cfg.FilingsDirectory, event)
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
	downloader *edgar.FilingDownloader, filingsDirectory string,
	event Event,
) (ownershipimporter.Decision, error) {
	filed, err := time.Parse("2006-01-02", event.FilingDate)
	if err != nil {
		return ownershipimporter.DecisionFailed, err
	}

	record := edgar.EdgarIndex{
		CIK: event.CIK, CompanyName: event.CompanyName,
		FormType: event.FormType, DateFiled: filed,
		FilingPath: event.FilingPath,
	}

	return ownershipimporter.ProcessOne(ctx, store, downloader,
		ownershipimporter.Config{
			Download: edgar.FilingDownloadConfig{
				Directory: filingsDirectory,
			},
		}, record)
}
