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
- operate through repeatable CLI commands without a built-in scheduler;
- keep market-data acquisition outside the return engine.

This is historical portfolio analysis, not a real-account tracker or strategy
backtester. Reports must describe the result as hypothetical and warn about
survivorship bias when today's selected stocks are applied to earlier periods.

## Architecture

Use four independent layers:

```text
Yahoo downloader ─┐
                  ├─> market-data verification ─> canonical dataset
Stooq downloader ─┘                                 │
Manual CSV import ──────────────────────────────────┘
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
- retain the unmodified upstream response as immutable evidence;
- produce a normalized candidate file and provenance sidecar;
- validate HTTP status, response shape, dates, numeric fields, and duplicates;
- never write directly to `analysis.db`;
- never calculate portfolio returns.

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
7. Produce an immutable, checksummed canonical dataset.

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

The import will be idempotent by dataset checksum. Promoted datasets remain
immutable, so an older report can always be reproduced.

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
- retrieval time and source-response checksum;
- declared adjustment capability.

The promoted canonical observation contract contains only:

- dataset ID;
- security ID;
- date;
- currency;
- positive `total_return_value`.

`total_return_value` is an index-like value whose ratios represent total
return. Its absolute level has no meaning.

A sidecar records:

- schema version;
- selected source;
- requested and covered periods;
- all security IDs;
- raw-response checksums;
- verification result;
- tolerance used;
- approval reason when applicable;
- canonical CSV checksum.

A manual import must use this canonical format. Broker-specific conversion is
outside V1; the tool must not guess column meanings from arbitrary exports.

### Persistence

Extend `analysis.db`; do not create a feature-specific database.

Add normalized concepts for:

- securities and optional CIK links;
- immutable market datasets;
- dataset observations;
- import checksums and provenance.

Basket definitions remain user-authored JSON. Reports and canonical bundles
are files; normalized observations and import lineage are stored in SQLite.

Raw downloads belong below `data/cache/market/`. Generated reports belong
below `data/reports/returns/`.

### CLI workflow

The public workflow will be:

1. Fetch Yahoo and Stooq candidates.
2. Verify them and review the discrepancy report.
3. Promote one whole-comparison canonical dataset.
4. Import the promoted dataset.
5. Generate the requested comparison report.

Representative command responsibilities are:

- `yahoo-prices fetch`: download Yahoo evidence and candidate rows;
- `stooq-prices fetch`: download Stooq evidence and candidate rows;
- `marketdata verify`: compare candidates without changing canonical data;
- `marketdata promote`: approve and create one canonical dataset;
- `returns import`: import a canonical dataset idempotently;
- `returns validate`: validate portfolio definitions;
- `returns report`: calculate and write Markdown and JSON reports.

Every command will return a non-zero status for incomplete or invalid work.
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
- data source, dataset ID, and checksums;
- validation or approval warnings;
- methodology and limitations.

The JSON report contains the same metadata plus daily basket values and
returns. It is the machine-readable contract for later web and risk analytics.

Write timestamped immutable reports and refresh `latest.md` and `latest.json`
only after both formats complete successfully.

## Delivery Order

1. Define the portfolio, source-mapping, candidate, canonical, and report
   schemas with fixture files.
2. Implement the Yahoo and Stooq downloaders with immutable raw capture.
3. Implement verification, discrepancy reporting, whole-source selection, and
   explicit promotion.
4. Extend `analysis.db` for immutable market datasets and idempotent imports.
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
- immutable raw-response naming and checksums.

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
- idempotent promotion by checksum.

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
- preservation of older immutable datasets;
- report reproduction from matching config and dataset checksums;
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
