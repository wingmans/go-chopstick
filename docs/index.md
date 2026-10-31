# Chop

The repository builds two Chop executables for SEC EDGAR data:

- `chopd` is the long-running application. It runs every continuous role and
  the HTTP application in one foreground process.
- `chop` is the operator and offline-data command-line interface.

NATS remains a separate local service. It carries durable filing events and
live control requests between the CLI and the running application.

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

`make run` is deliberately a thin development shortcut for
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

Use another terminal for application commands:

```bash
go run ./cmd/chop doctor
go run ./cmd/chop status
go run ./cmd/chop failures list
go run ./cmd/chop failures retry --stage download
```

Historical backfill and rebuild are offline operations. They acquire the same
data-root lock as `chopd` and refuse to run while the application is active:

```bash
go run ./cmd/chop backfill
go run ./cmd/chop rebuild edgar --confirm
```

The rebuild preserves downloaded filings, quarterly indexes, index ZIP files,
constituent sets, ownership data, and market data. It removes rebuildable
filing-analysis state, resets the EDGAR JetStream stream, and runs a finite
backfill. NATS must be running, but `chopd` must be stopped.

The former standalone polling commands are application operations now. The
continuous Atom poller belongs to `chopd`; local quarterly-index recovery is
available through the control CLI:

```bash
go run ./cmd/chop index import --latest --dry-run
go run ./cmd/chop index import --year 2025
```

A mutating import also acquires the data-root lock and therefore requires
`chopd` to be stopped.

`Ctrl-C` stops `chopd` cleanly. Development does not require systemd or a
daemon mode. A future systemd unit will run the same foreground executable.

## Boundaries

The control CLI does not mutate a running application's SQLite files. Live
status and failure operations use NATS request/reply and are handled by
`chopd`. `doctor` performs direct dependency checks so it remains useful when
NATS or `chopd` is unavailable.

The application remains a monolith. Its Atom, selection, download, processing,
ownership, news, and web roles share one process while retaining their NATS
handoffs and separate databases.

Make targets are limited to source-code lifecycle operations. Application
startup, administration, and data workflows belong to the executables rather
than Make.
