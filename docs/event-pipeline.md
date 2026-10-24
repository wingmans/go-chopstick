# Event pipeline

The event pipeline keeps the current modular monolith while making its role
boundaries executable. NATS is a deployment prerequisite; the application does
not embed a server.

```text
SEC Atom -> polling.db + outbox -> observed -> selection -> download -> process
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
now. Atom publishes every normalized entry as an observed discovery. The
selection worker publishes a second decision for every discovery. Only
selected decisions enter the download queue.

The combined `run` command supervises its NATS-backed roles. If a connection
or JetStream pull ends while NATS restarts, the affected role closes its old
connection and subscription, then reconnects with exponential backoff from one
to 30 seconds. Durable consumers resume from their last acknowledged message;
the HTTP filing viewer remains available during the broker outage.

Automatic processing uses `EDGAR_SET` and `EDGAR_FORM_TYPES`. The default
development scope is the 19-member `us-gaap` set and forms `10-K`, `10-Q`, and
`8-K`. The parser capability list validates the CLI and is shown by Doctor and
the news website. Unsupported forms remain visible as observed news, together
with the reason they were not selected.

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

Start all development infrastructure, application roles, and both HTTP
services in the foreground:

```sh
make dev
```

`make dev` starts the project-local NATS server, Atom discovery, selection,
download, processing, news, and the filings web server. After a reset it also
restores `data/sets/us-gaap.json` from the bundled golden snapshot. Existing
set files are not overwritten. Application startup creates missing schemas,
streams, and consumers. It preserves existing state and does not perform a
historical backfill. Press `Ctrl-C` to stop the application; NATS remains
running until `make nats-stop` or a reset.

If `master.tsv` is missing, `make dev` also starts a one-time index bootstrap
in parallel with the Atom feed. Once the master index is available, startup
reconciles the configured `EDGAR_SET`, `EDGAR_FORM_TYPES`, and
`EDGAR_FROM_YEAR` scope. Download and processing remain idempotent: existing
raw submissions and current parsed results are reused. Existing master indexes
are not refreshed at startup. Refresh the latest quarter and rebuild
`master.tsv` explicitly with:

```sh
make index-refresh
```

This command is safe to schedule once or twice per day: older quarterly files
are reused and only the latest quarter is refreshed.

Open the same development process with a tiled Ghostty console:

```sh
make watch
```

Before opening the console, `make watch` refreshes the latest SEC quarterly
index and rebuilds `master.tsv`. The console then runs `make dev`, so startup
reconciliation reads the refreshed index. It also follows discovery, download,
processing, and error logs, and refreshes a SQLite pipeline overview every
minute. Ghostty may ask for macOS Automation permission the first time the
AppleScript launcher runs.

Index downloads and `master.tsv` rebuilds use temporary files followed by an
atomic rename. Running `make index-refresh` beside an active ingest therefore
does not expose a partially written index. A reconciliation that already has
`master.tsv` open continues reading its previous snapshot; restart the ingest
or run discovery again to reconcile the replacement.

Run the overview independently when needed:

```sh
make overview
```

It uses read-only connections to `polling.db` and `feed.db`. The display
summarizes observations, outbox publication, failures, dead letters, selection
decisions, form counts, and the latest news items.

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
make logs-select
make logs-discovery
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

The news page can queue any observed filing with **Download and parse on
demand**. This publishes one selected event without expanding the automatic
set or form filters. It is intended for parser experiments that should not
turn into a broad filing archive.

Inspect documents from a parsed filing without retaining another permanent
copy:

```sh
go run ./cmd/edgar artifacts list \
  --cik 0001898496 \
  --accession 0001213900-26-111193

go run ./cmd/edgar artifacts extract \
  --cik 0001898496 \
  --accession 0001213900-26-111193 \
  --type EX-99.1 --type GRAPHIC

go run ./cmd/edgar artifacts serve \
  --cik 0001898496 \
  --accession 0001213900-26-111193

go run ./cmd/edgar artifacts clean \
  --cik 0001898496 \
  --accession 0001213900-26-111193
```

Extraction preserves filenames so local HTML can find its images, decodes
uuencoded `GRAPHIC` documents, and stops at 300 MiB per bundle. Cleanup only
removes a directory with a matching generated manifest; it never removes the
raw filing or parsed result. The Go-native viewer binds to
`http://127.0.0.1:8090`, lists every extracted document, and provides a lazy
loading thumbnail rail beside a large document preview. The Left and Right
arrow keys select adjacent documents; Home and End jump to the first and last
documents. Search, document-type, and graphics-only filters can be combined;
navigation and progress apply to the visible results. Image previews support
fit, zoom, rotation, and their corresponding keyboard shortcuts. The viewer
serves only a generated bundle with a matching manifest and does not require
Python or another external web server. Its template, stylesheet, and script
are separate source files embedded into the Go executable at build time.

Artifact tests are self-contained and generate only tiny temporary submissions
and manifests. They do not read from `data/`. Large downloaded filings may be
used for optional manual smoke tests, but `data/` remains ignored and those
filings must not become committed fixtures or required test dependencies. A
filing-specific regression should be reduced to the smallest representative
bytes before it is added to the test suite.

The explicit stages remain available for debugging.

```sh
go run ./cmd/edgar index --from-year 2024
go run ./cmd/edgar discover --set us-gaap --from-year 2024
go run ./cmd/edgar select --set us-gaap --drain
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
