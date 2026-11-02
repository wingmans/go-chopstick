package eventworker

import (
	"context"
	"log/slog"

	"github.com/nats-io/nats.go"
	"wingman.com/chopstick/internal/edgar"
	"wingman.com/chopstick/internal/eventbus"
	filingwork "wingman.com/chopstick/internal/filing/work"
	"wingman.com/chopstick/internal/pipeline"
)

func Process(ctx context.Context, bus *eventbus.NATS, cfg ProcessConfig,
	logger *slog.Logger,
) error {
	subscription, err := pullSubscription(bus,
		eventbus.DownloadedSubject+".>", ProcessDurable)
	if err != nil {
		return err
	}

	return consume(ctx, subscription, logger, "process", cfg.Drain,
		cfg.MaxAttempts, cfg.Failures, func(message *nats.Msg,
			event filingwork.Event,
		) (string, error) {
			path, err := edgar.LocalFilingPath(cfg.FilingsDirectory,
				event.FilingPath)
			if err != nil {
				return "", err
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

					return "", message.Ack()
				}

				return "", err
			}

			logger.Info("filing processed", "accession", event.AccessionNumber,
				"action", result.Action)

			return "", message.Ack()
		})
}
