CREATE TABLE IF NOT EXISTS submissions (
    object_key TEXT PRIMARY KEY,
    accession TEXT NOT NULL UNIQUE,
    cik TEXT NOT NULL,
    form_type TEXT NOT NULL,
    filing_date TEXT NOT NULL,
    filing_year INTEGER NOT NULL,
    logical_bytes INTEGER NOT NULL,
    stored_bytes INTEGER NOT NULL,
    sha256 TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS submissions_cik_date
    ON submissions (cik, filing_date);

CREATE INDEX IF NOT EXISTS submissions_form_date
    ON submissions (form_type, filing_date);

CREATE INDEX IF NOT EXISTS submissions_year_form
    ON submissions (filing_year, form_type);
