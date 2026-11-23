# Chopstick Architecture

Chopstick is one application with two executable boundaries. It is not a set
of independently deployed services.

## Executables

`cmd/chop` owns the Kong command hierarchy, flags, environment defaults, and
the adaptation from command-line values to typed use-case configuration. It
does not implement application workflows.

`internal/chopstick/operator` owns the typed operator use cases invoked by
`chop`. It does not parse command-line arguments.

`cmd/chopd` owns daemon flags and starts `internal/chopstick`. The `chopstick`
package composes and supervises every continuous role in one foreground
process.

## Package map

The top-level package groups describe responsibility rather than technical
layers:

- `internal/chopstick` composes the application, operator workflows, and web
  presentation;
- `internal/edgar` owns EDGAR parsing, filings, ownership, news, archive, and
  pipeline behavior;
- `internal/market` owns prices and corporate actions;
- `internal/constituents` owns the shared constituent model;
- `internal/ingest` contains concrete external-source adapters; and
- `internal/platform` contains technical support such as logging, outbound
  HTTP, application paths, environment loading, and local transport.

## Functional flow

`internal/ingest` contains source-specific acquisition adapters. Its `sec`,
`yahoo`, `ecb`, and `wikipedia` packages own external request formats and
source decoding. They do not own feature databases or HTTP presentation.

The SEC Atom and quarterly-index sources enter the same pipeline store and
outbox. `internal/edgar/ingest` owns discovery scheduling, deduplication,
durable import, and event adaptation. SEC protocol and source-record handling
live below `internal/ingest/sec`.

`internal/edgar/pipeline` owns the durable filing event, deduplication,
outbox, NATS delivery, failure state, and serial workers. Its files expose the
application flow: selection, submission acquisition, filing extraction,
ownership consumption, and news projection. Each step has one worker.
An outbox row is marked published only after JetStream acknowledges it. A
bounded acknowledgement timeout returns control to the current stage while
leaving unacknowledged rows available for a later run.
All continuous SEC readers and startup index downloads share one process-wide
request gate. HTTP 429 responses pause that gate and receive increasing retry
delays. HTTP 403 responses enforce the SEC's ten-minute quiet period. This
prevents independent workers from exceeding the aggregate request budget.
Financial discovery applies the configured form and constituent-set policy.
Automatic ownership discovery is issuer-scoped to the same constituent set.
It currently selects Form 4/4A and structured `SCHEDULE 13D/G` submissions.
The ownership package retains the broader form registry for explicit operator
work and future pipeline scope.

`internal/edgar/parser` produces source-level filing data.
`internal/edgar/filing` owns the persisted normalized financial model, file
I/O, history, and taxonomy. `internal/edgar/filing/normalize` constructs that
model from parsed filings, while `internal/edgar/filing/quality` owns coverage,
identity, and lint diagnostics. `internal/edgar/filing/process` coordinates
synchronous filing extraction, `internal/edgar/filing/store` persists read
models, and `internal/edgar/filing/dividend` owns the dividend projection.
EDGAR ownership and news keep their own feature packages and databases.

`internal/market` owns Yahoo synchronization, normalized CSV landing batches,
watermarks, repair work, and market persistence. Only Yahoo HTTP and JSON
handling lives in `internal/ingest/yahoo`.

`internal/constituents` owns validated constituent snapshots. Wikipedia is a
temporary membership adapter and will be replaced by an MSCI adapter without
changing the constituent model or command workflow.

## Dependency rules

- Commands parse and adapt; they do not contain functional workflows.
- `chopstick/operator` contains use cases and operator rendering, not CLI
  parsing.
- `chopstick` composes packages; it does not own domain transformations.
- `platform` depends only on the standard library and third-party packages.
- Source adapters do not import `chopstick` or own feature persistence.
- `edgar` and `market` may use source adapters, constituents, and platform
  packages, but never import `chopstick`.
- Domain packages do not import commands or web handlers.
- `internal/platform/transport` is the local CLI control connection.
- `internal/edgar/pipeline` owns NATS delivery for pipeline events.
- NATS remains an independently run local service.

## Storage and availability

Polling, analysis, ownership, news, and market data remain in separate
databases. This cleanup does not change their schemas or locations.

The application remains useful when a future archive service is unavailable:
source acquisition can continue to fetch filings from SEC EDGAR. Archive
deployment and protocol decisions are intentionally outside this boundary.

## Web routes

`internal/chopstick/web` owns all HTTP presentation, including the artifact
viewer and its embedded assets. `internal/edgar/artifacts` owns extraction and
generated bundle operations, but no HTTP handler.

The application shell links the public filings, ownership, news, and status
modules. Company, filing, ownership-company, and raw-inspector pages are
contextual links from those modules. APIs, actions, assets, and raw documents
are intentionally absent from primary navigation.

## Source retention

Retention follows the cost and authority of each source rather than a generic
archive-every-response rule:

- pristine SEC filing submissions and index ZIPs are retained as source
  evidence;
- SEC Atom XML is transient because quarterly indexes reconcile discovery;
- Yahoo retains normalized CSV landing batches and synchronization state, not
  raw JSON responses;
- ECB observations are reacquired when needed;
- constituent snapshots are retained, but Wikipedia HTML is not.

A future EDGAR archive service is separate from form processing. It may retain
a form before Chopstick supports parsing or transforming that form. Archive
service integration is not part of the current package cleanup.

## Future ingest commands

Future integration will expose source operations below `chop ingest`. Sources
will be assembled from one explicit compile-time list. Runtime plugin loading,
reflection, and implicit `init` registration are intentionally excluded.
