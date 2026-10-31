# Inspect market data

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
    COUNT(DISTINCT security_id) AS securities
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
 AND p.security_id = s.security_id
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
 AND s.security_id = w.security_id
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
