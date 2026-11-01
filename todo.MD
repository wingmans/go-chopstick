Complete the Chop CLI/daemon split and isolate command transport.

Repository: chopstick

The split is already mostly implemented. Inspect the current architecture before
changing it and evolve the existing implementation rather than rebuilding it.

Objectives

1. Keep two public executables:

   - `chop`: operator CLI
   - `chopd`: long-running application daemon

2. Introduce `internal/transport` as the exclusive boundary for communication
   between `chop` and `chopd`.

3. Wire live CLI commands through the transport package.

Architecture

`cmd/chop`
    -> `internal/transport.Client`
    -> NATS request/reply
    -> `internal/transport.Server`
    -> typed application operations
    -> domain and storage packages

Transport responsibilities

Move the current command transport functionality, currently under
`internal/control` or equivalent code, into `internal/transport`.

`internal/transport` should own:

- NATS command subjects.
- Request and response DTOs.
- Encoding and decoding.
- Client request timeouts.
- Transport-level errors.
- NATS connection setup needed by command transport.
- Server-side command registration and dispatch.
- Mapping unavailable or timed-out requests to clear errors.

It must not own:

- CLI flag parsing.
- Terminal formatting.
- Business logic.
- Database queries implementing commands.
- Application lifecycle decisions.

CLI responsibilities

`cmd/chop` should:

- Parse arguments and flags.
- Construct typed transport requests.
- Call `internal/transport.Client`.
- Render typed responses for the terminal.
- Map transport failures to useful exit errors.

The CLI must not:

- Import daemon command handlers.
- Re-enter another CLI parser.
- Construct fake `argv` values.
- Read or mutate daemon-owned databases for live operations.
- Contain NATS subjects or serialization details.

At minimum, route these live commands through transport:

- `chop status`
- `chop failures list`
- `chop failures retry`

Structure the transport API so future live commands can be added without
putting NATS details back into the CLI.

Daemon responsibilities

`cmd/chopd` should:

- Start the monolithic Chop application in the foreground.
- Create the transport server.
- Register typed handlers.
- Delegate handlers to application services.
- Shut down cleanly when its context is cancelled.

Do not daemonize inside Go. Development must continue to work with:

    make nats-start
    make run

Systemd support can be added later without changing application architecture.

Offline commands

Commands that require `chopd` to be stopped may continue to invoke typed
application packages directly. Examples include:

- backfill
- destructive rebuild
- index import that requires exclusive data access

Keep the existing application lock for these operations. Do not force offline
operations through a running daemon merely for architectural uniformity.

`doctor` may retain direct dependency checks because it must remain useful when
NATS or `chopd` is unavailable.

Naming

- Public CLI name: `chop`
- Public daemon name: `chopd`
- Source directories: `cmd/chop` and `cmd/chopd`
- Transport package: `internal/transport`
- Control subjects should use the existing `chop.control.*` namespace.
- SEC EDGAR remains the domain name.

Do not rename or reorganize `internal/edgar`. Preserve EDGAR environment
variables, filing event subjects, storage paths, and domain terminology where
they refer to SEC EDGAR rather than the application executable.

Operational constraints

- NATS remains a separate local service.
- Do not embed NATS in `chopd`.
- Do not introduce a Unix socket.
- Keep the application as one monolithic service.
- Preserve the existing separate databases.
- Do not split ingest, processing, web, ownership, or news into separate
  services.

Cleanup

- Remove `internal/control` after all users have migrated.
- Remove duplicate transport clients, subjects, DTOs, and NATS dispatch code.
- Ensure no live CLI handler bypasses `internal/transport`.
- Keep transport types focused and avoid a generic untyped command envelope.
- Prefer explicit typed methods such as `Status`, `ListFailures`, and
  `RetryFailures`.

Testing

Add or update tests covering:

- Transport request and response encoding.
- Client timeout and daemon-unavailable behavior.
- Server dispatch to typed handlers.
- Invalid requests.
- Handler errors returned to the CLI.
- CLI parsing independently from transport.
- Clean daemon shutdown.
- Offline command lock behavior.

Run:

    go test ./...
    golangci-lint run ./cmd/chop ./cmd/chopd ./internal/transport \
        ./internal/edgarapp
    git diff --check

Do not add `//nolint` suppressions.

Documentation

Update current documentation and examples to use `chop`, `chopd`, and
`internal/transport`. Keep Markdown source lines at 80 columns or fewer, except
where fixed-format tables or URLs make wrapping impractical.

Deliver the implementation as a patch. Summarize:

- The final package boundaries.
- Which commands use transport.
- Which commands deliberately remain offline.
- Tests and validation performed.
- Any remaining architectural gaps.

  
4:01 PM


