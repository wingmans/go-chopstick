-- name: ApplyNewsItem :exec
INSERT INTO feed_items (
    stream_sequence,
    event_id,
    accession_number,
    cik,
    company_name,
    form_type,
    filing_date,
    filing_url,
    filing_path,
    source_feed,
    selected,
    selection_reason,
    selection_mode,
    selection_set
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(accession_number) DO UPDATE SET
    stream_sequence = excluded.stream_sequence,
    event_id = excluded.event_id,
    selected = excluded.selected,
    selection_reason = excluded.selection_reason,
    selection_mode = excluded.selection_mode,
    selection_set = excluded.selection_set;

-- name: ListNewsItems :many
SELECT
    stream_sequence,
    accession_number,
    cik,
    company_name,
    form_type,
    filing_date,
    filing_url,
    filing_path,
    source_feed,
    selected,
    selection_reason,
    selection_mode,
    selection_set
FROM feed_items
WHERE (
        company_name LIKE sqlc.arg(query_pattern)
        OR accession_number LIKE sqlc.arg(query_pattern)
        OR cik LIKE sqlc.arg(query_pattern)
        OR sqlc.arg(query_pattern) = ''
    )
  AND (cik = sqlc.arg(cik) OR sqlc.arg(cik) = '')
  AND (form_type = sqlc.arg(form_type) OR sqlc.arg(form_type) = '')
  AND (selected = sqlc.arg(selected) OR sqlc.arg(selected) = -1)
  AND (filing_date >= sqlc.arg(from_date) OR sqlc.arg(from_date) = '')
  AND (filing_date <= sqlc.arg(to_date) OR sqlc.arg(to_date) = '')
ORDER BY stream_sequence DESC
LIMIT sqlc.arg(row_limit);

-- name: NewsItem :one
SELECT
    stream_sequence,
    accession_number,
    cik,
    company_name,
    form_type,
    filing_date,
    filing_url,
    filing_path,
    source_feed,
    selected,
    selection_reason,
    selection_mode,
    selection_set
FROM feed_items
WHERE accession_number = ?;
