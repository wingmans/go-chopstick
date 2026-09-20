# Taxonomy Tuner

This note describes a rough design for an external taxonomy tuning tool. The
tool is intended to improve the local, high-level US-GAAP taxonomy without
making taxonomy learning part of the `edgar` application.

## Purpose

The tuner is an active-learning workbench, not an automatic fine-tuning
system. It uses evidence from parsed filings, linter output, and annual-report
HTML to produce reviewable taxonomy suggestions.

The intended first scope is small and practical:

- revenue;
- gross profit;
- operating income;
- net income;
- assets;
- liabilities and equity;
- operating, investing, and financing cash flow.

The tool should help discover historical aliases and common variations across
the US-GAAP coverage set. It must not silently change reported values or
promote every unmapped concept into the production taxonomy.

## Boundaries

The `edgar` application remains responsible for deterministic parsing,
projection, persistence, and linting. The tuner remains outside the `edgar`
packages and consumes their outputs.

The following rules are important:

- raw SEC filings are pristine and must never be changed;
- parsed source data remains auditable through paths and checksums;
- XBRL facts remain authoritative for numeric values;
- annual-report HTML is supporting evidence, not numeric ground truth;
- taxonomy changes require human review;
- suggestions must not modify the checked-in taxonomy automatically.

## Inputs

The first version can work entirely from local files:

```text
internal/filingview/taxonomy.json
taxonomy-lint.json
taxonomy-coverage.json
filing-view.json
filing.json
annual-report.html
```

The linter should eventually support a structured JSON output mode. Each
finding should include, where available:

- accession, CIK, form type, and filing year;
- source path and source checksum;
- namespace and concept name;
- human-readable label;
- statement or presentation role;
- period and unit;
- dimensional status;
- occurrence count;
- selected or discarded status;
- related mapped concepts;
- diagnostic codes.

## Annual-Report Evidence

The annual-report HTML, usually the primary `r1.html` document, can provide:

- visible row labels such as `Net sales` or `Net income`;
- statement headings and nearby table context;
- displayed period columns;
- XBRL concept names and `contextRef` values;
- unit, scale, sign, and decimals;
- presentation order;
- whether a fact appears in a primary statement or a disclosure.

The first implementation should inspect primary financial statement tables
only. Narrative text, footnotes, and broad natural-language extraction can be
deferred.

HTML commonly repeats facts, hides facts, formats values, and presents
dimension-specific facts. It therefore helps explain a candidate but should
not be treated as proof that two concepts have identical meaning.

## Candidate Discovery

The tuner should rank unmapped concepts using conservative evidence:

1. Prefer standard `us-gaap` concepts over issuer extensions.
2. Require a numeric fact for numeric metrics.
3. Prefer non-dimensional contexts for primary statements.
4. Require a compatible instant or duration period.
5. Require a compatible unit.
6. Count repeated use across companies and filing years.
7. Count repeated use in the same statement.
8. Compare visible HTML labels and surrounding headings.
9. Inspect presentation and calculation relationships when available.
10. Lower confidence when the concept has conflicting uses or units.

Company extensions should be reported separately. Frequent use by one issuer
does not make an extension a general taxonomy concept.

## Suggestion Format

Suggestions should retain their evidence and be deterministic. A suggestion
identifier should be derived from the taxonomy version, candidate concept, and
evidence set so that repeated scans do not create duplicate review items.

An illustrative suggestion is:

```json
{
  "metric": "revenue",
  "candidate": {
    "namespace_family": "us-gaap",
    "name": "SalesRevenueNet"
  },
  "evidence": {
    "filings": 14,
    "companies": 8,
    "years": [2010, 2011, 2012],
    "labels": ["Net sales", "Sales revenue"],
    "statements": ["income"],
    "dimensional": false,
    "units": ["USD"]
  },
  "confidence": "review",
  "reason": "Repeated annual income-statement fact"
}
```

The exact schema can evolve, but each proposal should explain why it exists
and identify the filings that support it.

## Review Loop

A small command-line loop is sufficient:

```text
taxonomy-tuner scan \
  --taxonomy internal/filingview/taxonomy.json \
  --parsed-dir data/parsed \
  --filings-dir data/filings \
  --out work/taxonomy-review
```

```text
taxonomy-tuner review --input work/taxonomy-review
taxonomy-tuner accept --input work/taxonomy-review --suggestion S-0042
taxonomy-tuner reject --input work/taxonomy-review --suggestion S-0043
taxonomy-tuner export --input work/taxonomy-review \
  --out work/taxonomy-patch.json
```

JSON files are sufficient initially. The review directory should retain:

- the taxonomy version;
- parser version;
- source filing checksums;
- deterministic suggestion identifiers;
- accepted and rejected decisions;
- reviewer reason and timestamp;
- the generated taxonomy patch.

The exported patch is reviewed and applied separately to the maintained
taxonomy. The tuner does not write to `internal/filingview/taxonomy.json`.

## What It Can Improve

The workflow can help identify:

- historical aliases for common metrics;
- concepts whose names changed over time;
- industry-specific alternatives;
- concepts missing from the local registry;
- apparent duplicate concepts;
- concepts with incompatible units or dimensions;
- mappings that work for one issuer but fail across the coverage set.

It cannot resolve all semantic ambiguity. Similar labels may represent
different scopes, exclusions, segments, or accounting policies. Suggestions
must remain evidence-backed review items.

## Deferred Work

The first tuner should not require machine learning, a database, or a complete
US-GAAP taxonomy. Later versions may add:

- richer presentation-linkbase analysis;
- explicit dimensional and segment review;
- unit and sign normalization checks;
- accounting identity checks;
- company and industry overrides;
- a browser-based review screen;
- DuckDB-backed evidence queries once the questions are known.

An external tool with deterministic reports and human-approved patches gives
the project useful taxonomy feedback while preserving raw-data lineage and
keeping the production parser predictable.
