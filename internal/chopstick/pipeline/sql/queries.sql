-- name: SaveDeadLetter :exec
INSERT INTO polling_dead_letters (feed_url, title, link, raw_entry, reason)
VALUES (?, ?, ?, ?, ?);

-- name: IsDuplicate :one
SELECT EXISTS(
    SELECT 1
    FROM processed_accessions
    WHERE accession_number = sqlc.arg(accession_number)
);

-- name: SaveProcessedAccession :execresult
INSERT INTO processed_accessions (
    accession_number,
    cik,
    form_type,
    source_feed
) VALUES (?, ?, ?, ?)
ON CONFLICT(accession_number) DO NOTHING;

-- name: SaveOutboxMessage :exec
INSERT INTO polling_outbox (event_id, subject, payload)
VALUES (?, ?, ?)
ON CONFLICT(event_id) DO NOTHING;

-- name: PendingOutboxMessages :many
SELECT id, event_id, subject, payload
FROM polling_outbox
WHERE published_at IS NULL
ORDER BY id;

-- name: RecordOutboxPublishFailure :exec
UPDATE polling_outbox
SET attempts = attempts + 1,
    last_error = sqlc.arg(last_error)
WHERE id = sqlc.arg(id);

-- name: MarkOutboxPublished :exec
UPDATE polling_outbox
SET published_at = CURRENT_TIMESTAMP,
    attempts = attempts + 1,
    last_error = ''
WHERE id = ?;

-- name: PendingOutboxCount :one
SELECT COUNT(*)
FROM polling_outbox
WHERE published_at IS NULL;

-- name: RecordWorkFailure :exec
INSERT INTO work_failures (
    stage,
    accession_number,
    subject,
    payload,
    attempts,
    last_error
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
    resolved_at = NULL;

-- name: WorkFailureAttempts :one
SELECT attempts
FROM work_failures
WHERE stage = sqlc.arg(stage)
  AND accession_number = sqlc.arg(accession_number);

-- name: ResolveWorkFailure :exec
UPDATE work_failures
SET resolved_at = CURRENT_TIMESTAMP
WHERE stage = sqlc.arg(stage)
  AND accession_number = sqlc.arg(accession_number)
  AND resolved_at IS NULL;

-- name: WorkFailures :many
SELECT
    stage,
    accession_number,
    subject,
    payload,
    attempts,
    last_failed_at,
    last_error
FROM work_failures
WHERE resolved_at IS NULL
  AND (
    CAST(sqlc.arg(filter_stage) AS TEXT) = ''
    OR stage = CAST(sqlc.arg(filter_stage) AS TEXT)
  )
ORDER BY last_failed_at DESC, accession_number;

-- name: MarkWorkFailureRetryQueued :exec
UPDATE work_failures
SET attempts = 0,
    last_error = 'retry queued'
WHERE stage = sqlc.arg(stage)
  AND accession_number = sqlc.arg(accession_number);
