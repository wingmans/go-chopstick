# Chopstick

The repository builds two Chopstick executables for SEC EDGAR data:

- `chopd` is the long-running application. It runs every continuous role and
  the HTTP application in one foreground process.
- `chop` is the operator and offline-data command-line interface.

`cmd/chop` contains the Kong command hierarchy and adapts parsed options to
typed use cases. `internal/chopstick/operator` contains command workflows and
operator rendering, but no command-line parser. See the
[architecture guide](architecture.md) for the package boundaries.
The current index-first daemon work is described in the
[daemon pipeline plan](daemon-pipeline-plan.md).
Future options for accelerating large index scans are recorded in the
[master index performance note](master-index-performance.md).
All external HTTP requests use the mandatory local
[outbound gateway](outbound-gateway.md).
Package ownership and dependency direction are described in the
[architecture guide](architecture.md). The standalone ECB tool is documented
in the [ECB ingest guide](ecb-ingest.md).

NATS remains a separate local service and carries durable filing events. Live
control requests use the local TCP transport between `chop` and `chopd`.
NATS disconnects and reconnects are logged. Outbox publication uses a bounded
acknowledgement wait; a timeout fails the current stage and leaves unpublished
rows pending for the next run.

## Development

Start the existing project-local NATS service, then run `chopd` in the
foreground:

```bash
make nats-start
make run
```

Use `make nats-status`, `make nats-stop`, and `make nats-reset` to inspect or
manage the project-local development instance. Reset requires NATS to be
stopped.

`make run` starts and verifies the outbound gateway, then runs
`go run ./cmd/chopd`. It does not start or supervise NATS and does not perform
application operations.

The default instance runs the complete local monolith:

- Atom ingestion and durable publication.
- Filing selection.
- Filing download.
- Filing parsing and analysis-database updates.
- Ownership processing.
- News projection and the HTTP application.
- Missing-index bootstrap and startup reconciliation from the local master
  index.

The default automatic financial scope is `10-K`, `10-K/A`, `10-Q`,
`10-Q/A`, `8-K`, and `8-K/A` for the configured constituent set. Ownership
processing is also issuer-scoped to the configured set. It automatically
handles Form 4/4A and structured `SCHEDULE 13D/G` submissions. Forms 3/5,
13F, and legacy HTML `SC 13D/G` submissions are deferred. `S-1` and `S-1/A`
remain on-demand inputs rather than automatic pipeline work.

Use another terminal for application commands:

```bash
go run ./cmd/chop doctor
go run ./cmd/chop status
go run ./cmd/chop failures list
go run ./cmd/chop failures retry --stage download
go run ./cmd/chop failures retry --stage ownership
```

Historical backfill and rebuild are offline operations. They acquire the same
data-root lock as `chopd` and refuse to run while the application is active:

```bash
go run ./cmd/chop backfill
go run ./cmd/chop rebuild edgar --confirm
```

Backfill reports each sequential stage, elapsed time, quarterly-index progress,
master-index discovery counts, and the existing consumer backlog counters. This
is process-local progress reporting, not a persistent job or scheduler model.

The rebuild preserves downloaded filings, quarterly indexes, index ZIP files,
constituent sets, ownership data, and market data. It removes rebuildable
filing-analysis state, resets the EDGAR JetStream stream, and runs a finite
backfill. NATS must be running, but `chopd` must be stopped.

Atom and quarterly-index ingestion are application operations. The continuous
Atom poller belongs to `chopd`. A quarterly-index import is submitted to the
running daemon through the control CLI:

```bash
go run ./cmd/chop index import --latest --dry-run
go run ./cmd/chop index import --year 2025
go run ./cmd/chop index status
```

The command returns after `chopd` accepts the import. Import, deduplication,
outbox publication, and workflow logs remain in the daemon. Use `chop status`
to inspect the active import and pipeline backlog. `--latest` selects the
latest local quarterly file; it does not fetch from SEC.

`chop index status` is the operator view of index storage. It reports the
quarterly cache and master paths, locally available quarters, gaps, master
freshness, durably imported quarters, the last committed import, and an active
import. Inspecting files directly is a troubleshooting technique, not the
normal management interface.

The full local master contains every quarterly TSV currently present in the
local cache. It does not require the complete SEC history. A development
instance can deliberately retain only three or four years:

```bash
go run ./cmd/chop index refresh --from-year 2023
go run ./cmd/chop index import --from-year 2023 --to-year 2026
```

Year selectors bound source history. `EDGAR_SET` and `EDGAR_FORM_TYPES` remain
downstream selection policy, so changing a constituent set does not require
downloading the source indexes again.

`Ctrl-C` stops `chopd` cleanly. Development does not require systemd or a
daemon mode. A future systemd unit will run the same foreground executable.

## Boundaries

The control CLI does not mutate a running application's SQLite files. Live
status and failure operations use the local TCP connection in
`internal/platform/transport` and are handled by `chopd`. The default command
address is `127.0.0.1:8084` and can be changed with
`CHOP_COMMAND_ADDRESS` or `--command-address`.

NATS remains separate and carries pipeline events. It is not the CLI command
transport.

`doctor` performs direct dependency checks so it remains useful when NATS or
`chopd` is unavailable. Backfill and rebuild remain offline operations and
require the application lock instead of contacting `chopd`. Index import uses
the local command transport and daemon-owned storage paths. Index status uses
the same transport and does not inspect daemon storage from the CLI process.

The application remains a monolith. Its Atom, selection, download, processing,
ownership, news, and web roles share one process while retaining their NATS
handoffs and separate databases.

Make targets cover source-code lifecycle operations and the local NATS and
outbound-gateway infrastructure. Application administration and data
workflows belong to the executables rather than Make.
