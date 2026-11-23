CREATE TABLE IF NOT EXISTS feed_items (
    stream_sequence INTEGER PRIMARY KEY,
    event_id TEXT NOT NULL UNIQUE,
    accession_number TEXT NOT NULL UNIQUE,
    cik TEXT NOT NULL,
    company_name TEXT NOT NULL,
    form_type TEXT NOT NULL,
    filing_date TEXT NOT NULL,
    filing_url TEXT NOT NULL,
    filing_path TEXT NOT NULL DEFAULT '',
    source_feed TEXT NOT NULL,
    selected INTEGER NOT NULL DEFAULT 0,
    selection_reason TEXT NOT NULL DEFAULT '',
    selection_mode TEXT NOT NULL DEFAULT '',
    selection_set TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS feed_items_form
    ON feed_items (form_type);

CREATE INDEX IF NOT EXISTS feed_items_cik
    ON feed_items (cik);
