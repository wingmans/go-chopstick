# Taxonomy Linter

The taxonomy linter checks the derived financial view and the verbose parsed
filing separately. It is diagnostic and read-only. It never changes raw SEC
data, parsed filings, or the taxonomy registry.

## Compact View

`taxonomy lint` reads `filing-view.json` and checks the data that the web
application consumes:

```text
edgar taxonomy lint --cik 789019 --form-type 10-K --year 2024
edgar taxonomy lint --file data/parsed/.../filing-view.json
```

It reports:

- canonical metrics that are mapped by the taxonomy;
- concepts that did not roll up into a canonical metric;
- rows without values;
- invalid period keys;
- canonical metrics appearing with multiple units.

Unmapped terms are findings rather than command failures. A filing can contain
valid facts that are outside the current dashboard model.

## Source Coverage

`taxonomy coverage` reads `filing.json` before compact projection:

```text
edgar taxonomy coverage --set us-gaap-coverage --form-type 10-K
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

The current implementation reports duplicates. It does not yet select a
canonical winner, which is safer until the rules are validated against more
companies.

### Unit normalization and sign handling

Facts may use different unit identifiers, scaling, or sign conventions. The
next layer should normalize units into a typed representation such as USD,
shares, or USD-per-share, preserve the original unit, and apply sign rules
only when they are explicitly defined. Reported values must never be silently
changed.

### Explicit dimensional facts

Dimensional facts describe segments, products, geographies, or other slices.
They should not compete with consolidated statement rows. The model should
retain their dimensions and expose them through a separate segment view or
exclude them explicitly from primary-statement selection.

### Accounting identity checks

Identity checks compare related reported facts without inventing replacements.
Examples include:

- assets versus liabilities plus equity;
- gross profit versus revenue minus cost of revenue;
- ending cash versus beginning cash plus cash-flow components.

Failures should be warnings with the involved concepts, periods, units, and
tolerance. They can reveal parser or taxonomy mistakes, but may also reflect
presentation choices, rounding, or restatements.

## Interpretation

High unmapped or dimensional counts are not automatically bad. They are
signals for taxonomy review. Industry-specific filings, especially banks,
insurers, REITs, and utilities, require separate concepts and should not be
forced into industrial-company ratios.
