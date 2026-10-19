package pollingworker

import (
	"context"
	"fmt"
	"time"

	"wingman.com/fetch-ecb/internal/eventbus"
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
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO work_failures (
			stage, accession_number, subject, payload, attempts, last_error
		) VALUES (?, ?, ?, ?, 1, ?)
		ON CONFLICT(stage, accession_number) DO UPDATE SET
			subject = excluded.subject,
			payload = excluded.payload,
			attempts = CASE
				WHEN work_failures.resolved_at IS NULL
				THEN work_failures.attempts + 1
				ELSE 1
			END,
			last_failed_at = CURRENT_TIMESTAMP,
			last_error = excluded.last_error,
			resolved_at = NULL`, stage, accession, subject, payload,
		workErr.Error())
	if err != nil {
		return 0, fmt.Errorf("record %s failure: %w", stage, err)
	}

	var attempts int
	if err := s.db.QueryRowContext(ctx, `
		SELECT attempts FROM work_failures
		WHERE stage = ? AND accession_number = ?`, stage, accession,
	).Scan(&attempts); err != nil {
		return 0, fmt.Errorf("read %s failure attempts: %w", stage, err)
	}

	return attempts, nil
}

// ResolveWorkFailure marks any previous failure for an item as resolved.
func (s *DedupeService) ResolveWorkFailure(ctx context.Context, stage, accession string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE work_failures
		SET resolved_at = CURRENT_TIMESTAMP
		WHERE stage = ? AND accession_number = ? AND resolved_at IS NULL`,
		stage, accession)
	if err != nil {
		return fmt.Errorf("resolve %s failure: %w", stage, err)
	}

	return nil
}

// WorkFailures returns unresolved failures, optionally for one stage.
func (s *DedupeService) WorkFailures(ctx context.Context, stage string) ([]WorkFailure, error) {
	query := `SELECT stage, accession_number, subject, payload, attempts,
		last_failed_at, last_error
		FROM work_failures WHERE resolved_at IS NULL`
	args := []any{}

	if stage != "" {
		query += " AND stage = ?"

		args = append(args, stage)
	}

	query += " ORDER BY last_failed_at DESC, accession_number"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read work failures: %w", err)
	}
	defer func() { _ = rows.Close() }()

	failures := make([]WorkFailure, 0)

	for rows.Next() {
		var failure WorkFailure
		if err := rows.Scan(&failure.Stage, &failure.AccessionNumber,
			&failure.Subject, &failure.Payload, &failure.Attempts,
			&failure.LastFailedAt, &failure.LastError); err != nil {
			return nil, fmt.Errorf("scan work failure: %w", err)
		}

		failures = append(failures, failure)
	}

	return failures, rows.Err()
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

		if _, err := s.db.ExecContext(ctx, `
			UPDATE work_failures
			SET attempts = 0, last_error = 'retry queued'
			WHERE stage = ? AND accession_number = ?`, failure.Stage,
			failure.AccessionNumber); err != nil {
			return retried, fmt.Errorf("mark retry queued: %w", err)
		}

		retried++
	}

	return retried, nil
}
