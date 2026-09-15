# Taxonomy Decisions

The project uses a small, local taxonomy to give common SEC XBRL concepts
stable product names. It is a comparison aid for the dashboard, not a
replacement for the official SEC or FASB taxonomies.

## Source Of Truth

The initial registry is stored in
`internal/filingview/taxonomy.json` and embedded in the binary. Embedding the
default keeps behavior independent of the process working directory. The
loader also accepts an explicit JSON path for experiments and manually
trimmed registries.

The registry has a schema version and a taxonomy version. Taxonomy changes
must be reviewed and committed like code because they can change displayed
values and comparisons.

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

The first linter reports the taxonomy version, projected row count, mapped
rows, and unmapped terms with their source namespace and occurrence count.
This is deliberately a view-level check. It cannot report facts discarded
during compaction.

Future source coverage analysis should inspect `filing.json` and classify
facts as mapped, unmapped, ignored, dimensional, or unsupported. Suggestions
may be generated for review, but the taxonomy should not learn automatically
from encountered filings.

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
