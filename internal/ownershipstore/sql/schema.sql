CREATE TABLE ownership_filings (
    accession TEXT PRIMARY KEY,
    form_type TEXT NOT NULL,
    form_family TEXT NOT NULL,
    filing_date TEXT NOT NULL,
    report_date TEXT NOT NULL,
    source_path TEXT NOT NULL,
    source_sha256 TEXT NOT NULL,
    parser_version TEXT NOT NULL,
    is_amendment INTEGER NOT NULL,
    amendment_type TEXT NOT NULL,
    amendment_number TEXT NOT NULL,
    original_submission_date TEXT NOT NULL,
    previous_accession TEXT NOT NULL,
    report_kind TEXT NOT NULL,
    status TEXT NOT NULL,
    diagnostic TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (status IN ('processed', 'unsupported', 'failed'))
);

CREATE INDEX ownership_filings_family_date
    ON ownership_filings (form_family, filing_date, accession);

CREATE INDEX ownership_filings_status
    ON ownership_filings (status, parser_version, filing_date);

CREATE TABLE ownership_parties (
    party_id TEXT PRIMARY KEY,
    cik TEXT NOT NULL,
    name TEXT NOT NULL,
    party_type TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE INDEX ownership_parties_cik
    ON ownership_parties (cik)
    WHERE cik != '';

CREATE TABLE ownership_filing_parties (
    accession TEXT NOT NULL,
    party_id TEXT NOT NULL,
    role TEXT NOT NULL,
    ordinal INTEGER NOT NULL,
    is_director INTEGER NOT NULL,
    is_officer INTEGER NOT NULL,
    is_ten_percent_owner INTEGER NOT NULL,
    is_other INTEGER NOT NULL,
    officer_title TEXT NOT NULL,
    member_of_group TEXT NOT NULL,
    PRIMARY KEY (accession, role, ordinal),
    FOREIGN KEY (accession) REFERENCES ownership_filings (accession)
        ON DELETE CASCADE,
    FOREIGN KEY (party_id) REFERENCES ownership_parties (party_id)
);

CREATE INDEX ownership_filing_parties_party
    ON ownership_filing_parties (party_id, role, accession);

CREATE TABLE ownership_signatures (
    accession TEXT NOT NULL,
    ordinal INTEGER NOT NULL,
    signer_name TEXT NOT NULL,
    signature_date TEXT NOT NULL,
    PRIMARY KEY (accession, ordinal),
    FOREIGN KEY (accession) REFERENCES ownership_filings (accession)
        ON DELETE CASCADE
);

CREATE TABLE ownership_securities (
    security_id TEXT PRIMARY KEY,
    issuer_party_id TEXT,
    title TEXT NOT NULL,
    security_class TEXT NOT NULL,
    security_kind TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (issuer_party_id) REFERENCES ownership_parties (party_id)
);

CREATE TABLE ownership_security_identifiers (
    security_id TEXT NOT NULL,
    identifier_type TEXT NOT NULL,
    identifier_value TEXT NOT NULL,
    source_accession TEXT NOT NULL,
    confidence TEXT NOT NULL,
    PRIMARY KEY (
        security_id,
        identifier_type,
        identifier_value,
        source_accession
    ),
    FOREIGN KEY (security_id) REFERENCES ownership_securities (security_id),
    FOREIGN KEY (source_accession) REFERENCES ownership_filings (accession)
        ON DELETE CASCADE
);

CREATE INDEX ownership_security_identifier_lookup
    ON ownership_security_identifiers
    (identifier_type, identifier_value, security_id);

CREATE TABLE institutional_positions (
    accession TEXT NOT NULL,
    row_number INTEGER NOT NULL,
    manager_party_id TEXT NOT NULL,
    security_id TEXT NOT NULL,
    report_period TEXT NOT NULL,
    issuer_name TEXT NOT NULL,
    security_class TEXT NOT NULL,
    cusip TEXT NOT NULL,
    figi TEXT NOT NULL,
    reported_value TEXT NOT NULL,
    amount TEXT NOT NULL,
    amount_type TEXT NOT NULL,
    put_call TEXT NOT NULL,
    investment_discretion TEXT NOT NULL,
    other_managers TEXT NOT NULL,
    voting_sole TEXT NOT NULL,
    voting_shared TEXT NOT NULL,
    voting_none TEXT NOT NULL,
    PRIMARY KEY (accession, row_number),
    FOREIGN KEY (accession) REFERENCES ownership_filings (accession)
        ON DELETE CASCADE,
    FOREIGN KEY (manager_party_id) REFERENCES ownership_parties (party_id),
    FOREIGN KEY (security_id) REFERENCES ownership_securities (security_id)
);

CREATE INDEX institutional_positions_cusip_period
    ON institutional_positions (cusip, report_period, manager_party_id);

CREATE INDEX institutional_positions_manager_period
    ON institutional_positions (manager_party_id, report_period, cusip);

CREATE TABLE institutional_summaries (
    accession TEXT PRIMARY KEY,
    entry_count TEXT NOT NULL,
    total_value TEXT NOT NULL,
    included_manager_count TEXT NOT NULL,
    FOREIGN KEY (accession) REFERENCES ownership_filings (accession)
        ON DELETE CASCADE
);

CREATE TABLE institutional_notices (
    accession TEXT NOT NULL,
    row_number INTEGER NOT NULL,
    manager_party_id TEXT NOT NULL,
    report_period TEXT NOT NULL,
    notice_kind TEXT NOT NULL,
    reporting_manager_name TEXT NOT NULL,
    reporting_manager_file TEXT NOT NULL,
    PRIMARY KEY (accession, row_number),
    FOREIGN KEY (accession) REFERENCES ownership_filings (accession)
        ON DELETE CASCADE,
    FOREIGN KEY (manager_party_id) REFERENCES ownership_parties (party_id)
);

CREATE TABLE beneficial_ownership_reports (
    accession TEXT NOT NULL,
    row_number INTEGER NOT NULL,
    issuer_party_id TEXT NOT NULL,
    owner_party_id TEXT NOT NULL,
    security_id TEXT NOT NULL,
    cusip TEXT NOT NULL,
    event_date TEXT NOT NULL,
    shares TEXT NOT NULL,
    percentage TEXT NOT NULL,
    sole_voting_power TEXT NOT NULL,
    shared_voting_power TEXT NOT NULL,
    sole_dispositive_power TEXT NOT NULL,
    shared_dispositive_power TEXT NOT NULL,
    reporting_person_type TEXT NOT NULL,
    filer_category TEXT NOT NULL,
    PRIMARY KEY (accession, row_number),
    FOREIGN KEY (accession) REFERENCES ownership_filings (accession)
        ON DELETE CASCADE,
    FOREIGN KEY (issuer_party_id) REFERENCES ownership_parties (party_id),
    FOREIGN KEY (owner_party_id) REFERENCES ownership_parties (party_id),
    FOREIGN KEY (security_id) REFERENCES ownership_securities (security_id)
);

CREATE INDEX beneficial_reports_issuer_owner_date
    ON beneficial_ownership_reports
    (issuer_party_id, owner_party_id, event_date, accession);

CREATE INDEX beneficial_reports_cusip_date
    ON beneficial_ownership_reports (cusip, event_date, accession);

CREATE TABLE beneficial_ownership_narratives (
    accession TEXT NOT NULL,
    row_number INTEGER NOT NULL,
    item_name TEXT NOT NULL,
    field_name TEXT NOT NULL,
    field_text TEXT NOT NULL,
    PRIMARY KEY (accession, row_number),
    FOREIGN KEY (accession) REFERENCES ownership_filings (accession)
        ON DELETE CASCADE
);

CREATE TABLE insider_holdings (
    accession TEXT NOT NULL,
    row_number INTEGER NOT NULL,
    owner_ordinal INTEGER NOT NULL,
    issuer_party_id TEXT NOT NULL,
    owner_party_id TEXT NOT NULL,
    security_id TEXT NOT NULL,
    report_date TEXT NOT NULL,
    is_derivative INTEGER NOT NULL,
    amount TEXT NOT NULL,
    direct_or_indirect TEXT NOT NULL,
    nature_of_ownership TEXT NOT NULL,
    transaction_form_type TEXT NOT NULL,
    exercise_price TEXT NOT NULL,
    exercise_date TEXT NOT NULL,
    expiration_date TEXT NOT NULL,
    underlying_title TEXT NOT NULL,
    underlying_shares TEXT NOT NULL,
    footnote_refs TEXT NOT NULL,
    PRIMARY KEY (accession, row_number, owner_ordinal),
    FOREIGN KEY (accession) REFERENCES ownership_filings (accession)
        ON DELETE CASCADE,
    FOREIGN KEY (issuer_party_id) REFERENCES ownership_parties (party_id),
    FOREIGN KEY (owner_party_id) REFERENCES ownership_parties (party_id),
    FOREIGN KEY (security_id) REFERENCES ownership_securities (security_id)
);

CREATE INDEX insider_holdings_owner_date
    ON insider_holdings
    (issuer_party_id, owner_party_id, report_date, accession);

CREATE TABLE insider_transactions (
    accession TEXT NOT NULL,
    row_number INTEGER NOT NULL,
    owner_ordinal INTEGER NOT NULL,
    issuer_party_id TEXT NOT NULL,
    owner_party_id TEXT NOT NULL,
    security_id TEXT NOT NULL,
    report_date TEXT NOT NULL,
    is_derivative INTEGER NOT NULL,
    transaction_date TEXT NOT NULL,
    execution_date TEXT NOT NULL,
    transaction_form_type TEXT NOT NULL,
    transaction_code TEXT NOT NULL,
    equity_swap_involved TEXT NOT NULL,
    acquired_disposed TEXT NOT NULL,
    amount TEXT NOT NULL,
    price TEXT NOT NULL,
    shares_after TEXT NOT NULL,
    direct_or_indirect TEXT NOT NULL,
    nature_of_ownership TEXT NOT NULL,
    exercise_price TEXT NOT NULL,
    exercise_date TEXT NOT NULL,
    expiration_date TEXT NOT NULL,
    underlying_title TEXT NOT NULL,
    underlying_shares TEXT NOT NULL,
    footnote_refs TEXT NOT NULL,
    PRIMARY KEY (accession, row_number, owner_ordinal),
    FOREIGN KEY (accession) REFERENCES ownership_filings (accession)
        ON DELETE CASCADE,
    FOREIGN KEY (issuer_party_id) REFERENCES ownership_parties (party_id),
    FOREIGN KEY (owner_party_id) REFERENCES ownership_parties (party_id),
    FOREIGN KEY (security_id) REFERENCES ownership_securities (security_id)
);

CREATE INDEX insider_transactions_owner_date
    ON insider_transactions
    (issuer_party_id, owner_party_id, transaction_date, accession);

CREATE TABLE ownership_footnotes (
    accession TEXT NOT NULL,
    footnote_id TEXT NOT NULL,
    footnote_text TEXT NOT NULL,
    PRIMARY KEY (accession, footnote_id),
    FOREIGN KEY (accession) REFERENCES ownership_filings (accession)
        ON DELETE CASCADE
);

CREATE TABLE ownership_coverage (
    form_family TEXT NOT NULL,
    from_date TEXT NOT NULL,
    through_date TEXT NOT NULL,
    parser_version TEXT NOT NULL,
    expected_count INTEGER NOT NULL,
    processed_count INTEGER NOT NULL,
    failed_count INTEGER NOT NULL,
    unsupported_count INTEGER NOT NULL,
    coverage_status TEXT NOT NULL,
    started_at TEXT NOT NULL,
    completed_at TEXT NOT NULL,
    PRIMARY KEY (form_family, from_date, through_date, parser_version),
    CHECK (coverage_status IN ('pending', 'running', 'partial', 'complete'))
);

CREATE VIEW effective_institutional_positions AS
WITH baseline AS (
    SELECT
        p.manager_party_id,
        p.report_period,
        f.accession,
        f.filing_date,
        ROW_NUMBER() OVER (
            PARTITION BY p.manager_party_id, p.report_period
            ORDER BY f.filing_date DESC, f.accession DESC
        ) AS rank_number
    FROM institutional_positions p
    JOIN ownership_filings f ON f.accession = p.accession
    WHERE f.status = 'processed'
      AND f.amendment_type != 'NEW HOLDINGS'
    GROUP BY p.manager_party_id, p.report_period, f.accession, f.filing_date
), candidate AS (
    SELECT p.*
    FROM institutional_positions p
    JOIN ownership_filings f ON f.accession = p.accession
    LEFT JOIN baseline b
      ON b.manager_party_id = p.manager_party_id
     AND b.report_period = p.report_period
     AND b.rank_number = 1
    WHERE f.status = 'processed'
      AND (
          p.accession = b.accession
          OR (
              f.amendment_type = 'NEW HOLDINGS'
              AND (
                  b.accession IS NULL
                  OR f.filing_date > b.filing_date
                  OR (
                      f.filing_date = b.filing_date
                      AND f.accession > b.accession
                  )
              )
          )
      )
), ranked AS (
    SELECT
        candidate.*,
        ROW_NUMBER() OVER (
            PARTITION BY
                manager_party_id,
                report_period,
                cusip,
                security_class,
                put_call,
                other_managers
            ORDER BY accession DESC, row_number DESC
        ) AS rank_number
    FROM candidate
)
SELECT *
FROM ranked
WHERE rank_number = 1;

CREATE VIEW latest_beneficial_ownership AS
WITH ranked AS (
    SELECT
        report.*,
        filing.form_type,
        filing.filing_date,
        ROW_NUMBER() OVER (
            PARTITION BY
                report.issuer_party_id,
                report.owner_party_id,
                report.security_id
            ORDER BY
                report.event_date DESC,
                filing.filing_date DESC,
                report.accession DESC
        ) AS rank_number
    FROM beneficial_ownership_reports report
    JOIN ownership_filings filing ON filing.accession = report.accession
    WHERE filing.status = 'processed'
)
SELECT *
FROM ranked
WHERE rank_number = 1;

CREATE VIEW reported_insider_holdings AS
SELECT
    holding.accession,
    holding.row_number,
    holding.owner_ordinal,
    holding.issuer_party_id,
    holding.owner_party_id,
    holding.security_id,
    holding.report_date AS as_of_date,
    holding.is_derivative,
    holding.amount,
    holding.direct_or_indirect,
    holding.nature_of_ownership
FROM insider_holdings holding
JOIN ownership_filings filing ON filing.accession = holding.accession
WHERE filing.status = 'processed'
UNION ALL
SELECT
    txn.accession,
    txn.row_number,
    txn.owner_ordinal,
    txn.issuer_party_id,
    txn.owner_party_id,
    txn.security_id,
    CASE
        WHEN txn.transaction_date != ''
        THEN txn.transaction_date
        ELSE txn.report_date
    END AS as_of_date,
    txn.is_derivative,
    txn.shares_after AS amount,
    txn.direct_or_indirect,
    txn.nature_of_ownership
FROM insider_transactions txn
JOIN ownership_filings filing ON filing.accession = txn.accession
WHERE filing.status = 'processed'
  AND txn.shares_after != '';

CREATE VIEW latest_insider_holdings AS
WITH ranked AS (
    SELECT
        reported.*,
        filing.filing_date,
        ROW_NUMBER() OVER (
            PARTITION BY
                reported.issuer_party_id,
                reported.owner_party_id,
                reported.security_id,
                reported.is_derivative,
                reported.direct_or_indirect,
                reported.nature_of_ownership
            ORDER BY
                reported.as_of_date DESC,
                filing.filing_date DESC,
                reported.accession DESC,
                reported.row_number DESC
        ) AS rank_number
    FROM reported_insider_holdings reported
    JOIN ownership_filings filing ON filing.accession = reported.accession
)
SELECT *
FROM ranked
WHERE rank_number = 1;
