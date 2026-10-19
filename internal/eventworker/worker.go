// Package eventworker contains the serial download and processing consumers.
package eventworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/nats-io/nats.go"

	"wingman.com/fetch-ecb/internal/edgar"
	"wingman.com/fetch-ecb/internal/eventbus"
	"wingman.com/fetch-ecb/internal/pipeline"
	"wingman.com/fetch-ecb/internal/pollingworker"
)

const (
	downloadDurable = "edgar-download-v1"
	processDurable  = "edgar-process-v1"
)

// DownloadConfig configures the serial filing download consumer.
type DownloadConfig struct {
	Directory string
	UserAgent string
	BaseURL   string
	Drain     bool
}

// ProcessConfig configures the serial parse and aggregate consumer.
type ProcessConfig struct {
	FilingsDirectory string
	ParsedDirectory  string
	AnalysisDBPath   string
	Drain            bool
}

// Download consumes discovery events and publishes downloaded events only
// after the authoritative raw submission is present.
func Download(ctx context.Context, bus *eventbus.NATS, cfg DownloadConfig,
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

	subscription, err := pullSubscription(bus, eventbus.DiscoverySubject+".>",
		downloadDurable)
	if err != nil {
		return err
	}

	return consume(ctx, subscription, logger, cfg.Drain, func(message *nats.Msg,
		event pollingworker.FilingEvent,
	) error {
		record := edgar.EdgarIndex{
			CIK:         event.CIK,
			CompanyName: event.CompanyName,
			FormType:    event.FormType,
			FilingPath:  event.FilingPath,
			IndexPath:   "",
		}
		if err := downloader.Download(ctx, record); err != nil {
			return err
		}

		event.IndexPath = ""

		payload, err := json.Marshal(event)
		if err != nil {
			return err
		}

		subject := eventbus.SubjectFor(eventbus.DownloadedSubject,
			event.FormType)

		messageID := "filing.downloaded:" + event.AccessionNumber
		if err := bus.Publish(ctx, subject, messageID, payload); err != nil {
			return err
		}

		logger.Info("filing ready", "accession", event.AccessionNumber)

		return message.Ack()
	})
}

// Process consumes downloaded events and runs the idempotent parser and
// aggregate writers. It deliberately processes one message at a time.
func Process(ctx context.Context, bus *eventbus.NATS, cfg ProcessConfig,
	logger *slog.Logger,
) error {
	subscription, err := pullSubscription(bus,
		eventbus.DownloadedSubject+".>", processDurable)
	if err != nil {
		return err
	}

	return consume(ctx, subscription, logger, cfg.Drain, func(message *nats.Msg,
		event pollingworker.FilingEvent,
	) error {
		path, err := edgar.LocalFilingPath(cfg.FilingsDirectory,
			event.FilingPath)
		if err != nil {
			return err
		}

		result, err := pipeline.ProcessSubmission(ctx, path,
			pipeline.ProcessingConfig{
				Directory:        cfg.ParsedDirectory,
				FilingsDirectory: cfg.FilingsDirectory,
				AnalysisDBPath:   cfg.AnalysisDBPath,
				FormType:         event.FormType,
			})
		if err != nil {
			if result.Action != "" {
				logger.Warn("filing persisted with processing diagnostics",
					"accession", event.AccessionNumber,
					"action", result.Action, "error", err)

				return message.Ack()
			}

			return err
		}

		logger.Info("filing processed", "accession", event.AccessionNumber,
			"action", result.Action)

		return message.Ack()
	})
}

func pullSubscription(bus *eventbus.NATS, subject, durable string) (
	*nats.Subscription, error,
) {
	subscription, err := bus.JetStream().PullSubscribe(subject, durable,
		nats.BindStream(eventbus.StreamName),
		nats.ManualAck(),
		nats.AckExplicit(),
		nats.AckWait(30*time.Minute),
		nats.DeliverAll(),
	)
	if err != nil {
		return nil, fmt.Errorf("subscribe %s: %w", subject, err)
	}

	return subscription, nil
}

func consume(ctx context.Context, subscription *nats.Subscription,
	logger *slog.Logger, drain bool,
	handle func(*nats.Msg, pollingworker.FilingEvent) error,
) error {
	for {
		messages, err := subscription.Fetch(1, nats.MaxWait(time.Second))
		if errors.Is(err, nats.ErrTimeout) {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			if drain {
				info, infoErr := subscription.ConsumerInfo()
				if infoErr != nil {
					return fmt.Errorf("inspect work backlog: %w", infoErr)
				}

				if info.NumPending == 0 && info.NumAckPending == 0 {
					return nil
				}
			}

			continue
		}

		if err != nil {
			return fmt.Errorf("fetch work: %w", err)
		}

		message := messages[0]

		var event pollingworker.FilingEvent
		if err := json.Unmarshal(message.Data, &event); err != nil {
			_ = message.Term()

			continue
		}

		filed, err := time.Parse("2006-01-02", event.FilingDate)
		if err != nil {
			_ = message.Term()

			logger.Error("discarding event with invalid filing date",
				"filing_date", event.FilingDate,
				"accession", event.AccessionNumber)

			continue
		}

		if filed.Year() < edgar.EarliestPipelineYear {
			_ = message.Ack()

			logger.Info("skipping filing before pipeline support boundary",
				"filing_date", event.FilingDate,
				"accession", event.AccessionNumber)

			continue
		}

		if err := handle(message, event); err != nil {
			_ = message.NakWithDelay(time.Minute)

			logger.Warn("event work failed; retry scheduled", "error", err,
				"accession", event.AccessionNumber)
		}
	}
}
