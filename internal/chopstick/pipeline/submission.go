package pipeline

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/nats-io/nats.go"
	"wingman.com/chopstick/internal/edgar"
)

// RunSubmission acquires authoritative raw submissions for selected filings.
func RunSubmission(ctx context.Context, bus *NATS, cfg SubmissionConfig,
	logger *slog.Logger,
) error {
	downloader, err := edgar.NewFilingDownloader(&http.Client{
		Timeout: 30 * time.Second,
	}, edgar.FilingDownloadConfig{
		Directory: cfg.Directory,
		UserAgent: cfg.UserAgent,
		BaseURL:   cfg.BaseURL,
	})
	if err != nil {
		return err
	}

	subscription, err := pullSubscription(bus, SelectedSubject+".>",
		SubmissionDurable)
	if err != nil {
		return err
	}

	return consume(ctx, subscription, logger, "download", cfg.Drain,
		cfg.MaxAttempts, cfg.Failures, func(message *nats.Msg,
			event Event,
		) (string, error) {
			record := edgar.EdgarIndex{
				CIK:         event.CIK,
				CompanyName: event.CompanyName,
				FormType:    event.FormType,
				FilingPath:  event.FilingPath,
				IndexPath:   "",
			}
			if err := downloader.Download(ctx, record); err != nil {
				return "", err
			}

			event.IndexPath = ""

			payload, err := json.Marshal(event)
			if err != nil {
				return "", err
			}

			subject := SubjectFor(DownloadedSubject,
				event.FormType)

			messageID := "filing.downloaded:" + event.AccessionNumber
			if err := bus.Publish(ctx, subject, messageID, payload); err != nil {
				return "", err
			}

			logger.Info("filing ready", "accession", event.AccessionNumber)

			return "", message.Ack()
		})
}

// Process consumes downloaded events and runs the idempotent parser and
// aggregate writers. It deliberately processes one message at a time.
