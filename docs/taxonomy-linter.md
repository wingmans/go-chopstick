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

Validation runs reuse the local index and filing caches by default. To refresh
only the newest SEC index quarter, opt in explicitly:

```bash
REFRESH_LATEST=1 make taxonomy-validation
```

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
- Missing `eps_diluted` may keep supplemental `eps_basic` as related evidence.
  Basic EPS is not substituted for diluted EPS; it only shows that the filing
  has per-share earnings data while the diluted concept remains absent.
- `missing_metric_dimensional_evidence` records source-level hints that were
  excluded from the compact filing view because they use dimensional contexts.
  The first supported case is missing `eps_diluted` with source
  `EarningsPerShareBasic` facts reported by share class. This evidence reduces
  hard-gap noise in validation reports, but it does not make the metric present.
  Future work may explicitly promote selected dimensional facts when the axis,
  member, period, and unit handling rules are clear enough to preserve meaning.

The `industry_sensitive` tier keeps banks, insurers, REITs, and other
specialized filers from looking worse than they are when a generic industrial
metric such as gross profit, cost of revenue, current assets/current
liabilities, operating income, or capital expenditures is absent. The tier does
not hide those gaps; it keeps them in a separate review lane.

### Current hard-gap notes

Run `data/validation/runs/20260917T181634Z` reduced unexplained core gaps to
the small set below. These are intentionally left as hard gaps rather than
taxonomy aliases until fresh evidence justifies a safer rule:

- Caterpillar 2015-2018 is missing compact `cash`. Source facts include
  restricted-cash and cash-flow concepts, but no obvious consolidated
  `cash` equivalent that should be mapped to the dashboard metric.
- Linde 2018 is missing compact `revenue` and `investing_cash_flow`. The filing
  is sparse, with only 133 source facts, and appears to be a special
  pre-combination or transitional filing rather than a normal operating-year
  taxonomy miss.

Future validation runs should treat this list as review context, not as a
permanent suppression list. If later filings or manual source review show a
clear consolidated concept, add the alias with a focused before/after run.

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

## Taxonomy Tuning Scope And Fast Loop

The immediate product goal is reliable consolidated, company-level aggregates:
revenue, profit, assets, liabilities, equity, cash, cash flow, per-share
values, and the inputs needed by the current ratios. Taxonomy work should be
prioritized by whether an unmapped concept can change one of those outputs.

The 132,484 source-level findings in the 2026-09-18 validation run are not
132,484 defects. They are finding groups across 685,898 source facts, including
476,419 dimensional facts. Many describe segments, geographies, products,
share classes, debt terms, fair-value tables, or other disclosure detail. They
matter for future analysis, but mapping them into the current compact company
view would risk treating a slice or disclosure component as a consolidated
total.

For the current phase:

1. Prioritize unmapped concepts that repeatedly block a core compact metric or
   cause an accounting identity or ratio failure.
2. Review high-frequency concepts only after checking their contexts, units,
   periods, and statement role. Frequency alone is not evidence that a mapping
   is correct.
3. Keep dimensional facts in the source report, but exclude them from the
   consolidated mapping queue unless a future dimension-aware feature explicitly
   requests them.
4. Keep industry-sensitive metrics in a separate review lane. Do not add
   company profiles merely to make the headline counts look cleaner.
5. Stop mapping when the candidate no longer changes a top-level output or
   removes a real quality failure. The goal is useful coverage, not an empty
   unmapped-concept list.

The 19-company set is sufficient for the first automatic regression gate. It
contains 222 annual filings from 2015 onward across technology, financials,
insurance, healthcare, energy, consumer, industrial, materials, real estate,
and utility issuers. It is broad enough to expose cross-industry regressions,
but it is not proof of universal XBRL compatibility. A future holdout set of
companies not used during tuning should provide that check.

The rapid taxonomy iteration loop should be:

1. Select one canonical metric or one small related cluster, such as
   liabilities and equity.
2. Query the detailed JSON report for the highest-frequency candidate concepts
   affecting that metric.
3. Inspect a few source facts from different companies and years, including
   context, dimensions, unit, period, and sign.
4. Make one small, human-approved taxonomy change.
5. Re-run the fast validation against existing parsed data: source taxonomy
   coverage plus compact taxonomy lint. Do not download or reparse filings for
   a taxonomy-only change.
6. Compare core metric presence, quality issues, identity checks, and the
   candidate concept counts before and after.
7. Run the full parse and download-backed validation only after several small
   changes have accumulated or parser behavior changed.

The planned automatic result should distinguish operational status from
taxonomy progress. A run is red for missing/unparsed filings, command errors,
or compact quality failures. It is review-needed when core metric gaps or
taxonomy findings remain. It is green only when the operational gate passes
and the configured core-gap and quality thresholds pass. Source-level
unmapped volume alone should not make the run red.

The validation script now reports this gate in `summary.md` and on stdout:

- `RED`: processing, coverage, taxonomy-command, or compact-quality failure;
- `REVIEW`: operational checks pass but unexplained hard core metric gaps remain;
- `GREEN`: operational checks pass and no unexplained hard core gaps remain.

Related-evidence gaps and dimensional-EPS gaps remain visible in the coverage
reports, but do not block `GREEN` because they are explicitly explained and do
not represent a safe consolidated mapping failure.

### Human-reviewed hard-gap baseline

The validation gate keeps hard core gaps visible while allowing a human to
approve narrow, filing-specific exceptions. The canonical baseline is
`golden/taxonomy-validation-exceptions.json`. It currently contains six
entries: four Caterpillar `cash` near-matches and two Linde 2018 unavailable
metrics (`revenue` and `investing_cash_flow`).

An exception matches only the exact `cik`, `accession`, and `metric`. It records
the reason and source evidence, but never creates a value or changes a filing
view. Accepted exceptions are reported separately from unexplained gaps. A run
is `GREEN` only when operational checks pass and the number of unexplained hard
gaps is zero.

The human review process is deliberate:

1. Run validation and inspect each hard gap in the raw filing and parsed view.
2. Add only confirmed, filing-specific exceptions to the JSON baseline.
3. Commit the baseline with the code change and review rationale.
4. Re-run validation and confirm that accepted exceptions remain visible and
   no new unexplained gaps are hidden.

Do not add broad issuer, metric, year, or industry wildcards. Do not use the
baseline to turn a broader concept into a narrower metric. For example,
Caterpillar's `CashCashEquivalentsAndShortTermInvestments` is accepted as a
reviewed near-match, not silently mapped to pure cash. Future work may add a
separate cash-and-short-term-investments metric.

The fast default loop is available as `make taxonomy-validation-fast`. It uses
the five-company `golden` set, one 2024 10-K year, and local caches. The full
19-company pass remains `make taxonomy-validation`.

Berkshire's diluted-EPS gap is currently a valid `REVIEW` result: the 2024
filing has basic EPS facts scoped to share-class dimensions and no consolidated
diluted-EPS fact. It should not be resolved by selecting one class or mapping
basic EPS to diluted EPS.

### Derived liabilities decision

Some filings report a consolidated liabilities-and-equity total and an equity
total without a standalone liabilities fact. The compact view may derive
liabilities as `liabilities_and_equity - equity` only when the selected facts
share the same period, instant context, and normalized unit. Plain equity is
preferred; NCI-inclusive equity is used only when plain equity is absent. The
derived row is marked with a `derived` namespace and does not alter the raw
filing or source taxonomy report.

This does not make `LiabilitiesAndStockholdersEquity` a taxonomy alias for
`liabilities`, and it does not promote dimensional EPS to diluted EPS. When
plain equity is absent, the compact view now exposes the NCI-inclusive value as
an explicitly derived fallback for the main equity row while retaining the
original supplemental row. Ratios using the main equity row therefore use the
reported total available for that filing; readers that require attributable-to-
parent equity must use the supplemental/source evidence.
