# Architecture

## Purpose

Chopstick is an idempotent EDGAR data pipeline. Raw SEC inputs are preserved
so every derived result can be deleted and rebuilt. The command-line program
is the control plane for the pipeline and will remain so when individual
stages later become services.

## Data boundaries

The pipeline has three data boundaries:

1. Raw inputs are immutable evidence and a local cache of SEC data.
2. `data/analysis.db` is the canonical application database for normalized,
   validated data used by the web application, analysis, and data-quality
   queries.
3. Reports and compatibility JSON files are disposable exports.

Raw inputs currently include downloaded SEC filing submissions under
`data/cache/filings/` and cached SEC index ZIP files. Extracted TSV indexes,
parsed filing files, validation runs, and SQLite databases are derived data.

The runtime must never discover filings by recursively scanning directories.
It may open a known raw filing path identified by the database and accession.
SQLite is the current system of record for normalized data. A future search
index or analytical store will be a projection of this database.

## Pipeline

The current flow is:

```text
raw index ZIPs -> extracted indexes -> filing catalog -> raw filings
    -> parsers -> analysis.db -> web and data-quality queries
```

The daily index provides historical discovery and reconciliation. The SEC
Atom feed is the low-latency discovery path. Both paths use durable accession
deduplication and can safely produce the same filing more than once.

The parser writes normalized filing data and dividend projections to
`analysis.db` transactionally. Dividend updates replace the contribution from
the current accession and do not rebuild a company by scanning parsed files.

The web application reads normalized views from `analysis.db`. Raw filing
content is opened only for a specific known filing, such as a document view.

## Idempotency and recovery

Each stage may be interrupted and rerun. A valid existing result is reused;
an absent, incomplete, stale, or corrupt result is rebuilt. Database writes
use stable identities such as CIK plus accession and are safe to repeat.

Raw cache entries are reusable only after path, identity, and content checks
pass. Existing raw files are skipped by the downloader.

The clean-start workflow removes all derived data, rebuilds extracted indexes
from cached raw ZIP files, downloads only missing filings, and recreates
`analysis.db` from the raw inputs.

## CLI control plane

The `edgar` executable owns the operational boundaries:

- `index` extracts and stitches index data;
- `filings` downloads and processes selected filings;
- `parse` processes already available raw filings;
- `taxonomy` and `coverage` run data-quality checks;
- `serve` starts the local web application.

The commands are intentionally usable as one local executable today. Future
service processes should preserve these stage contracts and database
identities rather than introduce separate feature-specific databases.

## Future event activity

Atom remains the low-latency trigger for ownership, insider-trade, dividend,
and selected 8-K activity. The first implementation can persist activity in
`analysis.db`. A later outbox or event publisher can push committed events to
other consumers without changing the parser or web data model.
