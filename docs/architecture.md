# Chop Architecture

Chop is one application with two executable boundaries. It is not a set of
independently deployed services.

## Executables

`cmd/chop` owns the Kong command hierarchy, flags, environment defaults, and
the adaptation from command-line values to typed use-case configuration. It
does not implement application workflows.

`internal/choppie` owns the typed operator use cases invoked by `chop`. It does
not parse command-line arguments.

`cmd/chopd` owns daemon flags and starts `internal/edgarapp`. The `edgarapp`
package composes and supervises every continuous role in one foreground
process.

## Functional flow

`internal/edgar` accesses SEC indexes and filing sources. The
`internal/edgar/atom` package discovers current filings from the SEC Atom
feed.

`internal/filing/work` owns the durable filing event, deduplication, outbox,
and failure state. `internal/eventbus` transports those events through NATS,
and `internal/eventworker` runs the serial consumers.

`internal/parser` produces source-level filing data. `internal/filing`
produces normalized financial read models, while `internal/pipeline`
coordinates the finite processing workflow. Ownership, market, and news keep
their own feature packages and databases.

## Dependency rules

- Commands parse and adapt; they do not contain functional workflows.
- `choppie` contains use cases and operator rendering, not CLI parsing.
- `edgarapp` composes packages; it does not own domain transformations.
- Domain packages do not import commands, `choppie`, or web handlers.
- `internal/transport` is the local CLI control connection.
- `internal/eventbus` is the NATS adapter for durable pipeline events.
- NATS remains an independently run local service.

## Storage and availability

Polling, analysis, ownership, news, and market data remain in separate
databases. This cleanup does not change their schemas or locations.

The application remains useful when a future archive service is unavailable:
source acquisition can continue to fetch filings from SEC EDGAR. Archive
deployment and protocol decisions are intentionally outside this boundary.

## Web routes

The application shell links the public filings, ownership, news, and status
modules. Company, filing, ownership-company, and raw-inspector pages are
contextual links from those modules. APIs, actions, assets, and raw documents
are intentionally absent from primary navigation.
