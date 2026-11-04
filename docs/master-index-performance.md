# Master Index Discovery Performance

## Status

This is a future review note. It does not authorize a new service, scheduler,
database, or pipeline framework.

The current development `master.tsv` is approximately 500 MB and contains
about 5.8 million rows. A complete scan is therefore visible during startup or
backfill even when most source filings are already cached.

## Current Path

Discovery currently performs these operations sequentially:

1. Scan `master.tsv` one line at a time with `bufio.Scanner`.
2. Split and normalize every row.
3. Parse the filing date for every valid row.
4. Apply the financial constituent/form policy and global ownership policy.
5. Collect matching records in batches of 1,000.
6. Insert accessions and outbox messages into `polling.db` in one transaction.
7. Publish pending outbox messages to NATS, waiting for each acknowledgement.

The reported discovery duration therefore includes file parsing, filtering,
SQLite work, and NATS publication. Do not assume that line-by-line reading is
the dominant cost until these parts are measured separately.

Known avoidable work includes:

- allocating all fields with `strings.Split` for every row;
- parsing dates before knowing whether a row is relevant;
- repeatedly normalizing and linearly searching configured CIKs;
- rescanning unchanged historical quarters on every fresh reconciliation;
- publishing NATS messages synchronously one at a time.

## Measure First

Add measurements before changing the storage model:

- bytes and rows scanned per second;
- rows matching financial and ownership policy;
- time spent parsing and filtering;
- time spent in each SQLite batch transaction;
- time spent publishing each outbox batch;
- average and maximum batch latency;
- warm-filesystem and cold-filesystem timings.

A focused `IndexReader` benchmark should use a representative index slice.
An end-to-end discovery benchmark should use a temporary SQLite database and a
recording publisher so parsing, storage, and publication can be compared.

## Option 1: Cheap Reader Improvements

Keep the TSV and streaming design, but remove unnecessary work:

- prepare normalized CIK and form lookup maps once;
- locate delimiters without allocating every field;
- inspect CIK and form before parsing the filing date and remaining fields;
- parse only matching rows into a complete `EdgarIndex`;
- tune the scanner or use a buffered reader only after benchmarking;
- benchmark larger SQLite batches rather than assuming 1,000 is optimal.

These changes preserve the current files, event contract, databases, and
single-worker model. They should be considered before introducing another
index format.

## Option 2: Reconcile Quarterly Partitions Incrementally

This is likely the highest-value structural improvement.

Quarterly TSV files are already the natural partitions. Record the checksum of
each quarter successfully reconciled into the discovery store. On startup:

1. List the retained quarterly files.
2. Compare their checksums with the committed reconciliation state.
3. Scan only new or changed quarters.
4. Always reconsider the current quarter when its checksum changes.

This makes an unchanged startup nearly constant-time instead of rescanning all
history. A fresh rebuild still scans every retained quarter, but ordinary
daemon restarts do not.

The existing index import provenance may be reusable. Avoid creating a second
overlapping state model merely for startup reconciliation.

## Option 3: Bounded Parallel Parsing

For a fresh rebuild, independent quarterly files could be parsed concurrently.
Use a small, bounded worker count and retain one SQLite/outbox writer.

This can use multiple CPU cores without introducing multiple application
workers or violating SQLite's single-writer constraint. It will help only if
parsing is material; it will not improve a workload dominated by disk bandwidth,
SQLite writes, or synchronous NATS acknowledgements.

## Option 4: Batched NATS Publication

The outbox currently preserves the correct durability boundary, but publication
waits for acknowledgements sequentially. A bounded asynchronous JetStream
publish batch could improve throughput while keeping messages in the outbox
until acknowledged.

This has more failure and shutdown semantics than reader optimization. Consider
it only when measurements show publication is a major part of discovery time.

## Option 5: A Queryable Local Index

If repeated filtered scans remain important, import the SEC index into a small
SQLite index with indexes on accession, CIK, form type, and filing date. Startup
could then query only the required financial and ownership rows.

This adds another derived database and rebuild path. It is justified only when
incremental quarterly reconciliation and cheaper parsing are still inadequate.
Do not introduce a columnar engine, distributed query system, or separate index
service for this workload without stronger evidence.

## Recommended Order

1. Measure parsing, SQLite, and NATS time separately.
2. Precompute lookup maps and avoid parsing irrelevant rows fully.
3. Reconcile unchanged quarters incrementally by checksum.
4. Benchmark bounded parallel parsing for fresh rebuilds.
5. Batch NATS publication only if acknowledgements dominate.
6. Add a queryable index database only as a last local optimization.

## Correctness Constraints

Any optimization must produce the same durable discovery set:

- configured financial forms only for the selected constituent set;
- recognized ownership forms globally;
- one durable event per accession;
- unchanged event JSON and NATS subjects;
- unchanged outbox acknowledgement and retry behavior;
- deterministic recovery after interruption.

The optimization must remain inside the monolith. Horizontal scaling,
distributed index scans, generic orchestration, and scheduling remain out of
scope.
