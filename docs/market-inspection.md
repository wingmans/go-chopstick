# Inspect market data

Initialize a new market database and batch directory before running the
normal Yahoo price workflow:

```bash
./bin/chop setup --market
./bin/chop prices status
./bin/chop prices fetch
./bin/chop prices sync
```

Use the same `--database` and `--batch-dir` flags for setup and sync when you
keep a separate database or batch directory. Market setup requires no NATS
connection.

`sync` works through the manifest and exits when it runs out of eligible work.
Its summary reports how many requests were attempted, how many failed, and
how many symbols remain pending. Individual ticker failures still allow a
successful exit. Schedule the next invocation externally to resume pending
work.

Downloaded price series fail closed when Yahoo returns duplicate, unordered,
stale, or suspiciously discontinuous observations. Failed coverage validation
does not advance the synchronization watermark; the work remains pending for
retry and inspection with `chop prices status`.

CSV batches contain two record types. All `price` rows are written first,
followed by `action` rows for dividends and splits. Inspect the first column
before interpreting adjacent dates as price continuity.

Yahoo series are identified by provider and symbol. CIK and company name are
current manifest metadata, so an issuer transition updates them without
creating a second price series.

`fetch` attempts at most 25 symbols by default. Use repeated `--ticker` flags
to limit any command to specific manifest members:

```bash
chop prices fetch --ticker MSFT --ticker AAPL
```

Queue a known range for another attempt with `repair`:

```bash
chop prices repair --ticker MSFT --from 2026-10-01 --to 2026-10-31
```

Run `chop prices <command> --help` for paths, limits, and date options.

Open the market database with column headings:

```bash
sqlite3 -header -column data/market.db
```

Inspect overall coverage:

```sql
SELECT
    MIN(observation_date) AS earliest,
    MAX(observation_date) AS latest,
    COUNT(*) AS prices,
    COUNT(DISTINCT symbol) AS securities
FROM market_prices
WHERE provider = 'yahoo';
```

Inspect Microsoft coverage:

```sql
SELECT
    s.symbol,
    MIN(p.observation_date) AS earliest,
    MAX(p.observation_date) AS latest,
    COUNT(*) AS prices
FROM market_securities AS s
JOIN market_prices AS p
  ON p.provider = s.provider
 AND p.symbol = s.symbol
WHERE s.provider = 'yahoo'
  AND s.symbol = 'MSFT'
GROUP BY s.symbol;
```

Inspect stored watermarks:

```sql
SELECT
    s.symbol,
    w.covered_through,
    w.observed_from,
    w.observed_through
FROM market_sync_state AS w
JOIN market_securities AS s
  ON s.provider = w.provider
 AND s.symbol = w.symbol
WHERE w.provider = 'yahoo'
ORDER BY s.symbol
LIMIT 20;
```

Count securities lacking history from 2020:

```sql
SELECT COUNT(*) AS missing_2020_history
FROM market_sync_state
WHERE provider = 'yahoo'
  AND (observed_from IS NULL OR observed_from > '2020-01-01');
```

## Future pricing pipeline work

Add trusted exchange calendars so the pricing pipeline can validate individual
expected trading sessions, not only suspicious multi-day gaps. Associate each
security with its listing exchange and retain a versioned local calendar with
its source and retrieval date.

After each synchronization, compare observations with the expected sessions.
Queue missing sessions for repair. If a second fetch still has no observation,
record a visible exception for legitimate trading halts, suspensions, or other
security-specific no-trade events instead of silently treating the date as
covered.
