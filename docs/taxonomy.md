# Taxonomy Decisions

The project uses a small, local taxonomy to give common SEC XBRL concepts
stable product names. It is a comparison aid for the dashboard, not a
replacement for the official SEC or FASB taxonomies.

## Registry

The default registry is stored in
`internal/filingview/taxonomy.json` and embedded in the binary. This keeps
behavior independent of the process working directory. The loader also
accepts an explicit JSON path for experiments and manually trimmed registries.

The registry has both a schema version and a taxonomy version. Changes must be
reviewed and committed like code because they can change displayed values and
comparisons.

## Storage Decision

The taxonomy is application configuration and is version-controlled with the
code that interprets it.

The taxonomy does not belong beside golden filings. The directory boundaries
are:

- `internal/filingview/taxonomy.json`: shipped default taxonomy;
- `golden/`: test filings and expected results;
- `data/sets/`: constituent memberships;
- `data/parsed/`: generated parsed results;
- `data/filings/`: pristine downloaded filings.

Experimental or manually trimmed registries can be stored elsewhere and
selected explicitly with `--taxonomy`:

```text
edgar taxonomy lint --taxonomy ./my-taxonomy.json --file ./filing-view.json
```

If multiple maintained variants become necessary later, they can be added
under `internal/filingview/taxonomies/`. A single embedded default plus
explicit overrides is sufficient for now.

## Concept Mapping

Each metric has a stable key, display label, statement section, and one or
more concept references. A reference contains a local concept name, a
namespace family, and an alias priority.

The namespace family is important. Matching only a local name could confuse
a US-GAAP element with a company extension. The first implementation
recognizes versioned US-GAAP namespace URLs while retaining the exact source
namespace in `filing-view.json`.

The canonical key is used by the application. The original concept and
namespace remain in the view for auditability and later review.

## What Is Not Mapped

An unmapped fact is not automatically invalid. Filings contain dimensions,
segments, industry metrics, company extensions, and supporting disclosures
that do not belong in the primary dashboard statements.

The taxonomy linter therefore reports unmapped terms as findings. It does not
fail a filing, alter raw data, or automatically add terms to the registry.

## Linting

Compact views can be inspected with:

```text
edgar taxonomy lint --file data/parsed/.../filing-view.json
```

Without `--file`, the linter scans the local parsed directory and supports the
same selection filters as filing processing:

```text
edgar taxonomy lint --cik 789019 --form-type 10-K --year 2024
edgar taxonomy lint --set golden --form-type 10-K
```

The batch linter uses view metadata and does not require `master.tsv`. Results
are sorted by path. A load or schema error fails the command; unmapped terms
are reported as findings and do not fail the command.

The compact linter reports the taxonomy version, projected row count, mapped
rows, unmapped terms, and basic data-quality findings. It cannot report facts
discarded during compaction.

Source-level coverage is available for verbose parsed filings:

```text
edgar taxonomy coverage --cik 789019 --form-type 10-K
edgar taxonomy coverage --file data/parsed/.../filing.json
```

Coverage reports every source fact before compaction. It includes unmapped
concepts, company extensions, dimensional facts, missing contexts, invalid
periods, missing units, and duplicate facts.

The `us-gaap-coverage` fixture contains nineteen companies selected across
technology, communications, financials, healthcare, energy, consumer
staples, industrials, materials, real estate, and utilities. The small
`golden` fixture remains available for fast tests.

Source coverage now inspects `filing.json` before compaction. Future
improvements may add ignored and unsupported categories plus reviewable
suggestions, but the taxonomy should not learn automatically from encountered
filings.

## Rollups And Ratios

Concept aliases and accounting formulas are separate concerns. A parent
relationship alone cannot express calculations such as gross profit or a
margin. The registry maps source concepts to metrics; ratio definitions name
their inputs and formula. Future validation rules can check accounting
identities without changing reported values.

## Industry-Grade Direction

The foundation is considered reliable when mappings are deterministic,
units and periods are validated, dimensional facts are separated, source
checksums and parser versions are retained, and fixtures cover companies,
sectors, forms, and filing eras.

The official source filing remains pristine. Compact views are disposable,
versioned projections that can be rebuilt whenever parser or taxonomy logic
changes.
