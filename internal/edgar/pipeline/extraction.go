package pipeline

import (
	"context"
	"log/slog"

	"github.com/nats-io/nats.go"
	filingprocess "wingman.com/chopstick/internal/edgar/filing/process"
	"wingman.com/chopstick/internal/ingest/sec"
)

// RunExtraction parses acquired submissions and stores normalized results.
func RunExtraction(ctx context.Context, bus *NATS, cfg ExtractionConfig,
	logger *slog.Logger,
) error {
	subscription, err := pullSubscription(bus,
		DownloadedSubject+".>", ExtractionDurable)
	if err != nil {
		return err
	}

	return consume(ctx, subscription, logger, "process", cfg.Drain,
		cfg.MaxAttempts, cfg.Failures, nil, func(message *nats.Msg,
			event Event,
		) (string, error) {
			path, err := sec.LocalFilingPath(cfg.FilingsDirectory,
				event.FilingPath)
			if err != nil {
				return "", err
			}

			result, err := filingprocess.ProcessSubmission(ctx, path,
				filingprocess.ProcessingConfig{
					Directory:        cfg.ParsedDirectory,
					FilingsDirectory: cfg.FilingsDirectory,
					AnalysisDBPath:   cfg.AnalysisDBPath,
					AnalysisStore:    cfg.AnalysisStore,
					FormType:         event.FormType,
				})
			if err != nil {
				if result.Action != "" {
					logger.Warn("filing persisted with processing diagnostics",
						"accession", event.AccessionNumber,
						"action", result.Action, "error", err)

					return "", message.Ack()
				}

				return "", err
			}

			logger.Info("filing processed", "accession", event.AccessionNumber,
				"action", result.Action)

			return "", message.Ack()
		})
}
