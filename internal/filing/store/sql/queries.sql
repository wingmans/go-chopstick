-- name: UpsertFilingView :exec
INSERT INTO filing_views (
    cik,
    accession,
    form_type,
    filing_date,
    report_date,
    company,
    status,
    facts,
    schema_version,
    payload,
    updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(cik, accession) DO UPDATE SET
    form_type = excluded.form_type,
    filing_date = excluded.filing_date,
    report_date = excluded.report_date,
    company = excluded.company,
    status = excluded.status,
    facts = excluded.facts,
    schema_version = excluded.schema_version,
    payload = excluded.payload,
    updated_at = excluded.updated_at;

-- name: HasFilingView :one
SELECT EXISTS(
    SELECT 1
    FROM filing_views
    WHERE cik = sqlc.arg(cik)
      AND accession = sqlc.arg(accession)
);

-- name: FilingViewStatus :one
SELECT status
FROM filing_views
WHERE cik = sqlc.arg(cik)
  AND accession = sqlc.arg(accession);

-- name: UpsertDividendView :exec
INSERT INTO dividend_views (
    cik,
    company,
    schema_version,
    payload,
    updated_at
) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(cik) DO UPDATE SET
    company = excluded.company,
    schema_version = excluded.schema_version,
    payload = excluded.payload,
    updated_at = excluded.updated_at;

-- name: DeleteDividendEvents :exec
DELETE FROM dividend_events
WHERE cik = ?;

-- name: InsertDividendEvent :exec
INSERT INTO dividend_events (
    cik,
    event_id,
    declaration_date,
    ex_dividend_date,
    record_date,
    payable_date,
    amount_per_share,
    currency,
    type,
    status,
    confidence,
    payload
) VALUES (
    sqlc.arg(cik),
    sqlc.arg(event_id),
    sqlc.narg(declaration_date),
    sqlc.narg(ex_dividend_date),
    sqlc.narg(record_date),
    sqlc.narg(payable_date),
    sqlc.narg(amount_per_share),
    sqlc.narg(currency),
    sqlc.arg(type),
    sqlc.arg(status),
    sqlc.arg(confidence),
    sqlc.arg(payload)
);

-- name: DeleteDividendObservations :exec
DELETE FROM dividend_observations
WHERE cik = ?;

-- name: InsertDividendObservation :exec
INSERT INTO dividend_observations (
    observation_id,
    cik,
    kind,
    period,
    context_id,
    context_start,
    context_end,
    accession,
    value,
    unit,
    form_type,
    filing_date,
    payload
) VALUES (
    sqlc.arg(observation_id),
    sqlc.arg(cik),
    sqlc.arg(kind),
    sqlc.arg(period),
    sqlc.narg(context_id),
    sqlc.narg(context_start),
    sqlc.narg(context_end),
    sqlc.arg(accession),
    sqlc.arg(value),
    sqlc.narg(unit),
    sqlc.arg(form_type),
    sqlc.arg(filing_date),
    sqlc.arg(payload)
);

-- name: FilingViewPayload :one
SELECT payload
FROM filing_views
WHERE cik = sqlc.arg(cik)
  AND accession = sqlc.arg(accession);

-- name: ListFilingViewPayloads :many
SELECT payload
FROM filing_views
ORDER BY filing_date DESC, accession DESC;

-- name: DividendViewPayload :one
SELECT payload
FROM dividend_views
WHERE cik = ?;
