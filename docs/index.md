# EDGAR application

The repository builds two EDGAR executables with separate responsibilities:

- `edgard` is the long-running application. It runs every continuous role and
  the HTTP application in one foreground process.
- `edgar` is the operator and offline-data command-line interface.

NATS remains a separate local service. It carries durable filing events and
live control requests between the CLI and the running application.

## Development

Start the existing project-local NATS service, then run `edgard` in the
foreground:

```bash
bash scripts/nats-dev.sh start
go run ./cmd/edgard
```

Use another terminal for application commands:

```bash
go run ./cmd/edgar doctor
go run ./cmd/edgar status
go run ./cmd/edgar failures list
go run ./cmd/edgar failures retry --stage download
```

Historical backfill and rebuild are offline operations. They acquire the same
data-root lock as `edgard` and refuse to run while the application is active:

```bash
go run ./cmd/edgar backfill
go run ./cmd/edgar rebuild edgar --confirm
```

The rebuild preserves downloaded filings, quarterly indexes, index ZIP files,
constituent sets, ownership data, and market data. It removes rebuildable
filing-analysis state, resets the EDGAR JetStream stream, and runs a finite
backfill. NATS must be running, but `edgard` must be stopped.

The former standalone polling commands are application operations now. The
continuous Atom poller belongs to `edgard`; local quarterly-index recovery is
available through the control CLI:

```bash
go run ./cmd/edgar index import --latest --dry-run
go run ./cmd/edgar index import --year 2025
```

A mutating import also acquires the data-root lock and therefore requires
`edgard` to be stopped.

`Ctrl-C` stops `edgard` cleanly. Development does not require systemd or a
daemon mode. A future systemd unit will run the same foreground executable.

## Boundaries

The control CLI does not mutate a running application's SQLite files. Live
status and failure operations use NATS request/reply and are handled by
`edgard`. `doctor` performs direct dependency checks so it remains useful when
NATS or `edgard` is unavailable.

The application remains a monolith. Its Atom, selection, download, processing,
ownership, news, and web roles share one process while retaining their NATS
handoffs and separate databases.

Make targets are limited to source-code lifecycle operations. Application
startup, administration, and data workflows belong to the executables rather
than Make.
