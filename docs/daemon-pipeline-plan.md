# Daemon-Owned Pipeline Plan

## Purpose

Make `chopd` the single owner of running application workflows while keeping
`chop` a small control and inspection CLI.

The goal is not to build a scheduler or a general workflow engine. Chop owns
SEC-specific ingestion and processing. Process supervision, scheduled
invocation, distributed scheduling, and generic storage infrastructure remain
outside the application.

## Decisions

- `chopd` remains one foreground monolith.
- NATS and JetStream remain an independent local service.
- The CLI uses `internal/transport` for application commands.
- NATS carries filing pipeline events, not CLI control requests.
- Atom and index ingestion are daemon-owned sources for the same pipeline.
- Atom ingestion has exactly one active owner.
- `chop index import` is the first finite background operation to move into
  `chopd`.
- After that path works, clean up the remaining command model before wiring
  more commands into the daemon.
- Downloaders and processors are the only likely future scale-out roles.
- SQLite and its current single-writer limitations are accepted for now.
- Existing databases remain separate.
- Detailed workflow logs belong to `chopd`.
- The CLI renders command results and structured status, not daemon logs.
- Cron and other scheduling mechanisms are out of scope.

## Target Runtime Model

```text
chop
  -> local command transport
  -> chopd
       -> continuous Atom discovery
       -> requested index discovery
       -> filing work store and outbox
       -> NATS JetStream
            -> selection consumer
            -> download consumer
            -> processing consumer
            -> ownership and news projections
```

The daemon owns continuous roles and finite application operations. Closing a
CLI terminal must not stop work already accepted by the daemon.

The outbox remains the transaction boundary between the SQLite work store and
JetStream. JetStream remains responsible for durable delivery, redelivery,
and distribution among consumers.

The two source readers remain concrete. They share the filing event, work
store, and outbox boundary, but they do not need a generic ingestion framework.

## Command Classification

### Daemon control commands

These commands inspect or affect the running application through
`internal/transport`:

- `chop status`
- `chop failures list`
- `chop failures retry`
- `chop index import`

The Atom reader is already daemon-owned and continuous. `chop index import`
becomes its finite, operator-triggered counterpart. It reads historical or
quarterly index records and submits the same `pipeline.Event` contract through
the same deduplication and outbox boundary.

The first implementation moves only this background operation. After it is
working, clean up command names, responsibilities, and use-case boundaries
before connecting `filings` or other commands to the daemon.

### Local inspection commands

Commands that inspect explicit local files or produce operator reports may
remain in the CLI process:

- artifact inspection and serving;
- taxonomy inspection;
- coverage reports;
- explicitly local parsing tools where no daemon state is mutated.

### Offline maintenance commands

Destructive maintenance remains deliberately offline until its daemon
semantics are designed:

- `chop backfill`;
- `chop rebuild edgar`;
- finite rebuild or recovery commands that require deleting databases or
  resetting JetStream.

Do not move these commands into a live daemon merely for consistency. Their
exclusive lock and shutdown requirements are valuable safety boundaries.

`chop index download` remains local during the first implementation. Its name
and relationship to import, refresh, and stitching must be cleaned up before
it is wired into `chopd`.

## Index Source Semantics

Preserve the current import selectors during the first move:

- one explicit quarterly TSV file;
- the latest local quarterly TSV file;
- one year;
- a year range;
- dry-run validation.

`--latest` continues to mean the latest matching local file. Do not silently
turn it into a network fetch.

The daemon should verify that `master.tsv` represents the complete local set
of quarterly TSV files. If it is missing or older than its inputs, restitch it
from the full local quarterly cache. Import selection and master stitching are
separate concerns: importing one quarter must not produce a master containing
only that quarter.

"Full local" does not mean the complete SEC history. A development cache may
deliberately contain only three or four years. Year selectors limit source
history, while the constituent set and form types remain downstream selection
policy.

Fetching or refreshing the latest SEC index should remain explicit. During
the later command cleanup, decide whether the public operation should be named
`index refresh`, `index sync`, or retain `index download`. That command may
perform a clear sequence of fetch, stitch, and import, but it must run only
when requested. It is not a scheduler.

## Minimal Progress Model

Do not build durable jobs, priorities, dependencies, cancellation, or a
generic scheduler in the first implementation.

Expose two kinds of state instead.

### Continuous role state

`chop status` should show:

- daemon start time;
- Atom role state and last activity;
- active index import state and last activity;
- selection, download, and processing consumer state;
- JetStream pending, in-flight, and redelivered counts;
- durable outbox and failure counts;
- the last role error where available.

`chop index status` should separately show the concrete index inventory:

- quarterly cache and master paths;
- available quarters and gaps;
- whether the master is missing, stale, or current;
- quarters committed by index imports;
- the last committed import and active import, when present.

The daemon calculates file inventory and keeps one small index-specific state
file beside the master. This is provenance for operator inspection, not a
durable job framework.

### Active finite operation

The daemon may expose one small in-memory snapshot for a manually submitted
operation:

- kind;
- state;
- start time;
- current phase;
- selected file and examined event counts;
- newly queued count;
- duplicate count;
- last message or error.

Only one index import should run at a time. A second request receives a clear
busy response. This is a concurrency guard around a concrete operation, not a
job scheduler or a reusable execution framework.

It is acceptable for the active-operation snapshot to disappear when the
daemon restarts. Events already committed to the outbox or JetStream remain
durable and visible through global pipeline counts.

Do not add operation history, operation result tables, or exact per-batch
terminal counts until a concrete operational need justifies them.

## CLI Behavior

The initial command behavior should be simple:

```text
$ chop index import --latest
Index import accepted by chopd.
Use `chop status` to inspect pipeline progress.
```

The transport request must return after the daemon accepts the operation. It
must not keep one TCP request open for the lifetime of reading, deduplicating,
and publishing the index records.

The CLI should return a non-zero exit code when:

- `chopd` is unavailable;
- the request is invalid;
- a conflicting operation is already active;
- the daemon rejects the request before accepting it.

Once accepted, later processing failures belong to daemon status and failure
commands. They are not the exit status of the short-lived submission command.

A future wait mode is explicitly deferred. If added later, it should poll
structured status rather than stream raw logs or change the TCP protocol into
a streaming command bus.

## Implementation Sequence

### 1. Wire index import as the first background operation

- Add one explicit index-import request and response to
  `internal/transport`.
- Keep the TCP protocol as a small request and response switch.
- Do not add a generic command envelope, handler registry, middleware stack,
  streaming protocol, or command bus abstraction.
- Map the existing typed Kong options to the transport request in `cmd/chop`.
- Add a concrete index-import handler in `chopstick`.
- Validate the request before accepting it.
- Return an accepted or busy response promptly.
- Run accepted work beneath the daemon's root context.
- Record one active index-import snapshot in `chopstick`.
- Ensure import and publication logs use the daemon logger.
- Remove the application lock requirement from the daemon-owned import path.
- Remove local import execution from the CLI path.

The CLI continues to own parsing and response formatting. The transport owns
only serialization and the local connection. `chopstick` owns background
execution and lifecycle.

### 2. Keep Atom and index readers concrete

- Reuse `internal/chopstick/ingest` for selecting and reading quarterly TSV
  files.
- Convert index records into the existing `pipeline.Event` contract.
- Save events through `pipeline.Store` so deduplication and outbox publication
  stay atomic.
- Publish pending outbox entries through the existing event bus.
- Let the continuously running selection, download, and processing consumers
  perform the remaining work.
- Preserve the existing Atom reader as its own continuous implementation.
- Share only the event, work store, outbox, and publisher boundaries.
- Keep event JSON, subjects, durable names, and database locations unchanged.

The index import ends when its records have been committed and pending outbox
messages have been published. Database completion is observed through consumer
backlog and failure status in this first version.

### 3. Verify and stitch the local full index

- Inspect the complete quarterly TSV cache before import.
- Detect a missing or stale `master.tsv` using local file state.
- Restitch the complete local master when required.
- Never build the master from only the selected import subset.
- Keep `--latest` limited to selecting the latest local quarterly file.
- Do not perform a network fetch unless the operator requested a future
  refresh or download command explicitly.

This is source preparation, not scheduling. No timer, recurrence, or automatic
command invocation belongs in this step.

### 4. Extend status without inventing scheduling

- Add role health and the active-operation snapshot to
  `transport.Status`.
- Keep the existing JetStream consumer metrics as the main progress signal.
- Render a compact role, queue, and failure summary in `chop status`.
- Show timestamps and counters, not copied daemon log lines.
- Clear the active import when it finishes or fails.

The distinction must remain visible:

- submission complete means the selected work was durably queued;
- pipeline idle means consumers currently report no pending work;
- unresolved failures mean some work did not reach its terminal output.

Do not claim exact per-import database completion from global queue counts.

### 5. Clean commands before wiring more operations

After the first background import is working, stop and clean the remaining
command model before adding more transport handlers.

- Clarify the difference between index download, refresh, stitch, and import.
- Decide whether one explicit index command should perform fetch, stitch, and
  import as a sequence.
- Keep dry-run and local inspection behavior clearly named.
- Remove duplicate validation and path construction from CLI adapters.
- Keep application use cases typed and independent of Kong.
- Keep the TCP request types concrete rather than generalizing from one
  successful background operation.
- Review `filings`, `backfill`, and rebuild semantics separately.

Only after this cleanup should another command be wired into `chopd`.

Before routing `filings --reprocess` through discovery deduplication, define
its meaning explicitly. Normal accession deduplication must not silently ignore
a requested reprocess, and ordinary discovery must not create duplicate work.

## Horizontal Scale Later

Horizontal deployment is not part of this implementation, but current changes
must not block it.

Preserve these properties:

- consumers are idempotent under redelivery;
- acknowledgement happens only after durable output succeeds;
- processors do not depend on in-process state from the ingestor;
- accession remains the primary deduplication identity;
- events do not rely on machine-local paths as permanent contracts;
- downloaded and parsed outputs have deterministic identities;
- projection databases remain independently rebuildable where practical.

When hardware and workload justify it, the same codebase may allow one daemon
instance to own ingestion and other instances to run only downloader or
processor roles. Multiple Atom ingestors are not a goal.

SQLite remains acceptable until its measured write limits become a real
constraint. Do not introduce a distributed database or generic storage layer
in anticipation of that point.

## Explicit Non-Goals

- cron integration;
- an internal scheduler;
- workflow DAGs or dependencies;
- durable job history;
- job priorities;
- distributed locks or leader election;
- raw log streaming to the CLI;
- NATS request/reply for CLI commands;
- a generic TCP command framework;
- multiple Atom ingestion instances;
- horizontal deployment configuration;
- database replacement or migration;
- archive-service extraction;
- a generic plugin system for future databases.

## Tests

Add tests that prove the ownership boundary and observable behavior:

- `chop index import` sends a typed transport request and does not acquire the
  application lock or mutate pipeline state from the CLI process.
- An accepted request returns promptly.
- The daemon continues the import after the client disconnects.
- A second conflicting request receives a busy response.
- Daemon cancellation stops the active import cleanly.
- Existing file, latest, year, range, and dry-run selectors remain compatible.
- `--latest` selects local input and does not fetch from the network.
- A missing or stale master is stitched from all local quarterly inputs.
- Importing one selected quarter does not truncate the full master.
- Discovery still deduplicates accessions through `pipeline.Store`.
- Outbox rows remain pending when publication fails.
- Pending rows publish successfully after recovery.
- Existing event JSON and NATS subjects remain unchanged.
- `chop status` renders role, consumer, outbox, and failure state.
- `chop index status` renders source inventory and committed import coverage.
- Processing errors remain visible through `chop failures list`.
- CLI output does not contain daemon workflow logs.

Run:

```bash
go test ./...
golangci-lint run ./cmd/... ./internal/...
make check-generated
git diff --check
```

## Completion Criteria

The first implementation is complete when:

- `chop index import` no longer acquires the daemon lock or writes pipeline
  state in the CLI process;
- `chopd` accepts and owns the index import;
- work enters the existing deduplicated outbox and JetStream pipeline;
- the full local master is present and current with its quarterly inputs;
- `chop index status` identifies available and imported periods without
  requiring direct disk inspection;
- daemon logs contain the import and publication activity;
- the CLI returns promptly with an accepted or rejected result;
- `chop status` shows enough role and queue state to understand whether the
  pipeline is working, waiting, or failing;
- the remaining command model is reviewed before another command is wired;
- no scheduler, durable job framework, or new service has been introduced.
