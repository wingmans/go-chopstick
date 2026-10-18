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

JetStream delivery is at least once. Download and processing therefore retain
their existing idempotent file and database checks. Consumers are serial for
now. Subject suffixes route broad categories such as form type; filtering on
CIK or date remains consumer-side.

The news service owns `feed.db`. It orders items by JetStream stream sequence,
not by a locally generated timestamp. If `feed.db` is deleted, the next start
recreates it and resets only the news durable consumer so the retained stream
is replayed from the beginning.

## Development

Start a file-backed development server:

```sh
scripts/nats-dev.sh start
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

Reconcile discoveries from the stitched index as a one-shot command:

```sh
go run ./cmd/edgar batch --master ./data/cache/indexes/master.tsv
```

The news page listens on `http://127.0.0.1:8081` by default. Stop or reset the
development server with:

```sh
scripts/nats-dev.sh stop
scripts/nats-dev.sh reset
```

Reset removes only `var/nats/store` and its local log. Application databases
and downloaded or parsed files remain independent and can be deleted according
to the specific replay experiment.
