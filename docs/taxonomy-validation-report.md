# Taxonomy Validation Reports

This note defines the validation report as the evidence contract between the
EDGAR parser and the external taxonomy tuner.

The report is diagnostic output. It does not change raw SEC filings, parsed
filings, or the production taxonomy. It makes taxonomy quality measurable and
gives a reviewer enough context to approve or reject a proposed mapping.

## Purpose

The report should answer four questions:

- Which filings and facts were inspected?
- Which facts mapped to known product metrics?
- Which facts were omitted, ambiguous, or inconsistent?
- What evidence supports a possible taxonomy change?

The parser remains deterministic. The tuner consumes reports and emits a
reviewable taxonomy patch outside the `edgar` application.

## Expected-Set Coverage

The separate `edgar coverage` command compares selected master-index records
with local downloaded filings and parsed views:

```text
edgar coverage --set us-gaap-coverage --form-type 10-K \
  --from-year 2010 --to-year 2025
```

It writes `data/validation/coverage-report.json` by default. The report keeps
these states separate:

- `missing`: a master record has no local filing;
- `downloaded`: the filing exists but has no usable parsed view;
- `parsed`: both the filing and parsed view exist;
- `parsed_error`: the parsed view exists but cannot be loaded;
- `source_error`: the local filing path cannot be inspected.

For parsed views, the report also records which canonical taxonomy metrics are
present, missing, or supplemental-only. Missing quarterly index files and
missing filings produce structured acquisition requests. These requests are
suggestions for a later index, filings, or parse invocation; the coverage
command never executes them.

The expected population is deliberately conservative: it is made from records
present in `master.tsv` after applying the set, CIK, form-type, and year
filters.
A company is not assumed to file every form in every calendar year. Missing
master records therefore require index coverage to be repaired and the master
file to be stitched before they can become expected filing records.

## Report Artifacts

A validation run should write a directory containing:

```text
validation-report.json
filings.jsonl
findings.jsonl
suggestions.jsonl
```

The JSONL files are optional in the first implementation. They are useful when
the report becomes large because each filing or finding can be streamed and
inspected without loading the complete report into memory.

The report directory should also retain the input manifest or a reference to
it. This makes a later run comparable even when the parsed directory has
changed.

## Run Metadata

The top-level report should contain stable metadata:

```json
{
  "schema_version": 1,
  "taxonomy_version": "2026-01",
  "parser_version": "git:abc123",
  "generated_at": "2026-09-16T12:00:00Z",
  "selection": {
    "set": "us-gaap-coverage",
    "ciks": ["789019"],
    "form_types": ["10-K"],
    "years": [2010, 2025]
  },
  "input_count": 1,
  "filing_count": 1,
  "finding_count": 2,
  "status": "review"
}
```

`generated_at` is useful for audit trails but must not affect finding or
suggestion identifiers. The parser and taxonomy versions should identify the
logic that produced the evidence. Source filing checksums should identify the
actual input bytes.

## Filing Evidence

Each filing entry should include:

- CIK, accession number, form type, filing date, and report year;
- source path and source SHA-256 checksum;
- parsed view path and parsed-view checksum;
- processing status;
- counts of source facts, mapped facts, unmapped facts, and discarded facts;
- counts of duplicate candidates, dimensional facts, and unit issues;
- the concepts and metrics observed in that filing.

The report should retain the relative paths used by the application. Absolute
paths make reports difficult to move between machines and test environments.

## Findings

Findings describe a condition that needs attention. Each finding should have:

- a stable code;
- severity: `info`, `review`, or `error`;
- CIK, accession, form type, and report year when known;
- source concept and namespace;
- metric key when a mapping was attempted;
- unit, period, and dimensional status;
- a concise message;
- a source path and line or fact identifier when available.

Useful initial codes include:

```text
unmapped_concept
company_extension
duplicate_fact
unit_mismatch
sign_mismatch
dimensional_fact
missing_unit
invalid_period
identity_failure
```

An `error` means the data cannot be trusted for the relevant validation
operation. A `review` finding means the value may be useful but requires a
human decision. An `info` finding is retained for context and does not require
action.

Unmapped concepts should normally be `review`, not `error`. Many valid XBRL
facts are dimensions, disclosures, or issuer-specific extensions outside the
small primary-statement taxonomy.

## Coverage Summary

The report should aggregate evidence at three levels:

1. filing: what happened in one filing;
2. company: how consistently a concept maps for one CIK;
3. coverage set: how broadly a mapping works across companies and years.

For every taxonomy metric, include at least:

- filings containing the metric;
- companies containing the metric;
- years covered;
- mapped and unmapped concept counts;
- units observed;
- dimensional and non-dimensional counts;
- duplicate and conflict counts;
- the concepts most often associated with the metric.

This distinguishes a mapping that works for one issuer from one that is useful
across the SP500 coverage set.

## Suggestion Evidence

The report may contain suggestions, but suggestions are evidence-backed review
items, not automatic edits. A suggestion should include:

- target metric key;
- candidate namespace and concept;
- deterministic suggestion identifier;
- companies and filings supporting it;
- years and statements where it occurs;
- observed labels, units, and dimensions;
- confidence: `low`, `medium`, or `high`;
- reasons for the confidence;
- conflicting evidence, if any.

The identifier should be derived from the taxonomy version, candidate concept,
metric key, and sorted evidence identifiers. Re-running a scan over unchanged
inputs must produce the same identifier and ordering.

## Tuner Workflow

The external tuner can consume the report in a small review loop:

```text
taxonomy-tuner scan --report validation-report.json
taxonomy-tuner review --report validation-report.json
taxonomy-tuner export --report validation-report.json \
  --out work/taxonomy-patch.json
```

The exported patch should contain only additions, removals, or explicit label
changes selected by a reviewer. Applying that patch remains a separate action,
followed by a new parser run and validation report.

The tuner should show the source filing, visible label, source concept, current
mapping, and nearby statement context together. This keeps a proposed change
inspectable rather than reducing it to a frequency count.

## Failure And Exit Status

Report generation should continue after a filing-level finding whenever the
remaining inputs can still be inspected. The command should return a non-zero
status for operational failures such as unreadable input, invalid JSON, or an
incomplete report.

Quality findings should be summarized separately from operational failures.
An invocation may therefore complete with `status: "review"` while still
producing a useful report. A strict CI mode can later promote selected finding
codes or severities to a non-zero exit status.

## Future Improvements

Later versions may add HTML statement evidence, presentation-linkbase context,
calculation relationships, accepted and rejected review decisions, and report
comparisons between taxonomy versions.

### Presentation And Calculation Evidence

Presentation relationships describe where concepts appear in a financial
statement. They can provide parent-child grouping, display order, statement
context, preferred labels, and nearby concepts. This helps distinguish a
primary-statement fact from a disclosure and gives the tuner useful context for
an unmapped concept.

Presentation relationships do not prove that concepts are interchangeable,
that a parent equals the sum of its children, or that a concept belongs in a
canonical metric. They are primarily a presentation structure.

Calculation relationships describe arithmetic relationships between concepts.
For example:

```text
GrossProfit
  Revenue                         +1
  CostOfRevenue                   -1
```

This suggests `GrossProfit = Revenue - CostOfRevenue`. Calculation weights can
also express sign conventions, so they must not be applied as blind value
transformations.

Calculation relationships can support checks for subtotals, duplicate-fact
selection, units, and signs. They are not complete accounting identities:
components may be omitted, dimensional facts may not be additive, and custom
concepts or incompatible contexts may be involved. A mismatch should therefore
be recorded as evidence, for example:

```text
identity: GrossProfit = Revenue - CostOfRevenue
result: mismatch
possible causes:
  - dimensional fact selected
  - sign convention differs
  - missing component
  - duplicate fact selection
```

The tuner should use these sources in sequence:

1. use presentation context to locate the relevant statement;
2. use calculation relationships to test numerical fit;
3. compare contexts, units, periods, dimensions, and signs;
4. rank the evidence for human review;
5. propose a taxonomy mapping only after that review.

Presentation and calculation relationships are evidence for review. They must
not automatically change reported values or the production taxonomy.
