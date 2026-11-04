# Chop Architecture

Chop is one application with two executable boundaries. It is not a set of
independently deployed services.

## Executables

`cmd/chop` owns the Kong command hierarchy, flags, environment defaults, and
the adaptation from command-line values to typed use-case configuration. It
does not implement application workflows.

`internal/operator` owns the typed operator use cases invoked by `chop`. It
does not parse command-line arguments.

`cmd/chopd` owns daemon flags and starts `internal/chopstick`. The `chopstick`
package composes and supervises every continuous role in one foreground
process.

## Functional flow

`internal/edgar` accesses SEC indexes and filing sources. Concrete Atom and
quarterly-index sources live in `internal/chopstick/ingest`. Both sources
enter the same pipeline store and outbox. Index ingestion also reports the
local quarterly inventory and keeps a small import-provenance file beside the
derived master index.

`internal/chopstick/pipeline` owns the durable filing event, deduplication,
outbox, NATS delivery, failure state, and serial workers. Its files expose the
application flow: selection, submission acquisition, filing extraction,
ownership import, and news projection. Each step has one worker.

`internal/parser` produces source-level filing data. `internal/filing` owns
the persisted normalized financial model, file I/O, history, and taxonomy.
`internal/filing/normalize` constructs that model from parsed filings, while
`internal/filing/quality` owns coverage, identity, and lint diagnostics.
`internal/filing/process` coordinates synchronous filing extraction,
`internal/filing/store` persists read models, and `internal/filing/dividend`
owns the dividend projection. Ownership, market,
and news keep their own feature packages and databases.

## Dependency rules

- Commands parse and adapt; they do not contain functional workflows.
- `operator` contains use cases and operator rendering, not CLI parsing.
- `chopstick` composes packages; it does not own domain transformations.
- Domain packages do not import commands, `operator`, `chopstick`, or web
  handlers.
- `internal/transport` is the local CLI control connection.
- `internal/chopstick/pipeline` owns NATS delivery for pipeline events.
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
