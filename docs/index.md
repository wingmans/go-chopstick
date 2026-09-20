# EDGAR Project Guide

This is the human entry point for the SEC EDGAR part of the project. It
describes the working model, the data that is produced, and the decisions that
keep the system understandable while it grows.

The central lesson so far is simple:

> Keep the SEC filing and the parsed evidence intact. Build small, explicit,
> rebuildable projections for the product, and require human approval before a
> validation finding changes parser or taxonomy behavior.

## Where To Start

### Filing forms

The form-specific notes explain what the SEC submission contains and what the
reader extracts:

- [10-K: annual reports](03-forms/10-K.md)
- [10-Q: quarterly reports](03-forms/10-Q.md)
- [8-K: current reports and exhibits](03-forms/8-K.md)

The current product validation target is consolidated `10-K` data. `10-Q` and
`8-K` remain documented and supported as filing shapes, but they are not part
of the current golden-19 quality gate.

### Main technical notes

- [EDGAR pipeline](01-overview/edgar-pipeline.md): the end-to-end flow.
- [EDGAR data decisions](02-edgar/edgar-data-decisions.md): storage, paths,
  rate limits, and download behavior.
- [Parser and XBRL reader](04-parser/parser-xbrl-reader.md): submission parsing,
  normalized facts, diagnostics, and persistence.
- [`filing-view.json`](04-parser/filing-view.md): the compact model consumed
  by the web
  dashboard.
- [Taxonomy decisions](05-taxonomy/taxonomy.md): the local concept registry
  and its
  boundaries.
- [Data quality policy](01-overview/data-quality.md): units, signs, dimensions,
  accounting identities, and human review.
- [Taxonomy validation reports](05-taxonomy/taxonomy-validation-report.md):
  coverage,
  lint, findings, and validation gates.
- [Taxonomy linter](05-taxonomy/taxonomy-linter.md): compact and source review.
- [Taxonomy tuner](05-taxonomy/taxonomy-tuner.md): candidate discovery and the
  review
  workflow for future mappings.
- [XBRL parsing error log](04-parser/xbrl-parsing-error-fix.md): legacy and
  malformed
  filing behavior.
- [Official SEC references](90-reference/sec.md): filing search, EDGAR
  indexes, and SEC XBRL documentation.
- [Remaining work](01-overview/todo.md): deferred product and infrastructure
  work.

## The Pipeline

The pipeline separates upstream evidence from derived application data:

```text
SEC EDGAR
  -> quarterly indexes
  -> stitched master.tsv
  -> downloaded filing submissions
  -> parsed filing.json
  -> compact filing-view.json
  -> web pages, lint, coverage, and validation
```

### 1. Index

`edgar index` downloads quarterly SEC index archives, extracts their TSV
records, and can stitch them into `data/indexes/master.tsv`.

Older index files are reused from disk. `--refresh-latest` is deliberately
explicit: it refreshes the newest available quarter while older quarters stay
local. A normal rerun should not create network traffic for unchanged index
data.

### 2. Filing selection and download

`edgar filings` selects records from `master.tsv`, normally by form type,
company set, and year. It downloads only missing local submissions and keeps
the SEC-relative path so the local file can be traced back to its source URL.

For the current end-to-end target:

```bash
REFRESH_LATEST=1 make integration
```

The integration script selects `10-K` for both download and parse. This is
intentional: old local `8-K`, `10-Q`, and Form 4 files may still exist, but
they are outside the current product scope and should not create validation
noise.

### 3. Parse

`edgar parse` reads local submissions and writes a verbose `filing.json`.
The parser reads the submission envelope, document inventory, inline XBRL,
contexts, units, facts, namespaces, and diagnostics. The raw submission is
never rewritten.

The parser may create a parser-only normalization copy for narrowly understood
legacy defects such as malformed XML entities or split tag syntax. The copy is
used to recover facts; the pristine SEC bytes, normalization diagnostics, and
lineage remain available.

### 4. Compact

The filing-view builder selects default-context facts, resolves taxonomy
aliases, normalizes units, derives reviewed ratios, and evaluates accounting
identity checks. It writes `filing-view.json` beside `filing.json`.

The compact file is disposable. It can and should be rebuilt after parser or
taxonomy changes.

### 5. Review

Validation has two complementary views:

- source coverage asks what the filing contains before compaction;
- compact lint asks whether the product projection is mapped and internally
  coherent.

The current 19-company gate is GREEN when all expected filings parse, compact
quality is clean, and remaining hard core gaps are either supported by review
evidence or explicitly accepted.

## XBRL In Brief

XBRL is a structured reporting language, not a single financial table. A
filing contains facts identified by concepts and qualified by context.

### Purpose

XBRL lets a filing report machine-readable financial facts while retaining
meaning about the reporting entity, period, unit, presentation, and sometimes
dimensions. The same business idea can still be expressed through different
concepts or issuer extensions, which is why a product needs a reviewed mapping
layer.

### Structure

- **Concept:** the meaning of a fact, such as `Assets` or `Revenues`.
- **Fact:** a reported value for a concept.
- **Context:** entity, period, and optional dimensions for the fact.
- **Unit:** USD, shares, a per-share unit, or another measure.
- **Dimension:** a segment, geography, product, class, or other breakdown.
- **Namespace:** the taxonomy family and version that define the concept.
- **Relationships:** presentation, calculation, definition, and label links
  that help explain how concepts relate.

The project preserves these source details in `filing.json`. It does not
pretend that a label alone is enough to establish equivalence.

### The local taxonomy

The local taxonomy is a small product registry in
`internal/filingview/taxonomy.json`. It maps several possible source concepts
to stable application keys such as `revenue`, `net_income`, `assets`, and
`operating_cash_flow`.

Its purpose is comparability, not completeness. It gives the dashboard a
stable vocabulary while retaining the original namespace and concept for
auditability. It is not a replacement for the official SEC or FASB taxonomy.

The registry contains:

- metric keys and display labels;
- statement ownership, such as income, balance, or cash flow;
- ordered concept aliases and priorities;
- coverage tiers such as core or industry-sensitive;
- explicit ratio definitions for derived values.

The taxonomy must remain deterministic and human-reviewed. Encountering a new
concept must not silently change product behavior.

## Parser And Reader Outcomes

The useful parser outcomes are visible in command logs and persisted data:

- `processed`: a local filing was parsed or rebuilt;
- `reused`: existing parsed output was considered current;
- `failed`: processing could not produce a valid result;
- `diagnostic`: a recoverable or reviewable source condition was recorded;
- `noop`: the command described work without changing files.

An overall processing summary reports selected, processed, reused, planned, and
failed filings. A non-fatal diagnostic is not the same thing as a failed
filing. For example, a document-count mismatch can be retained for review
while parsing succeeds and the filing remains usable.

The reader's durable evidence is:

- raw bytes under `data/filings`;
- verbose parsed facts and diagnostics in `filing.json`;
- compact selected facts and quality findings in `filing-view.json`.

### Why linter findings are not automatically a problem

The source taxonomy report can contain a very large number of findings. Many
are expected and not useful as product defects:

- dimensional facts are deliberately excluded from consolidated statement
  rows;
- issuer extensions and disclosure concepts are outside the current dashboard;
- industry-sensitive metrics are not uniformly reported by banks, insurers,
  REITs, or industrial companies;
- a source can report a broader but valid concept, such as consolidated cash;
- a filing can genuinely omit a metric for a period.

The compact linter is the more relevant product check. In the accepted
golden-19 run it reports zero unmapped compact findings and zero quality
issues. The remaining source findings are evidence for future taxonomy work,
not a reason to chase every concept or declare the parser unusable.

## Data And File Types

`data/` is a working data area. Some contents are source evidence; others are
derived and disposable.

```text
data/
  cache/index-zips/       downloaded SEC index ZIPs
  indexes/quarterly/     extracted quarterly index TSVs
  indexes/master.tsv     stitched filing catalog
  filings/edgar/data/    pristine downloaded SEC submissions
  parsed/<cik>/<acc>/
    filing.json           verbose parsed submission and XBRL facts
    filing-view.json      compact, rebuildable product projection
  sets/                   local copies of selected constituent sets
  validation/runs/        disposable run reports, logs, and summaries
```

The durable expectations and policy files live outside `data/`:

- `golden/`: small input sets, accepted exceptions, and manual expected
  values;
- `internal/filingview/taxonomy.json`: the shipped taxonomy registry;
- `internal/*/testdata/`: focused parser and coverage fixtures;
- `docs/`: decisions, operating notes, and lessons learned.

The cleanup rule is therefore conservative: remove derived data when a clean
rebuild is needed, but preserve raw filings and cached source evidence unless
there is a deliberate reason to discard them.

## Linter, Coverage, And Tuner

### Compact linter

The linter checks saved `filing-view.json` files against the local taxonomy:

```bash
edgar taxonomy lint --set us-gaap-coverage --form-type 10-K
edgar taxonomy lint --file data/parsed/.../filing-view.json
```

It reports unmapped compact terms and quality findings. It does not edit the
taxonomy or fail merely because the source filing contains extra concepts.

### Source coverage

Coverage inspects `filing.json` before compaction:

```bash
edgar taxonomy coverage --set us-gaap-coverage --form-type 10-K
```

This is intentionally noisier. It answers "what did the filing contain?"
rather than "what does the product currently display?"

### Taxonomy tuner

The tuner workflow is a candidate-discovery aid, not an autonomous mapper. It
can group frequent unmapped concepts, attach filing evidence, and produce a
reviewable suggestion. A human must decide whether the concept belongs in the
product taxonomy, which metric it means, and whether the evidence is
consolidated, dimensional, or industry-specific.

The safe loop is:

```text
coverage finding
  -> inspect source filing and context
  -> propose candidate mapping
  -> human review
  -> small taxonomy patch
  -> tests and golden validation
```

The tuner should never edit `edgar`, rewrite raw facts, or promote a mapping
only because it is frequent. See
[taxonomy-tuner.md](05-taxonomy/taxonomy-tuner.md) for the detailed workflow
and boundaries.

## Quality Guardrails And The Stopping Rule

The project is not trying to make XBRL perfectly uniform. The practical target
is reliable consolidated company-level metrics for the current product and
backtests.

The current guardrail set is sufficient when:

- `make test`, `make lint`, and `make manual-checks` pass;
- the clean golden-19 run parses every expected 10-K;
- compact lint has no quality issues;
- hard core gaps are zero or explicitly accepted with evidence;
- the five-company manual baseline remains green.

At that point quality checks continue as regression protection, but active
taxonomy expansion becomes maintenance work. The next value should come from
the product and backtesting workflows, not from eliminating the long tail of
source-level findings.

## Useful Commands

```bash
make build
make test
make lint
make manual-checks
REFRESH_LATEST=1 make integration
SET_NAME=us-gaap-coverage FROM_YEAR=2015 TO_YEAR=2025 make taxonomy-validation
make docs-pdf
```

`make docs-pdf` combines this index and all documentation chapters into
`edgar-project-guide.pdf`. The script installs missing `pandoc` and BasicTeX
dependencies through Homebrew. A project-local `pandoc` can be selected with
`PANDOC_BIN=/path/to/pandoc`; XeLaTeX remains a system toolchain dependency.

For a deeper decision or an older failure, start with the linked note rather
than the run directory. Run directories are evidence snapshots and may be
cleaned; the documentation and golden fixtures are the durable project memory.
