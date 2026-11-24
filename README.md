# Chopstick

Chopstick is a local SEC EDGAR ingestion, processing, and inspection
application. It keeps source acquisition, durable pipeline work, domain
transformation, and HTTP presentation in one repository with explicit package
boundaries.

The repository builds two primary executables:

- `chopd` runs ingestion, processing, and the web application.
- `chop` is the operator and local-data command-line interface.

Standalone acquisition tools remain available under `cmd/ingest`:

- `constituents` builds the current constituent-set snapshot;
- `ecb-fx` fetches ECB exchange-rate observations;
- `yahoo-prices` updates local market prices and corporate actions.

## Development

Install the pinned development tools and create the local environment file:

```bash
make deps
```

Provision the local dependencies, then run the application:

```bash
make setup
make run
```

`make setup` starts NATS and initializes the application databases and
JetStream resources. It also tries to start the outbound gateway.
`make run` requires the databases and JetStream. The viewers can run while
the gateway or an upstream provider is unavailable.

Use another terminal for operator commands:

```bash
./bin/chop doctor
./bin/chop status
./bin/chop failures list
```

Common source-code checks are:

```bash
make generate
make test
make lint
make build
```

All runtime databases, retained inputs, caches, and generated data live below
`CHOPSTICK_DATA_DIR`, which defaults to `./data`.

## Architecture

The main ownership rules are:

- `cmd` parses process input and adapts it to typed operations.
- `internal/ingest` owns external source protocols and decoding.
- `internal/edgar` owns filing discovery, parsing, processing, and archive
  behavior.
- `internal/market` owns prices and corporate actions.
- `internal/constituents` owns the shared constituent model.
- `internal/chopstick` composes and supervises the application.
- `internal/chopstick/web` owns HTTP handlers, templates, assets, and viewers.
- `internal/platform` owns technical support; its `outbound` package is the
  only production external HTTP-client factory.

See the [architecture guide](docs/architecture.md),
[outbound gateway guide](docs/outbound-gateway.md), and
[ECB ingest guide](docs/ecb-ingest.md) for details.
