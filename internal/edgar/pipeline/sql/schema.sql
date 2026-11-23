CREATE TABLE IF NOT EXISTS processed_accessions (
    accession_number TEXT PRIMARY KEY,
    cik TEXT NOT NULL,
    form_type TEXT NOT NULL,
    source_feed TEXT NOT NULL,
    processed_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS polling_dead_letters (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    received_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    feed_url TEXT NOT NULL,
    title TEXT NOT NULL,
    link TEXT NOT NULL,
    raw_entry TEXT NOT NULL,
    reason TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS polling_outbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL UNIQUE,
    subject TEXT NOT NULL,
    payload BLOB NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    published_at DATETIME,
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS work_failures (
    stage TEXT NOT NULL,
    accession_number TEXT NOT NULL,
    subject TEXT NOT NULL,
    payload BLOB NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    first_failed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_failed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_error TEXT NOT NULL,
    resolved_at DATETIME,
    PRIMARY KEY (stage, accession_number)
);
