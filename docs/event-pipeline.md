# Event pipeline

The event pipeline keeps the current modular monolith while making its role
boundaries executable. NATS is a deployment prerequisite; the application does
not embed a server.

```text
SEC Atom -> polling.db + outbox -> JetStream -> download -> process
                                      |
                                      +----------> EDGAR news
```

Atom is intentionally a lossy, low-latency source. The existing index path is
the end-of-day reconciliation source. Both sources write accessions and
discovery outbox rows in one SQLite transaction. Publication happens after the
commit and an outbox row is marked complete only after JetStream acknowledges
it.

The POC pipeline supports filings from 2016 onward. Index download and
discovery enforce this boundary so a clean run cannot accidentally enqueue
the less stable legacy formats. The raw submission path from the SEC master
index is authoritative. HTML index paths are not synthesized or downloaded.

JetStream delivery is at least once. Download and processing therefore retain
their existing idempotent file and database checks. Consumers are serial for
now. Subject suffixes route broad categories such as form type. Constituent
set and filing-year selection happen during discovery, before work enters the
stream. Download and process consume all work assigned to their durable
consumers.

The news service owns `feed.db`. It orders items by JetStream stream sequence,
not by a locally generated timestamp. If `feed.db` is deleted, the next start
recreates it and resets only the news durable consumer so the retained stream
is replayed from the beginning.

## Development

Copy the development configuration and provide your SEC contact address:

```sh
make env
```

Make reads `.env` and exports its documented settings to the application. CLI
flags still override application defaults. The normal development scope uses
`EDGAR_SET` and `EDGAR_FROM_YEAR`; the upper boundary is the current available
data.

Install the pinned NATS server binary into the git-ignored `.tools/bin`
directory. This does not install a system-wide binary or service.

```sh
make deps
```

Check configuration and infrastructure without changing them:

```sh
make doctor
make doctor-verbose
```

Run a finite historical pipeline with the scope from `.env`:

```sh
make backfill
```

The command retrieves and stitches indexes, discovers the selected filings,
and drains download and processing work. Override the local defaults without
editing `.env` when needed:

```sh
make backfill SET=us-gaap FROM_YEAR=2024
```

Run all continuous roles in the foreground:

```sh
make dev
```

`make dev` creates missing schemas, streams, and consumers through normal
application startup. It preserves existing state and does not perform a
historical backfill. Press `Ctrl-C` to stop the application; the project-local
NATS server remains running.

Inspect current state and unresolved failures from another terminal:

```sh
make status
make errors
```

Workers retry failed work five times before terminating that delivery and
leaving a durable failure record in `polling.db`. Requeue failures explicitly:

```sh
make retry-download
make retry-process
```

Normal logs include a progress summary every five seconds. `LOGLEVEL=debug`
also shows individual work decisions. `make dev` and `make backfill` append
their output to `data/logs/edgar.log`. Follow all output or a filtered view:

```sh
make logs
make logs-errors
make logs-download
make logs-process
```

The explicit stages remain available for debugging. `batch` is a deprecated
alias for `discover`.

```sh
make index
make discover
go run ./cmd/edgar download --drain
go run ./cmd/edgar process --drain
```

The news page listens on `http://127.0.0.1:8081` by default. Stop or reset
the development server with:

```sh
make nats-stop
make nats-status
make nats-reset
```

The PID, log, and JetStream store live together under `data/nats`. Reset
removes only that directory's development state. Application databases and
downloaded or parsed files remain independent and can be deleted according to
the specific replay experiment.
