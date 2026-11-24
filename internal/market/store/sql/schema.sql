CREATE TABLE IF NOT EXISTS market_securities (
    provider TEXT NOT NULL,
    symbol TEXT NOT NULL,
    cik TEXT NOT NULL,
    name TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (provider, symbol)
);

CREATE TABLE IF NOT EXISTS market_prices (
    provider TEXT NOT NULL,
    symbol TEXT NOT NULL,
    observation_date TEXT NOT NULL,
    currency TEXT NOT NULL,
    open REAL,
    close REAL NOT NULL,
    adjusted_close REAL NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (provider, symbol, observation_date),
    FOREIGN KEY (provider, symbol)
        REFERENCES market_securities (provider, symbol)
);

CREATE INDEX IF NOT EXISTS market_prices_date
    ON market_prices (provider, observation_date, symbol);

CREATE TABLE IF NOT EXISTS market_corporate_actions (
    provider TEXT NOT NULL,
    symbol TEXT NOT NULL,
    source_id TEXT NOT NULL,
    action_date TEXT NOT NULL,
    action_type TEXT NOT NULL,
    amount REAL,
    numerator REAL,
    denominator REAL,
    reconciled_at TEXT,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (provider, symbol, source_id),
    FOREIGN KEY (provider, symbol)
        REFERENCES market_securities (provider, symbol)
);

CREATE INDEX IF NOT EXISTS market_actions_date
    ON market_corporate_actions
    (provider, symbol, action_date);

CREATE TABLE IF NOT EXISTS market_sync_state (
    provider TEXT NOT NULL,
    symbol TEXT NOT NULL,
    bootstrap_complete INTEGER NOT NULL DEFAULT 0,
    covered_from TEXT,
    covered_through TEXT,
    observed_from TEXT,
    observed_through TEXT,
    reconciliation_required INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TEXT,
    failure_count INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
    lease_until TEXT,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (provider, symbol),
    FOREIGN KEY (provider, symbol)
        REFERENCES market_securities (provider, symbol)
);

CREATE TABLE IF NOT EXISTS market_repairs (
    provider TEXT NOT NULL,
    symbol TEXT NOT NULL,
    from_date TEXT NOT NULL,
    through_date TEXT NOT NULL,
    completed_at TEXT,
    created_at TEXT NOT NULL,
    PRIMARY KEY (provider, symbol, from_date, through_date),
    FOREIGN KEY (provider, symbol)
        REFERENCES market_securities (provider, symbol)
);
