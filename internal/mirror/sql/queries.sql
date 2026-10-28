-- name: UpsertSubmission :exec
INSERT INTO submissions (
    object_key,
    accession,
    cik,
    form_type,
    filing_date,
    filing_year,
    logical_bytes,
    stored_bytes,
    sha256
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(object_key) DO UPDATE SET
    accession = excluded.accession,
    cik = excluded.cik,
    form_type = excluded.form_type,
    filing_date = excluded.filing_date,
    filing_year = excluded.filing_year,
    logical_bytes = excluded.logical_bytes,
    stored_bytes = excluded.stored_bytes,
    sha256 = excluded.sha256;

-- name: SubmissionByAccession :one
SELECT
    object_key,
    accession,
    cik,
    form_type,
    filing_date,
    filing_year,
    logical_bytes,
    stored_bytes,
    sha256
FROM submissions
WHERE accession = ?;

-- name: SearchSubmissions :many
SELECT
    object_key,
    accession,
    cik,
    form_type,
    filing_date,
    filing_year,
    logical_bytes,
    stored_bytes,
    sha256
FROM submissions
WHERE (cik = sqlc.arg(cik) OR sqlc.arg(cik) = '')
  AND (form_type = sqlc.arg(form_type) OR sqlc.arg(form_type) = '')
  AND (filing_year = sqlc.arg(filing_year)
       OR sqlc.arg(filing_year) = 0)
  AND (accession > sqlc.arg(cursor) OR sqlc.arg(cursor) = '')
ORDER BY accession
LIMIT sqlc.arg(row_limit);

-- name: CatalogStats :one
SELECT
    COUNT(*) AS object_count,
    CAST(COALESCE(SUM(logical_bytes), 0) AS INTEGER) AS logical_bytes,
    CAST(COALESCE(SUM(stored_bytes), 0) AS INTEGER) AS stored_bytes
FROM submissions;

-- name: DeleteSubmission :exec
DELETE FROM submissions
WHERE object_key = ?;
