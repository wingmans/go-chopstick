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

The POC pipeline supports filings from 2016 onward. Index download and batch
discovery enforce this boundary so a clean run cannot accidentally enqueue the
less stable legacy formats. The raw submission path from the SEC master index
is authoritative. HTML index paths are not synthesized or downloaded.

JetStream delivery is at least once. Download and processing therefore retain
their existing idempotent file and database checks. Consumers are serial for
now. Subject suffixes route broad categories such as form type; filtering on
CIK or date remains consumer-side.

The news service owns `feed.db`. It orders items by JetStream stream sequence,
not by a locally generated timestamp. If `feed.db` is deleted, the next start
recreates it and resets only the news durable consumer so the retained stream
is replayed from the beginning.

## Development

Install the pinned NATS server binary into the git-ignored `.tools/bin`
directory. This does not install a system-wide binary or service.

```sh
make deps
```

Start the file-backed development server:

```sh
make nats-dev
```

Run every continuous role in one process:

```sh
go run ./cmd/edgar run
```

The roles can instead be inspected independently:

```sh
go run ./cmd/edgar atom
go run ./cmd/edgar download
go run ./cmd/edgar process
go run ./cmd/edgar news
```

Reconcile discoveries from the stitched index as a one-shot command. Batch
selection supports CIK, constituent set, form type, and filing-year filters.

```sh
go run ./cmd/edgar batch --set us-gaap --from-year 2022 --to-year 2026
```

Download and process the current backlog as separate, finite steps:

```sh
go run ./cmd/edgar download --drain
go run ./cmd/edgar process --drain
```

The news page listens on `http://127.0.0.1:8081` by default. Stop or reset the
development server with:

```sh
make nats-stop
make nats-status
make nats-reset
```

The PID, log, and JetStream store live together under `data/nats`. Reset
removes only that directory's development state. Application databases and
downloaded or parsed files remain independent and can be deleted according to
the specific replay experiment.
