# Return Tracking V1

## Summary

Build a local historical return tracker for hypothetical model portfolios.

V1 will:

- support US-listed stocks, ADRs, and ETFs in USD;
- compare one model portfolio with one benchmark;
- use an ETF, such as SPY, as the proxy for a published index;
- allocate target weights once and then let them drift;
- use dividend- and split-adjusted daily values;
- calculate cumulative total return, CAGR, calendar-year returns, and excess
  return;
- generate Markdown and JSON reports;
- support bounded manual runs and a daily in-process Yahoo sync service;
- keep market-data acquisition outside the return engine.

This is historical portfolio analysis, not a real-account tracker or strategy
backtester. Reports must describe the result as hypothetical and warn about
survivorship bias when today's selected stocks are applied to earlier periods.

## Architecture

Use four independent layers:

```text
Yahoo downloader ─┐
                  ├─> CSV batches ─> market.db ─> verification
Stooq downloader ─┘                                  │
                                                     v
Manual CSV import ───────────────────────────> canonical dataset
                                                     │
                                                     v
                                                analysis.db
                                                     │
                                                     v
                                            return calculation
                                                     │
                                                     v
                                            Markdown and JSON
```

### Source-specific downloaders

Add two small console applications:

- `yahoo-prices` retrieves Yahoo chart data;
- `stooq-prices` retrieves Stooq historical data.

Each downloader will:

- accept stable security IDs, provider symbols, and a date range;
- write normalized provider observations to inspectable CSV landing batches;
- bulk-upsert each validated CSV batch into `market.db`;
- validate HTTP status, response shape, dates, numeric fields, and duplicates;
- never calculate portfolio returns.

Yahoo CSV batches are retained below `data/market/yahoo/batches/<ticker>/`.
Each file covers one work item and contains both price and corporate-action
records. The downloader writes the file atomically, reads and validates it
through the CSV contract, and then imports the complete batch in one database
transaction. These are normalized landing files, not raw provider responses.
An absent Yahoo open is retained as an empty CSV field and SQL `NULL`; it is
never inferred from another value. Close and adjusted close remain required
because they are necessary for price and return calculations.
Yahoo timestamps whose open, close, and adjusted close are all absent are
empty placeholders rather than observations and are skipped. Partially missing
close or adjusted-close values remain validation failures.

The Yahoo downloader uses four independently retryable work types:

- bootstrap all available history when a selected security has no stored data;
- increment after that security's successful coverage watermark;
- reconcile its complete history after a new or changed corporate action;
- repair an explicitly identified historical interval.

`fetch` attempts a bounded number of symbols and can also use a duration limit,
allowing a broad universe to converge over many small runs. `status` lists every
selected symbol that still requires work. `repair` records work for a future
fetch. A failed task retains its error and retry time, while successful price
and corporate-action upserts advance the watermark in the same transaction.
Lower and upper requested-coverage watermarks are stored separately from the
first and last observations returned by the provider. Asking for an earlier
`--from` date therefore schedules a historical bootstrap even when current
prices already exist. This distinction also prevents a recently listed
security from appearing incomplete merely because it has no older prices.

`sync` repeats bounded runs until the selected universe reaches the requested
date or its cycle deadline expires. `serve` performs an immediate catch-up and
then starts a new sync cycle at 21:00 `America/New_York` each day. The service
recomputes the next local wall-clock time after every run so daylight-saving
changes do not shift the schedule. It also reloads the constituent manifest on
each cycle so newly selected securities become pending work.

`sync` exits with status 0 when no selected symbol remains pending, status 2
when its deadline expires with pending work, and status 1 for an operational
failure. `status` is informational and exits successfully even when it reports
pending work. `make market-dev` runs the daily service separately from
`make dev`; development startup does not implicitly make provider requests.

The database is authoritative during normal incremental operation. Retained
CSV batches are not scanned when a security has stored prices. If its database
prices are absent, bootstrap planning triggers a CSV recovery scan first. The
longest contiguous retained prefix is bulk-upserted, after which Yahoo is only
asked for the remaining interval. Explicit repair and reconciliation work is
never suppressed merely because a CSV file exists.

`market.db` is a disposable development projection. Stop `make market-dev`
before rebuilding it, then run:

```sh
make rebuild-market
```

Its complete DDL lives in `internal/marketstore/sql/schema.sql`; named queries
live beside it in `queries.sql` and are compiled by `sqlc`. There are no market
database migrations. Opening an older or unversioned schema fails with a
rebuild instruction instead of changing it in place.

The target removes only `market.db` and its SQLite sidecars. It preserves the
CSV batches and runs `yahoo-prices sync`, which restores their longest
contiguous prefixes before requesting missing ranges. Override the development
defaults with `MARKET_MANIFEST` and `MARKET_FROM` when required:

```sh
make rebuild-market \
  MARKET_MANIFEST=data/sets/sp500.json \
  MARKET_FROM=2020-01-01
```

The provider price table in `market.db` is not partitioned. Its primary key is
provider, security ID, and observation date. Repeating work is safe because
database writes use upsert semantics.

Yahoo is the initial total-return candidate. Yahoo describes adjusted close as
including split and distribution adjustments on its
[historical-data page][yahoo-history]. Its automatic chart endpoint remains
unsupported and must be treated as an experimental, personal-use integration.
Yahoo also documents subscription and licensing restrictions around historical
downloads on its [download help page][yahoo-download].

Stooq is an experimental comparison source. Its downloader may collect data,
but Stooq data is not eligible for total-return promotion until its treatment
of dividends and splits is documented or independently validated.

### Market-data preprocessor

Add a separate `marketdata` command that is the only component aware of
multiple sources.

It will:

1. Validate each candidate independently.
2. Compare overlapping daily percentage changes rather than absolute adjusted
   price levels.
3. Exclude known split and dividend dates from comparisons against a source
   whose adjustment method is unknown.
4. Write a discrepancy report showing coverage, missing dates, percentile
   differences, and the largest differences.
5. Promote exactly one source for the complete comparison.
6. Require explicit approval, including a reason, when differences exceed the
   configured tolerance.
7. Produce a canonical dataset.

The default numerical tolerance will be 25 basis points in a daily percentage
change. Structural errors, such as duplicate dates, non-positive values,
currency mismatches, or missing constituents, always block promotion.

Fallback occurs only at whole-comparison level. A promoted dataset may never
mix Yahoo for one security and another source for a different security.

A fallback source must explicitly provide split- and dividend-adjusted values
and pass the same checks. Stooq is therefore not an automatic fallback in V1.
A manually prepared or broker-derived canonical file can be a fallback after
explicit approval.

### Return tracker

Add a standalone `returns` command. It will not contain Yahoo or Stooq clients
and will not perform fallback.

Its only market-data input is an approved canonical dataset imported into the
existing `analysis.db`. The calculation layer will depend on a
`MarketSeriesRepository` interface, with SQLite as the only V1 implementation.

The import will be idempotent by dataset ID and observation key. Older dataset
IDs remain available so an older report can be reproduced.

## Data Contracts and Commands

### Stable identity

Use an internal `security_id` as the permanent identity.

Do not use ticker, CIK, or ISIN as the primary key:

- a ticker identifies a listing and can change;
- a CIK identifies an SEC registrant, not necessarily one security;
- an ISIN identifies an instrument issue rather than the underlying company.

A security definition contains:

- stable ID;
- display name;
- currency, restricted to USD in V1;
- optional normalized CIK for linking to EDGAR;
- no provider-specific symbols.

A separate source-mapping manifest maps each security ID to Yahoo and Stooq
symbols. The return tracker does not read that mapping.

### Portfolio manifest

Keep user-authored definitions in JSON. The manifest contains:

- `schema_version`;
- securities;
- baskets;
- comparisons.

A basket contains:

- stable ID and display name;
- role: `portfolio` or `benchmark`;
- members identified by `security_id`;
- optional decimal target weights.

Weights follow these rules:

- either every member has a weight or none does;
- omitted weights mean equal allocation;
- supplied weights must total exactly 100%;
- weights are applied once at the effective start date;
- mixed weighted and unweighted members are invalid.

A single-ETF benchmark is represented as a one-member benchmark basket.
Custom benchmark baskets use the same model as portfolios.

A comparison contains:

- stable ID;
- portfolio basket ID;
- benchmark basket ID;
- requested start date;
- optional end date, defaulting to the latest approved observation.

### Candidate and canonical market data

A downloader candidate contains:

- security ID;
- observation date;
- currency;
- unadjusted close where available;
- adjusted close where available;
- dividend and split events where available;
- provider and provider symbol;
- declared adjustment capability.

The promoted canonical observation contract contains only:

- dataset ID;
- security ID;
- date;
- currency;
- positive `total_return_value`.

`total_return_value` is an index-like value whose ratios represent total
return. Its absolute level has no meaning.

A manual import must use this canonical format. Broker-specific conversion is
outside V1; the tool must not guess column meanings from arbitrary exports.

### Persistence

Use `market.db` for downloaded provider observations and their update state.
Keep promoted canonical datasets and return-analysis data in `analysis.db`.

Add normalized market-data concepts for:

- securities and optional CIK links;
- provider prices, corporate actions, and update watermarks;
- retained CSV landing batches;

Add normalized analysis concepts for:

- market datasets;
- dataset observations;
- import state and selected source.

Basket definitions remain user-authored JSON. Reports and canonical bundles
are files; normalized observations and import lineage are stored in SQLite.
Generated reports belong below `data/reports/returns/`.

### CLI workflow

The public workflow will be:

1. Fetch Yahoo and Stooq candidates.
2. Verify them and review the discrepancy report.
3. Promote one whole-comparison canonical dataset.
4. Import the promoted dataset.
5. Generate the requested comparison report.

Representative command responsibilities are:

- `yahoo-prices fetch`: execute a bounded batch of due Yahoo work;
- `yahoo-prices sync`: run bounded batches until the target is complete;
- `yahoo-prices serve`: catch up, then sync daily after the US market session;
- `yahoo-prices status`: list missing, stale, or deferred Yahoo work;
- `yahoo-prices repair`: queue a historical interval for repair;
- `stooq-prices fetch`: download Stooq provider candidate rows;
- `marketdata verify`: compare candidates without changing canonical data;
- `marketdata promote`: approve and create one canonical dataset;
- `returns import`: import a canonical dataset idempotently;
- `returns validate`: validate portfolio definitions;
- `returns report`: calculate and write Markdown and JSON reports.

Commands return a non-zero status for invalid work or a failed fetch attempt.
A successful bounded fetch may leave work visible to `status` for a later run.
Promotion and import will use atomic writes or transactions.

## Return Calculation and Reporting

### Date handling

For the requested period:

- choose the first US trading observation on or after the requested start;
- choose the last observation on or before the requested end;
- require every portfolio and benchmark member on every included date;
- reject leading gaps, internal gaps, late IPOs, and incomplete constituents;
- never silently drop a security or forward-fill a missing market observation.

The report must show both requested and effective dates.

### Basket calculation

For each basket:

1. Normalize each member's starting total-return value to one.
2. Allocate the starting basket value using its target weights.
3. Calculate each member's value from its adjusted-value ratio.
4. Sum member values for the basket value on every date.
5. Let weights drift; do not rebalance.

This represents dividend reinvestment according to the selected provider's
adjustment convention.

The calculations produce:

- daily basket value and daily return series;
- cumulative total return;
- CAGR when the period is at least one year;
- complete calendar-year returns;
- a clearly labelled partial-period return where applicable;
- portfolio minus benchmark excess return in percentage points.

Do not sum constituent returns. Do not average daily constituent returns after
the initial allocation.

### Report contents

The Markdown report contains:

- comparison name and generation time;
- requested and effective dates;
- portfolio and benchmark definitions;
- initial weights;
- total return, CAGR, and excess return;
- annual return comparison;
- data source and dataset ID;
- validation or approval warnings;
- methodology and limitations.

The JSON report contains the same metadata plus daily basket values and
returns. It is the machine-readable contract for later web and risk analytics.

Write timestamped immutable reports and refresh `latest.md` and `latest.json`
only after both formats complete successfully.

## Delivery Order

1. Define the portfolio, source-mapping, candidate, canonical, and report
   schemas with fixture files.
2. Implement Yahoo and Stooq acquisition with database upserts and watermarks.
3. Implement verification, discrepancy reporting, whole-source selection, and
   explicit promotion.
4. Extend `analysis.db` for market datasets and idempotent imports.
5. Implement the storage-independent return engine.
6. Implement validation and report commands.
7. Add operating documentation and link this document from `docs/index.md`.

All added Markdown will be wrapped to 80 columns. Lint findings will be fixed
rather than suppressed with new `//nolint` directives.

## Test and Acceptance Plan

### Data acquisition

Test from local fixtures rather than live services:

- valid Yahoo and Stooq responses;
- provider schema changes;
- rate limits and non-success responses;
- malformed dates and numbers;
- duplicate or unordered observations;
- dividend and split events;
- bootstrap, incremental, reconciliation, and repair work;
- retry visibility and incremental coverage watermarks.

Live-provider tests remain optional manual smoke tests.

### Verification and promotion

Cover:

- agreement within 25 basis points;
- a blocking numerical discrepancy;
- a structural mismatch;
- explicit promotion approval with a recorded reason;
- rejection of mixed-source datasets;
- whole-comparison fallback to an eligible canonical source;
- rejection of Stooq as a total-return fallback;
- idempotent promotion by dataset ID.

### Return mathematics

Use hand-calculated fixtures for:

- one security;
- a two-security equal-weight portfolio;
- explicit unequal weights;
- weights drifting over multiple periods;
- a dividend-adjusted value differing from raw close;
- weekend start and end dates;
- total return, CAGR, annual return, and excess return;
- rejection of missing observations;
- proof that constituent returns are weighted rather than summed.

### Storage and reports

Verify:

- atomic and idempotent imports;
- preservation of older datasets;
- report reproduction from matching config and dataset ID;
- deterministic Markdown and JSON golden files;
- no network access during return calculation;
- failure without partial report replacement.

V1 is accepted when a fixture portfolio can be compared with an ETF benchmark,
the result matches hand calculations, its inputs are fully traceable, and the
same imported dataset reproduces byte-equivalent output.

## Explicit Exclusions and Defaults

V1 excludes:

- real trades, deposits, withdrawals, taxes, and fees;
- rebalancing;
- non-USD listings and FX conversion;
- direct price-index benchmarks such as `^GSPC`;
- historical index reconstruction;
- scheduled services and web UI;
- volatility, Sharpe ratio, drawdown, and tracking error;
- strategy selection and backtesting;
- VIX, intraday, and option data;
- automatic broker-format detection;
- silent provider mixing or missing-data repair.

EDGAR dividend facts remain an explanatory and reconciliation source. They are
not used to construct market total returns. The existing
`docs/06-dividends/dividend-register.md` already notes that 10-Q and 10-K facts
are observations rather than complete payment events.

Future paid feeds can implement the candidate-data contract without changing
portfolio definitions, canonical imports, calculation logic, or reports.

[yahoo-history]: https://finance.yahoo.com/quote/AAPL/history/
[yahoo-download]: https://in.help.yahoo.com/kb/finance/download-historical-data-yahoo-finance-sln2311.html
