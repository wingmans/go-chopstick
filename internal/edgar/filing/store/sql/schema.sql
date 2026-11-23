CREATE TABLE IF NOT EXISTS filing_views (
    cik TEXT NOT NULL,
    accession TEXT NOT NULL,
    form_type TEXT NOT NULL,
    filing_date TEXT NOT NULL,
    report_date TEXT NOT NULL,
    company TEXT NOT NULL,
    status TEXT NOT NULL,
    facts INTEGER NOT NULL,
    schema_version INTEGER NOT NULL,
    payload BLOB NOT NULL,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (cik, accession)
);

CREATE INDEX IF NOT EXISTS filing_views_filing_date
    ON filing_views (filing_date DESC, accession DESC);

CREATE INDEX IF NOT EXISTS filing_views_cik_date
    ON filing_views (cik, filing_date, accession);

CREATE TABLE IF NOT EXISTS dividend_views (
    cik TEXT PRIMARY KEY,
    company TEXT NOT NULL,
    schema_version INTEGER NOT NULL,
    payload BLOB NOT NULL,
    updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS dividend_events (
    cik TEXT NOT NULL,
    event_id TEXT NOT NULL,
    declaration_date TEXT,
    ex_dividend_date TEXT,
    record_date TEXT,
    payable_date TEXT,
    amount_per_share TEXT,
    currency TEXT,
    type TEXT NOT NULL,
    status TEXT NOT NULL,
    confidence TEXT NOT NULL,
    payload BLOB NOT NULL,
    PRIMARY KEY (cik, event_id)
);

CREATE INDEX IF NOT EXISTS dividend_events_dates
    ON dividend_events (cik, payable_date, ex_dividend_date);

CREATE TABLE IF NOT EXISTS dividend_observations (
    observation_id TEXT PRIMARY KEY,
    cik TEXT NOT NULL,
    kind TEXT NOT NULL,
    period TEXT NOT NULL,
    context_id TEXT,
    context_start TEXT,
    context_end TEXT,
    accession TEXT NOT NULL,
    value TEXT NOT NULL,
    unit TEXT,
    form_type TEXT NOT NULL,
    filing_date TEXT NOT NULL,
    payload BLOB NOT NULL
);

CREATE INDEX IF NOT EXISTS dividend_observations_period
    ON dividend_observations (cik, period, filing_date);
