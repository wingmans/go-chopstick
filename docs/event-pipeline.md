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

Prepare the project-local tools and copy the development configuration:

```sh
make setup
```

Make reads `.env` and exports its documented settings to the application. CLI
flags still override application defaults. The normal development scope uses
`EDGAR_SET` and `EDGAR_FROM_YEAR`; the upper boundary is the current available
data. `CHOPSTICK_DATA_DIR` defaults to `./data` and is the root for all runtime
data, caches, databases, logs, and temporary files.

The pinned NATS server is installed into the git-ignored `.tools/bin`
directory. This does not install a system-wide binary or service. Update the
SEC contact address in `.env` before running the service.

Check configuration and infrastructure without changing them:

```sh
go run ./cmd/edgar doctor
go run ./cmd/edgar doctor --verbose
```

Run a finite historical pipeline with an explicit scope:

```sh
go run ./cmd/edgar backfill --set us-gaap --from-year 2024
```

The command retrieves and stitches indexes, discovers the selected filings,
and drains download and processing work.

Start the development infrastructure explicitly, then run all application
roles and both HTTP services in the foreground:

```sh
make nats-start
make dev
```

`make dev` starts Atom discovery, download, processing, news, and the filings
web server. It creates missing schemas, streams, and consumers through normal
application startup. It preserves existing state and does not perform a
historical backfill. It never starts a background process. Press `Ctrl-C` to
stop the application; the explicitly started NATS server remains running.

Inspect current state and unresolved failures from another terminal:

```sh
go run ./cmd/edgar status
go run ./cmd/edgar errors
```

`status` is the catch-up view. Each durable consumer reports its remaining
backlog, in-flight work, redeliveries, last activity, and whether it is caught
up. Run it again whenever a point-in-time snapshot is needed:

```sh
make status
```

Workers retry failed work five times before terminating that delivery and
leaving a durable failure record in `polling.db`. Requeue failures explicitly:

```sh
go run ./cmd/edgar retry --stage download
go run ./cmd/edgar retry --stage process
```

Normal logs include a progress summary every five seconds. `LOGLEVEL=debug`
also shows individual work decisions. `make dev` appends its output to
`data/logs/edgar.log`. Follow that file from another terminal when needed:

```sh
tail -F data/logs/edgar.log
```

The same file can back one terminal per role without running another service:

```sh
make logs-atom
make logs-download
make logs-process
make logs-news
make logs-filings
make logs-errors
```

These views filter the structured fields in the text log. The download and
process views show completed counts, the current durable backlog, failures,
and the rate since that worker started. JSON logging is not required for this
workflow.

The explicit stages remain available for debugging.

```sh
go run ./cmd/edgar index --from-year 2024
go run ./cmd/edgar discover --set us-gaap --from-year 2024
go run ./cmd/edgar download --drain
go run ./cmd/edgar process --drain
```

The news page listens on `http://127.0.0.1:8081` by default. Set
`EDGAR_NEWS_ADDRESS` in `.env` to use another address. The parsed-filings web
server defaults to `http://127.0.0.1:8083`; configure it with
`EDGAR_FILINGS_ADDRESS`. Both start with `make dev`. Run only the filings web
server for isolated debugging with:

```sh
make filings-web
```

Stop or reset the development server with:

```sh
make nats-stop
make nats-status
make nats-reset
```

The PID, log, and JetStream store live together under `data/nats`. Reset
removes only that directory's development state. Application databases and
downloaded or parsed files remain independent and can be deleted according to
the specific replay experiment.

Use `make clean-dev` to remove processing state while preserving downloaded
filings, index source material, and constituent sets. Use `make reset-dev` to
remove all application data and start again without any cached downloads. Both
commands stop the project-local NATS server before removing its state.
