package pollingworker

import (
	"context"
	"fmt"
	"time"

	"wingman.com/chopstick/internal/eventbus"
	"wingman.com/chopstick/internal/pollingworker/dbgen"
)

// WorkFailure is the current failure state for one pipeline item.
type WorkFailure struct {
	Stage           string
	AccessionNumber string
	Subject         string
	Payload         []byte
	Attempts        int
	LastFailedAt    time.Time
	LastError       string
}

// RecordWorkFailure records one failed delivery and returns the consecutive
// attempt count. A failure after a resolved item starts at one again.
func (s *DedupeService) RecordWorkFailure(ctx context.Context, stage,
	accession, subject string, payload []byte, workErr error,
) (int, error) {
	if err := s.queries.RecordWorkFailure(
		ctx,
		dbgen.RecordWorkFailureParams{
			Stage:           stage,
			AccessionNumber: accession,
			Subject:         subject,
			Payload:         payload,
			LastError:       workErr.Error(),
		},
	); err != nil {
		return 0, fmt.Errorf("record %s failure: %w", stage, err)
	}

	attempts, err := s.queries.WorkFailureAttempts(
		ctx,
		dbgen.WorkFailureAttemptsParams{
			Stage:           stage,
			AccessionNumber: accession,
		},
	)
	if err != nil {
		return 0, fmt.Errorf("read %s failure attempts: %w", stage, err)
	}

	return int(attempts), nil
}

// ResolveWorkFailure marks any previous failure for an item as resolved.
func (s *DedupeService) ResolveWorkFailure(ctx context.Context, stage, accession string) error {
	err := s.queries.ResolveWorkFailure(ctx, dbgen.ResolveWorkFailureParams{
		Stage:           stage,
		AccessionNumber: accession,
	})
	if err != nil {
		return fmt.Errorf("resolve %s failure: %w", stage, err)
	}

	return nil
}

// WorkFailures returns unresolved failures, optionally for one stage.
func (s *DedupeService) WorkFailures(ctx context.Context, stage string) ([]WorkFailure, error) {
	rows, err := s.queries.WorkFailures(ctx, stage)
	if err != nil {
		return nil, fmt.Errorf("read work failures: %w", err)
	}

	failures := make([]WorkFailure, 0, len(rows))

	for _, row := range rows {
		failures = append(failures, WorkFailure{
			Stage:           row.Stage,
			AccessionNumber: row.AccessionNumber,
			Subject:         row.Subject,
			Payload:         row.Payload,
			Attempts:        int(row.Attempts),
			LastFailedAt:    row.LastFailedAt,
			LastError:       row.LastError,
		})
	}

	return failures, nil
}

// RetryWorkFailures republishes unresolved items and resets their consecutive
// attempt count. The failure remains visible until the worker succeeds.
func (s *DedupeService) RetryWorkFailures(ctx context.Context, stage string, publisher eventbus.Publisher) (int, error) {
	failures, err := s.WorkFailures(ctx, stage)
	if err != nil {
		return 0, err
	}

	retried := 0

	for _, failure := range failures {
		messageID := fmt.Sprintf("filing.retry:%s:%s:%d", failure.Stage,
			failure.AccessionNumber, time.Now().UTC().UnixNano())
		if err := publisher.Publish(ctx, failure.Subject, messageID,
			failure.Payload); err != nil {
			return retried, err
		}

		if err := s.queries.MarkWorkFailureRetryQueued(
			ctx,
			dbgen.MarkWorkFailureRetryQueuedParams{
				Stage:           failure.Stage,
				AccessionNumber: failure.AccessionNumber,
			},
		); err != nil {
			return retried, fmt.Errorf("mark retry queued: %w", err)
		}

		retried++
	}

	return retried, nil
}
