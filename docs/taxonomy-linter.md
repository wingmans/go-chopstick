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
edgar taxonomy lint --set us-gaap-coverage --form-type 10-K \
  --from-year 2015 --format text \
  --out data/validation/runs/2026-09-16-taxonomy/lint-report.txt
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

The validation script writes detailed JSON artifacts and separate
human-readable text summaries. The text files intentionally aggregate the JSON
instead of repeating every filing row:

- `coverage-report.txt` summarizes expected filing coverage, missing core
  metrics, industry-sensitive gaps, and filing hotspots.
- `taxonomy-coverage.txt` summarizes source-level unmapped concepts, company
  extension concepts, and high-noise filings.
- `lint-report.txt` summarizes compact-view quality issues and filings that
  need review.

Use the linked JSON files when a summary item needs line-item detail.
For identity-check failures, JSON keeps both backward-compatible
`quality_issues` strings and structured `quality_checks` entries with actual,
expected, tolerance, period, unit, involved metrics, and the selected source
evidence for each formula input. Evidence records include metric, role, sign,
concept, context reference, reported value, decimals, and unit reference so
reviewers can distinguish taxonomy mistakes from context/period selection or
issuer-specific presentation.
Formula checks only compare facts selected for the same period, unit, and
source context. When the compact rows share a display period but come from
different XBRL contexts, the check is skipped instead of reported as a quality
issue.

The expected filing coverage report also contains per-filing metric presence.
Useful review one-liners are:

```bash
jq -r '.filings[] | .missing_metrics[]?' \
  data/validation/runs/<run-id>/coverage-report.json |
  sort | uniq -c | sort -nr
```

```bash
jq -r '.filings[] | .metrics | to_entries[] | [.key,.value] | @tsv' \
  data/validation/runs/<run-id>/coverage-report.json |
  awk -F '\t' '$2=="present"{present[$1]++} $2=="missing"{missing[$1]++}
    END {for (m in present) print m, present[m]+0, missing[m]+0}'
```

Missing metric output is intentionally split two ways:

- `missing_metrics` keeps the full backward-compatible list.
- `missing_metrics_by_tier` groups absent metrics as `core`,
  `industry_sensitive`, or `supplemental`.
- `filings_with_missing_metrics_by_tier` in the summary counts how many
  filings had at least one missing metric in each tier.
- `missing_metric_related_evidence` records nearby canonical metrics that were
  present when a metric is missing. For example, missing `liabilities` may keep
  `liabilities_and_equity` or `current_liabilities` as review evidence. This
  does not make the metric present; it separates hard gaps from gaps that may
  be covered by a related presentation.

The `industry_sensitive` tier keeps banks, insurers, REITs, and other
specialized filers from looking worse than they are when a generic industrial
metric such as gross profit, cost of revenue, current assets/current
liabilities, operating income, or capital expenditures is absent. The tier does
not hide those gaps; it keeps them in a separate review lane.

## Source Coverage

`taxonomy coverage` reads `filing.json` before compact projection:

```text
edgar taxonomy coverage --set us-gaap-coverage --form-type 10-K
edgar taxonomy coverage --set us-gaap-coverage --form-type 10-K \
  --from-year 2015 --format json \
  --out data/validation/runs/2026-09-16-taxonomy/taxonomy-coverage.json
edgar taxonomy coverage --set us-gaap-coverage --form-type 10-K \
  --from-year 2015 --format text \
  --out data/validation/runs/2026-09-16-taxonomy/taxonomy-coverage.txt
edgar taxonomy coverage --file data/parsed/.../filing.json
```

It reports the complete source fact population, including concepts discarded
from the compact view. The report separates unmapped concepts and probable
company extensions, and counts dimensional facts, missing contexts, invalid
periods, missing units, and duplicate facts.

JSON coverage reports keep the complete unmapped and extension lists. Text
coverage reports are capped to the highest-count unmapped concepts and
extensions per filing so the human-readable artifact stays reviewable. Use the
JSON artifact for exhaustive tuning queries.

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
- ending cash versus beginning cash plus cash-flow components.

Identity checks now run during compact-view build and taxonomy lint. Failures
are warnings with the involved metrics, periods, units, actual value, expected
value, and tolerance. They can reveal parser or taxonomy mistakes, but may also
reflect presentation choices, rounding, or restatements.

Balance-sheet checks first try `assets = liabilities_and_equity` when a filing
reports that direct total. The fallback `assets = liabilities + equity` is
skipped for that period and unit when the direct total exists, because plain
shareholders' equity may exclude noncontrolling interest. Do not map
`StockholdersEquityIncludingPortionAttributableToNoncontrollingInterest` to
plain `equity` unless the downstream ratios and labels are also made
noncontrolling-interest-aware.

Gross-profit identity checks are intentionally not active yet. Golden-set
evidence showed that `revenue - cost_of_revenue` can disagree with reported
gross profit for legitimate issuer presentation reasons, such as revenue that
includes financing, membership, service, or other amounts outside the gross
profit subtotal. Bring this check back only when calculation-link or statement
presentation evidence proves the selected facts are intended to reconcile.

## Interpretation

High unmapped or dimensional counts are not automatically bad. They are
signals for taxonomy review. Industry-specific filings, especially banks,
insurers, REITs, and utilities, require separate concepts and should not be
forced into industrial-company ratios.
