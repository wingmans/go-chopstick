# Operations and Runtime Manageability

## Problem

The runtime is no longer understandable from its default terminal output.
`make dev` starts multiple roles and interleaves Atom discovery, retained-event
replay, downloads, parsing, ownership ingestion, price work, and HTTP logs.
An operator cannot quickly answer:

- what started;
- whether work is bounded or continuous;
- whether data comes from cache or the network;
- which schema version each database uses;
- how much work remains;
- whether a role is caught up, idle, blocked, or failing.

Manageability is a product requirement. Another large replay should not be run
until the active phase and failure state are plainly visible.

## Architecture decision

Keep a modular monolith for now. Do not split executables merely to make the
logs look simpler. The existing roles already provide useful internal stage
boundaries, and premature process separation would add deployment and
coordination work without fixing schema or status visibility.

Split operating modes now:

- websites only;
- finite backfill or repair;
- continuous EDGAR ingestion;
- ownership ingestion;
- scheduled price ingestion;
- the complete local stack.

A role may become a separate service later when it has a demonstrably
independent lifecycle, scaling requirement, failure boundary, or deployment
cadence. Record that decision before splitting it out.

## Parser extraction

Extract parsing from runtime orchestration as a clean in-process module. The
parser boundary accepts filing bytes or a known local source plus explicit
metadata and returns typed results and diagnostics. It must not download,
publish NATS events, open application databases, or depend on process-global
configuration.

Keep the extracted parser in the same Go module initially. A separate parser
service would add transport, version negotiation, deployment, and failure
handling without a current scaling requirement. Reconsider a service only if
parser isolation, independent deployment, or distributed CPU scaling becomes
a measured need.

The extraction should also make a parser-only CLI possible for one filing or
fixture. That command must be deterministic and useful for debugging without
starting the pipeline.

## Package boundary audit

Evaluate the current package structure before adding more feature packages.
The goal is not a particular package count. The goal is one clear reason for
each package to exist.

For every package, record its responsibility, owner, public API, state, and
dependencies. Then:

- merge packages that only forward calls or split one cohesive operation;
- separate packages that mix transport, storage, parsing, and presentation;
- remove duplicate models and competing names for the same concept;
- keep database ownership and transaction boundaries explicit;
- prevent feature packages from importing command or web orchestration;
- use dependency direction tests or tooling to preserve the chosen layers.

Do not create a package for every table, form type, route, or worker. Prefer a
small feature package with internal files until an independent contract is
real.

## NATS control-plane decision

Postpone using NATS as a command and control plane. Keep it as the durable
event transport for pipeline work. The CLI and local process supervisor remain
the control plane.

Remote commands over NATS would require authentication, authorization,
idempotency, expiry, acknowledgements, leader selection, audit history, and
clear behaviour when a command is duplicated or a worker is disconnected.
That is substantial complexity without a current operational requirement.

The status command may inspect NATS stream and consumer state. It must not
publish start, stop, rebuild, or repair commands as an incidental part of
status collection. Reconsider NATS control only for a concrete multi-host or
remote-operations requirement and write a separate design first.

## Schema management

Every SQLite database must have an explicit lifecycle policy. Maintain one
inventory covering at least polling, analysis, news, ownership, and market
data. For each database, document:

- current schema version and owning package;
- whether upgrades migrate in place or rebuild from retained inputs;
- which inputs are authoritative and retained;
- exactly what a rebuild deletes and preserves;
- the command used to inspect, migrate, rebuild, and verify it;
- whether the database may be opened by an older binary.

Startup must never silently perform a destructive rebuild. On a version
mismatch it must either run a reviewed, transactional migration or stop with
the database path, current version, required version, and exact recovery
command. A dry-run must describe the impact before a rebuild begins.

Schema status belongs in the normal status output. A rebuild must end with a
verification summary rather than relying on later startup to reveal that it
worked.

## Startup behaviour

Startup must be unsurprising. Before work begins, print one compact summary:

- selected operating mode and active roles;
- finite, replay, or continuous behaviour for each role;
- effective set, date range, and form selection;
- database and cache paths;
- schema readiness;
- network access and cache-reuse policy;
- HTTP addresses;
- log level and role-specific log locations.

A websites-only command must not poll Atom, consume queues, download filings,
or start price synchronization. A finite command must exit when its declared
work is complete. A continuous command must say that it will remain running
and identify its polling schedule.

Shutdown must report which roles stopped cleanly and which work remains safe
to resume.

## Web consolidation

Serve the local web application on one port. Keep filings, ownership, news,
status, and future price views as separate internal modules, but mount them on
one HTTP server with one application shell.

Use stable route groups:

- `/` for the company and filing entry point;
- `/ownership` for ownership views;
- `/news` for filing news;
- `/status` for operational status;
- `/assets` for shared static assets;
- `/api` only for intentionally supported machine-readable endpoints.

Replace the separate news and filings listen addresses with one documented
HTTP address. Keep temporary compatibility flags only long enough to make the
migration explicit. One port does not mean one unstructured handler package
or one database.

Improve the web implementation as part of the consolidation:

- organize CSS into tokens, base rules, reusable components, and small
  page-specific files;
- use HTMX for progressive enhancement while keeping links, forms, and core
  navigation functional without JavaScript;
- introduce shared layouts, navigation, error pages, and feature-owned
  template partials;
- parse and validate every template at startup and test important rendered
  routes;
- keep handlers thin and move read models and queries into feature packages;
- use sqlc for typed SQLite queries across analysis, ownership, news, and
  market stores;
- isolate any genuinely dynamic SQL behind small, tested query builders;
- use one consistent empty, loading, error, and stale-data vocabulary;
- show source dates and coverage limitations beside financial and ownership
  data.

HTMX is not a reason to create a second client-side application architecture.
The server-rendered page remains the source of truth, and fragments reuse the
same view models and templates as full-page responses.

sqlc adoption should follow database ownership boundaries. Do not combine the
databases merely to share generated code. Each store keeps its own schema,
queries, generated package, and lifecycle policy.

## Form viewer and archive-store integration

Integrate the existing form viewer into the single-port web application. A
filing page should provide one route from a normalized result or news event to:

- filing metadata and provenance;
- the SEC submission and document inventory;
- selected primary documents and exhibits;
- parsed facts, diagnostics, and feature-specific views;
- related ownership and classified news events.

Do not build another raw-file reader inside the web package. Integrate an
`archive-store` boundary as the only component that resolves SEC-relative
paths to retained local content. It should own:

- safe path resolution below the configured archive root;
- cache lookup and optional fetch decisions;
- gzip or other storage normalization;
- source URL, content hash, media type, and retrieval metadata;
- opening a complete submission or one known document by stable identity;
- a clear missing, corrupt, stale, or available result.

The downloader, parsers, ownership importer, form viewer, and repair tools
must use the same archive-store contract. This directly prevents a downloaded
file from later being opened through an unrelated SEC-relative working path.

Keep authoritative bytes in the existing cache. Do not copy filing blobs into
feature databases. Database rows retain stable archive identity, provenance,
and hashes.

Raw filing HTML is untrusted content. The viewer must not inject it into the
application origin as active markup. Render safe text or use a sandboxed
document response with a restrictive content security policy.

## Newsworthy filing design

Keep the current chronological discovery feed as operational evidence. Add a
separate, rebuildable news classification projection. Discovery must not be
filtered or acknowledged according to a changing product-news policy.

Classify using form type, 8-K item codes, document types, structured fields,
and explicit text evidence. Form type alone is insufficient. In particular,
the SEC has no dedicated profit-warning form.

### Initial event categories

#### Results and guidance

- `8-K` items 2.02, 7.01, and 8.01, especially with `EX-99` earnings or
  investor releases;
- `6-K` results, trading updates, and guidance for foreign private issuers;
- `10-Q`, `10-K`, `20-F`, and `40-F` as periodic result availability;
- `NT 10-Q`, `NT 10-K`, and their foreign equivalents as late-filing events.

Treat an earnings release and a profit warning as different events. Label a
profit or guidance warning only when the filing explicitly lowers, withdraws,
or negatively revises guidance. Do not infer a warning merely from weaker
reported numbers or negative words.

#### Distress and reporting integrity

Prioritize these `8-K` items:

- 1.03 bankruptcy or receivership;
- 2.04 acceleration or default;
- 3.01 delisting or listing-rule failure;
- 4.01 change in certifying accountant;
- 4.02 non-reliance on prior financial statements;
- 2.05 exit or disposal costs;
- 2.06 material impairments.

These normally outrank routine earnings and governance updates.

#### Mergers, acquisitions, and control

Track a deal as a lifecycle rather than unrelated filing headlines:

- `8-K` items 1.01, 2.01, and 5.01;
- `PREM14A` and `DEFM14A` merger proxy statements;
- `S-4` and `F-4` business-combination registrations;
- `425` business-combination communications;
- `SC TO-T`, `SC TO-I`, and amendments for tender offers;
- `SC 14D9` and amendments for the target's recommendation;
- `SCHEDULE 13D` and amendments when control intent is disclosed.

Group announcement, amendment, vote, tender, completion, termination, and
withdrawal under one deal identity when explicit evidence permits it. Never
join deals only because company names are similar.

#### Ownership and insiders

- `SCHEDULE 13D` and amendments are high-interest ownership or control events;
- `SCHEDULE 13G` and amendments are ownership-state events, usually lower
  urgency unless a threshold change is explicit;
- Forms `3`, `4`, and `5` are insider events;
- Form `144` is a proposed sale, not proof that a sale occurred;
- `13F-HR` is a periodic manager snapshot, not breaking transaction news.

Do not alert on every Form 4. Initial importance rules may use reporting-owner
role, transaction code, reported value, relation to holdings after the event,
and repeated transactions. Price data may help estimate value, but retain the
reported share and price evidence and show when market value is unavailable.

#### Capital and dilution

Consider `S-1`, `F-1`, `S-3`, `F-3`, prospectus filings, and relevant `8-K`
items 2.03, 3.02, and 3.03. Distinguish shelf registration from an actual
offering, debt creation, unregistered issuance, or changed security-holder
rights. Amendments alone should not repeatedly create new alerts.

#### Leadership and governance

Use `8-K` items 5.02, 5.03, 5.07, and 5.08. Prioritize chief executive, chief
financial officer, auditor, control, and contested-vote changes. Routine board
appointments and annual vote results remain visible but lower priority.

### Classification record

Store the classification separately from the filing record:

- stable event and issuer identity;
- category, importance, lifecycle state, and confidence;
- form, accession, filing and event dates, item codes, and exhibits;
- rule version and the exact evidence that triggered the classification;
- related or superseded filing identities;
- archive-store link for direct review.

Start with deterministic rules and an explicit `unclassified` state. Do not
make an opaque language-model score the source of truth. Later summarization
may describe retained evidence, but it must not invent category, severity, or
deal linkage.

Use the official SEC descriptions as the baseline for the classifier:

- [Form 8-K](https://www.sec.gov/forms/form8-k.pdf);
- [Form 6-K](https://www.sec.gov/about/forms/form6-k.pdf);
- [SEC guide to business-combination filings](https://www.sec.gov/search-filings/edgar-search-assistance/using-edgar-research-investments).

## Status and progress

Provide one concise system status command with drill-down by subsystem. It
must cover:

- SEC index and Atom discovery;
- selection, download, and financial processing;
- ownership import and completion coverage;
- news projection and both websites;
- price freshness, missing symbols, deferred work, and the next scheduled run;
- database schema versions;
- queue depth, in-flight work, failures, throughput, and last activity.

Each role must expose a phase such as `backfill`, `replay`, `live`, `idle`,
`blocked`, or `failed`. Counts need a denominator or a clear statement that
the stream is open-ended. Status must distinguish cache hits from network
downloads and terminal unsupported outcomes from failures.

The existing price-specific status command remains useful, but the main
system status should summarize it so price health is not invisible beside
EDGAR health.

## Logging

Default output should show lifecycle transitions, periodic summaries, and
actionable failures. Per-filing success messages belong behind debug logging
or in role-specific files. Shared terminal output must identify the role and
must not repeat the same failure for every item without aggregation.

The default view should answer four questions without log archaeology:

1. What is running?
2. What phase is it in?
3. How much work remains?
4. Does an operator need to intervene?

## Makefile cleanup

The Makefile should contain small, discoverable aliases rather than hidden
workflows. Separate development convenience from operational data management:

- `dev` starts a safe, documented interactive mode;
- `web` starts websites only;
- `dev-all` explicitly starts every continuous local role;
- `status` and subsystem status targets are read-only;
- `backfill-*`, `repair-*`, and `rebuild-*` are explicit operational actions;
- destructive targets describe their scope before running;
- targets delegate to CLI commands instead of duplicating control logic.

Do not make a normal startup target implicitly clean state, change schemas, or
begin a historical reload. Keep environment defaults visible and allow a
command-line override without relying on undocumented Make variables.

## CLI improvement

Make the CLI the documented source of operational behaviour. The Makefile
should provide memorable aliases, not an alternative interface with different
defaults.

Use a consistent noun and verb structure across EDGAR, ownership, news, and
prices. Standardize database, set, date-range, log, dry-run, and output flags.
Every long-running command must state whether it is finite or continuous.

Add:

- concise top-level help grouped by browse, ingest, inspect, repair, and serve;
- `--json` for status and other automation-facing output;
- `--dry-run` for destructive or large operational actions;
- startup summaries showing resolved configuration;
- stable exit codes for invalid configuration, partial work, and failure;
- direct commands for web-only, parser-only, and subsystem status workflows;
- suggestions for the next safe command when an action cannot continue.

Evaluate a single `chopstick` command as a user-facing facade over the current
executables. Do not combine binaries until the command grammar and ownership
boundaries are clear.

## Watch improvement

`make watch` must be an observability view, not a hidden data operation. Remove
its implicit index refresh. Starting a terminal layout must not trigger network
access, a backfill, schema work, or queue replay unless the selected mode says
so explicitly.

Improve the watch layout to:

- show the unified status summary first;
- label panes by role and phase;
- show aggregated progress rather than several raw log tails;
- dedicate one pane to actionable errors and retry guidance;
- include ownership and price health;
- allow web-only, EDGAR, ownership, price, and full-stack layouts;
- reconnect to already running services without starting duplicates;
- work without Ghostty where practical, with Ghostty as an optional layout;
- print the equivalent CLI commands before opening panes.

The layout should remain useful when every subsystem is idle. It must not
depend on a constant stream of log messages to appear healthy.

## Delivery order

Treat this document and the newsworthy-form design as the decision baseline.
After review, prioritize cleanup and implementation of these points over new
runtime features. Keep each step independently testable and leave the system
operable between steps.

1. Fix incorrect ownership cache-path processing.
2. Add schema inventory and schema status.
3. Define explicit operating modes and improve the CLI grammar.
4. Add ownership and price health to unified status.
5. Reduce and separate logs, then improve `watch` around status.
6. Extract the parser boundary and complete the package audit.
7. Integrate archive-store and the form viewer.
8. Consolidate the websites onto one port and restructure the web layer.
9. Add deterministic newsworthy-event classification.
10. Move store queries to sqlc in bounded, tested steps.
11. Clean the Makefile around the stable CLI contracts.
12. Reassess process splitting using observed failure and resource boundaries.
