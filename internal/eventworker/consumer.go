package eventworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"

	"wingman.com/chopstick/internal/edgar"
	"wingman.com/chopstick/internal/eventbus"
	filingwork "wingman.com/chopstick/internal/filing/work"
)

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
	logger *slog.Logger, stage string, drain bool, maxAttempts int,
	failures FailureStore,
	handle func(*nats.Msg, filingwork.Event) error,
) error {
	if maxAttempts <= 0 {
		maxAttempts = defaultAttempts
	}

	stats := consumerStats{started: time.Now(), lastReport: time.Now()}
	if err := reportProgress(subscription, logger, stage, &stats, true); err != nil {
		return err
	}

	for {
		messages, err := subscription.Fetch(1, nats.MaxWait(time.Second))
		if errors.Is(err, nats.ErrTimeout) {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			if err := reportProgress(subscription, logger, stage, &stats,
				false); err != nil {
				return err
			}

			if drain {
				info, infoErr := subscription.ConsumerInfo()
				if infoErr != nil {
					return fmt.Errorf("inspect work backlog: %w", infoErr)
				}

				if info.NumPending == 0 && info.NumAckPending == 0 {
					return reportProgress(subscription, logger, stage, &stats,
						true)
				}
			}

			continue
		}

		if err != nil {
			return fmt.Errorf("fetch work: %w", err)
		}

		message := messages[0]
		stats.scanned++

		var event filingwork.Event
		if err := json.Unmarshal(message.Data, &event); err != nil {
			_ = message.Term()
			stats.failed++

			logger.Error("discarding invalid event", "error", err)

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
			stats.skipped++

			logger.Debug("skipping filing before pipeline support boundary",
				"filing_date", event.FilingDate,
				"accession", event.AccessionNumber)

			continue
		}

		if workErr := handle(message, event); workErr != nil {
			stats.failed++

			attempts := 1
			if failures != nil {
				attempts, err = failures.RecordWorkFailure(ctx, stage,
					event.AccessionNumber, message.Subject, message.Data,
					workErr)
				if err != nil {
					_ = message.NakWithDelay(time.Minute)

					return err
				}
			}

			if attempts >= maxAttempts {
				_ = message.Term()

				logger.Error("event work exhausted retries",
					"error", workErr, "accession", event.AccessionNumber,
					"attempts", attempts)
			} else {
				_ = message.NakWithDelay(time.Minute)

				logger.Warn("event work failed; retry scheduled",
					"error", workErr, "accession", event.AccessionNumber,
					"attempt", attempts, "max_attempts", maxAttempts)
			}
		} else {
			stats.completed++

			if failures != nil {
				if err := failures.ResolveWorkFailure(ctx, stage,
					event.AccessionNumber); err != nil {
					return err
				}
			}
		}

		if err := reportProgress(subscription, logger, stage, &stats,
			false); err != nil {
			return err
		}
	}
}

type consumerStats struct {
	started    time.Time
	lastReport time.Time
	scanned    uint64
	completed  uint64
	skipped    uint64
	failed     uint64
}

func reportProgress(subscription *nats.Subscription, logger *slog.Logger,
	stage string, stats *consumerStats, force bool,
) error {
	if !force && time.Since(stats.lastReport) < progressEvery {
		return nil
	}

	info, err := subscription.ConsumerInfo()
	if err != nil {
		return fmt.Errorf("inspect %s backlog: %w", stage, err)
	}

	elapsed := time.Since(stats.started)

	rate := float64(0)
	if elapsed > 0 {
		rate = float64(stats.scanned) / elapsed.Seconds()
	}

	logger.Info("consumer progress",
		"scanned", stats.scanned, "completed", stats.completed,
		"skipped", stats.skipped, "failed", stats.failed,
		"pending", info.NumPending, "ack_pending", info.NumAckPending,
		"redelivered", info.NumRedelivered, "rate_per_second", rate)
	stats.lastReport = time.Now()

	return nil
}
