# Master Index Discovery Performance

## Status

This is a future review note. It does not authorize a new service, scheduler,
database, or pipeline framework.

The current development `master.tsv` is approximately 500 MB and contains
about 5.8 million rows. A complete scan is therefore visible during startup or
backfill even when most source filings are already cached.

No optimization is currently planned. This note records the measurements and
options discussed on 4 November 2026 so the decision can be revisited later.

## Observed Backfill

One backfill discovery run reported:

```text
examined=5804940 matched=2492951 new=0 duration_ms=488972
```

Discovery therefore spent about eight minutes and nine seconds finding no new
accessions. Stitching the quarterly TSV files, including refreshing the latest
period, takes less than 20 seconds.

A warm-cache `wc -l` over the approximately 500 MB stitched file completed in
about 0.6 seconds. That is not a parser benchmark, but it demonstrates that raw
file reading is not sufficient to explain the discovery duration.

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

`bufio.Scanner` is buffered. Although the API returns one line at a time, it
does not issue one disk read per line. In the observed `new=0` run, the more
important work was approximately 2.49 million attempted accession inserts and
an outbox check after every batch of 1,000 matching rows.

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

These are the same general techniques used by high-throughput parsing
benchmarks: read large buffers, avoid allocations, parse only required fields,
and split work into newline-aligned chunks. Those benchmarks normally perform
only an in-memory aggregation. They are not directly comparable to discovery,
which currently performs SQLite and outbox work for matching rows.

## Option 2: Compare Against an In-Memory Set

For a finite backfill, load `processed_accessions` once into a Go
`map[string]struct{}`. Stream the TSV and perform membership checks in memory.
Add newly encountered accessions to the map immediately, then send only new
events through the existing store and outbox.

Backfill holds the application lock, so the accession snapshot cannot become
stale through a concurrent daemon writer. At the current approximately 1.36
million known accessions, a map would probably require roughly 100 to 200 MB.

This is the simplest non-SQL comparison and should make an unchanged backfill
take seconds or tens of seconds rather than minutes. A sorted string slice with
binary search could reduce memory at the cost of more comparisons. Sorting both
inputs and merging them would be memory-efficient but adds temporary-file and
sorting machinery.

## Option 3: Use a Temporary SQLite Candidate Set

Another bounded-memory option is a SQLite `TEMP TABLE` containing eligible TSV
entries. Load it with a prepared statement in one transaction, deduplicate by
accession, and use an anti-join against `processed_accessions`.

The table belongs to SQLite's temporary database and disappears with its
connection. It must remain on one pinned connection. This avoids a persistent
index catalog, but still performs millions of temporary inserts. Benchmark it
against the in-memory set before choosing it.

## Option 4: Reconcile Quarterly Partitions Incrementally

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

## Option 5: Bounded Parallel Parsing

For a fresh rebuild, independent quarterly files could be parsed concurrently.
Use a small, bounded worker count and retain one SQLite/outbox writer.

This can use multiple CPU cores without introducing multiple application
workers or violating SQLite's single-writer constraint. It will help only if
parsing is material; it will not improve a workload dominated by disk bandwidth,
SQLite writes, or synchronous NATS acknowledgements.

## Option 6: Batched NATS Publication

The outbox currently preserves the correct durability boundary, but publication
waits for acknowledgements sequentially. A bounded asynchronous JetStream
publish batch could improve throughput while keeping messages in the outbox
until acknowledged.

This has more failure and shutdown semantics than reader optimization. Consider
it only when measurements show publication is a major part of discovery time.

## Option 7: A Queryable Local Index

If repeated filtered scans remain important, import the SEC index into a small
SQLite index with indexes on accession, CIK, form type, and filing date. Startup
could then query only the required financial and ownership rows.

This adds another derived database and rebuild path. It is justified only when
incremental quarterly reconciliation and cheaper parsing are still inadequate.
Do not introduce a columnar engine, distributed query system, or separate index
service for this workload without stronger evidence.

## Recommended Order

There is no commitment to change discovery at this time. If it becomes a
priority, use this order:

1. Measure parsing, SQLite, and outbox time separately.
2. Compare the in-memory accession set with a temporary SQLite anti-join.
3. Avoid fully parsing irrelevant rows.
4. Reconcile unchanged quarters incrementally only if restarts still matter.
5. Benchmark bounded parallel parsing only if parsing remains material.
6. Batch NATS publication only if acknowledgements dominate new discovery.
7. Add a persistent queryable index only as a last local optimization.

Whichever comparison is selected, it remains an implementation detail of index
discovery. New accessions must continue through the normal Chopstick store,
outbox, NATS, and worker foundation. Do not create a separate backfill
pipeline.

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
