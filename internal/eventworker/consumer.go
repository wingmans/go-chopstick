package eventworker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
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
	handle func(*nats.Msg, filingwork.Event) (string, error),
) error {
	if maxAttempts <= 0 {
		maxAttempts = defaultAttempts
	}

	stats := consumerStats{
		lastReport: time.Now(), outcomes: make(map[string]uint64),
	}
	if err := reportProgress(ctx, subscription, logger, stage, &stats,
		true); err != nil {
		return err
	}

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
					return reportProgress(ctx, subscription, logger, stage, &stats,
						true)
				}
			}

			if err := reportProgress(ctx, subscription, logger, stage, &stats,
				false); err != nil {
				return err
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

		outcome, workErr := handle(message, event)
		if workErr != nil {
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
			if outcome != "" {
				stats.outcomes[outcome]++
			}

			if failures != nil {
				if err := failures.ResolveWorkFailure(ctx, stage,
					event.AccessionNumber); err != nil {
					return err
				}
			}
		}

		if err := reportProgress(ctx, subscription, logger, stage, &stats,
			false); err != nil {
			return err
		}
	}
}

type consumerStats struct {
	lastReport time.Time
	scanned    uint64
	completed  uint64
	skipped    uint64
	failed     uint64
	outcomes   map[string]uint64
	previous   consumerSnapshot
	reported   bool
}

type consumerSnapshot struct {
	scanned     uint64
	completed   uint64
	skipped     uint64
	failed      uint64
	pending     uint64
	ackPending  int
	redelivered int
}

type consumerReport struct {
	level   slog.Level
	message string
	rate    float64
}

func reportProgress(ctx context.Context, subscription *nats.Subscription,
	logger *slog.Logger, stage string, stats *consumerStats, force bool,
) error {
	if !force && time.Since(stats.lastReport) < progressEvery {
		return nil
	}

	info, err := subscription.ConsumerInfo()
	if err != nil {
		return fmt.Errorf("inspect %s backlog: %w", stage, err)
	}

	now := time.Now()
	report := stats.nextReport(consumerSnapshot{
		scanned: stats.scanned, completed: stats.completed,
		skipped: stats.skipped, failed: stats.failed,
		pending: info.NumPending, ackPending: info.NumAckPending,
		redelivered: info.NumRedelivered,
	}, now)

	attributes := []any{
		"scanned", stats.scanned, "completed", stats.completed,
		"skipped", stats.skipped, "failed", stats.failed,
		"pending", info.NumPending, "ack_pending", info.NumAckPending,
		"redelivered", info.NumRedelivered,
		"rate_per_second", report.rate,
	}

	keys := make([]string, 0, len(stats.outcomes))
	for outcome := range stats.outcomes {
		keys = append(keys, outcome)
	}

	sort.Strings(keys)

	for _, outcome := range keys {
		attributes = append(attributes, outcome, stats.outcomes[outcome])
	}

	logger.Log(ctx, report.level, report.message, attributes...)

	return nil
}

func (s *consumerStats) nextReport(current consumerSnapshot,
	now time.Time,
) consumerReport {
	elapsed := now.Sub(s.lastReport)
	delta := current.scanned - s.previous.scanned

	rate := float64(0)
	if elapsed > 0 {
		rate = float64(delta) / elapsed.Seconds()
	}

	queueChanged := current.pending != s.previous.pending ||
		current.ackPending != s.previous.ackPending ||
		current.redelivered != s.previous.redelivered
	countersChanged := current.scanned != s.previous.scanned ||
		current.completed != s.previous.completed ||
		current.skipped != s.previous.skipped ||
		current.failed != s.previous.failed
	backlog := current.pending > 0 || current.ackPending > 0
	previousBacklog := s.previous.pending > 0 || s.previous.ackPending > 0
	drained := !backlog && (previousBacklog || delta > 0)

	level := slog.LevelDebug
	message := "consumer heartbeat"

	if countersChanged || queueChanged || (!s.reported && backlog) {
		level = slog.LevelInfo
		message = "consumer progress"
	}

	if drained {
		level = slog.LevelInfo
		message = "consumer backlog drained"
	}

	s.previous = current
	s.lastReport = now
	s.reported = true

	return consumerReport{level: level, message: message, rate: rate}
}
