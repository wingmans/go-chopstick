# Taxonomy Linter

The taxonomy linter checks the derived financial view and the verbose parsed
filing separately. It is diagnostic and read-only. It never changes raw SEC
data, parsed filings, or the taxonomy registry.

## Compact View

`taxonomy lint` reads `filing-view.json` and checks the data that the web
application consumes:

```text
edgar taxonomy lint --cik 789019 --form-type 10-K --year 2024
edgar taxonomy lint --set us-gaap-coverage --form-type 10-K \
  --from-year 2015 --format json \
  --out data/validation/runs/2026-09-16-taxonomy/lint-report.json
edgar taxonomy lint --file data/parsed/.../filing-view.json
```

It reports:

- canonical metrics that are mapped by the taxonomy;
- concepts that did not roll up into a canonical metric;
- rows without values;
- invalid period keys;
- canonical metrics appearing with multiple units;
- accounting identity failures, such as gross profit or balance-sheet
  equation mismatches.

Unmapped terms are findings rather than command failures. A filing can contain
valid facts that are outside the current dashboard model.

When `--file` is omitted, the command walks the parsed directory and scans all
matching `filing-view.json` files. The `--from-year` and `--to-year` filters
are intended for validation-set runs where a full filing history is useful,
for example the 19-company `us-gaap-coverage` set.

`--format json` writes a batch report with selection metadata, summary counts,
and one lint report per filing. Review findings such as unmapped concepts or
identity-check warnings remain successful command output; malformed or
unreadable input files are operational failures and produce a non-zero exit.

## Source Coverage

`taxonomy coverage` reads `filing.json` before compact projection:

```text
edgar taxonomy coverage --set us-gaap-coverage --form-type 10-K
edgar taxonomy coverage --set us-gaap-coverage --form-type 10-K \
  --from-year 2015 --format json \
  --out data/validation/runs/2026-09-16-taxonomy/taxonomy-coverage.json
edgar taxonomy coverage --file data/parsed/.../filing.json
```

It reports the complete source fact population, including concepts discarded
from the compact view. The report separates unmapped concepts and probable
company extensions, and counts dimensional facts, missing contexts, invalid
periods, missing units, and duplicate facts.

## Data-Quality Follow-Up

### Deterministic duplicate-fact selection

The same concept can occur more than once for a filing period because XBRL
contains multiple contexts, dimensions, units, or restated values. Selection
must use explicit rules, such as:

1. prefer non-dimensional consolidated contexts for primary statements;
2. require matching period type and unit;
3. prefer the appropriate filing document and canonical alias priority;
4. reject conflicting values instead of silently overwriting them.

The compact view now selects a deterministic winner for statement projection.
Coverage reports still expose duplicate source candidates for review.

### Unit normalization and sign handling

Facts may use different unit identifiers, scaling, or sign conventions. The
compact view normalizes common units into stable names such as `USD`, `shares`,
`pure`, and `USD/shares`, while preserving the original unit reference on each
value. Sign rules are only applied in explicit derived formulas or identity
checks. Reported values must never be silently changed.

### Explicit dimensional facts

Dimensional facts describe segments, products, geographies, or other slices.
They do not compete with consolidated statement rows. The parsed filing retains
their dimensions, while the compact view excludes them from primary-statement
selection and reports `counts.dimensional_facts_excluded`.

### Accounting identity checks

Identity checks compare related reported facts without inventing replacements.
Examples include:

- assets versus liabilities plus equity;
- gross profit versus revenue minus cost of revenue;
- ending cash versus beginning cash plus cash-flow components.

Identity checks now run during compact-view build and taxonomy lint. Failures
are warnings with the involved metrics, periods, units, actual value, expected
value, and tolerance. They can reveal parser or taxonomy mistakes, but may also
reflect presentation choices, rounding, or restatements.

## Interpretation

High unmapped or dimensional counts are not automatically bad. They are
signals for taxonomy review. Industry-specific filings, especially banks,
insurers, REITs, and utilities, require separate concepts and should not be
forced into industrial-company ratios.
