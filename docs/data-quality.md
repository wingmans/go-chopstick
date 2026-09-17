# Data Quality Policy

This note records the compact filing-view policy for normalized units and sign
handling. Parsed filings remain source-level records; this policy applies when
building `filing-view.json`.

## Human Review Principle

Validation runs can nominate parser fixes, taxonomy aliases, data-quality
rules, malformed fixtures, and expected-value changes. They should not
automatically turn those nominations into permanent truth.

A human reviewer stays in the loop for every quality-improvement loop:

- approve new or changed fixtures before they become tests;
- approve taxonomy mappings before they affect canonical metrics or ratios;
- approve parser normalization rules before they accept older source formats;
- approve expected-value changes before they redefine golden behavior.

This keeps validation output useful as evidence without letting one noisy run
rewrite the project's definition of correct behavior.

## Deferred Coverage Profiles

Validation runs may eventually need company or industry profiles, for example
to mark generic industrial metrics as not applicable for banks, insurers, or
REITs. That layer is intentionally deferred for now.

The current quality loop should first use the simpler tools already in place:
taxonomy mappings, `core` versus `industry_sensitive` coverage tiers, parser
fixes, malformed fixtures, and human-readable validation summaries. Those
tools keep missing metrics visible without requiring the project to maintain a
second source of truth for each company's business model.

Profiles can be reconsidered after several golden-set iterations if
industry-specific noise still blocks useful review. If added later, profiles
should be explicit metadata rather than hard-coded CIK checks, and they should
only affect coverage interpretation. They must not change source facts, compact
statement values, taxonomy aliases, or ratios.

The Prologis/REIT investigation showed the trade-off clearly: a profile could
move absent industrial metrics such as gross profit, cost of revenue, current
assets/current liabilities, and capital expenditures out of the missing queue.
That made the summary cleaner, but introduced company classification work and
the risk of hiding real misses too early. For now, keep those metrics in the
`industry_sensitive` review lane instead of adding a profile layer.

## Unit Normalization

XBRL unit IDs such as `U_USD` are local to a filing and are not comparable
across companies or years. The compact view therefore normalizes known unit
definitions to stable semantic units:

- `USD` for monetary facts measured in U.S. dollars.
- `shares` for share counts.
- `USD/shares` for per-share facts.
- `pure` for ratios and unitless values.
- `unknown:<unitRef>` when a source unit reference cannot be resolved.

The normalized unit is written on each `FactSeries.unit`. The original source
unit reference is preserved on each `FactValue.unit_ref` so every displayed
value can still be traced back to the parsed source fact.

## Sign Handling

The parser and compact view preserve reported fact values. They do not flip a
source value because a concept looks like an expense, payment, contra-account,
or cash outflow.

Sign direction belongs in derived formulas and validation rules:

- Cost and expense facts remain as reported.
- Cash-flow facts remain as reported, including negative reported values.
- Derived metrics express direction explicitly, for example by subtracting a
  positive capital-expenditure payment when calculating free cash flow.
- Accounting identity checks should report mismatches instead of silently
  correcting signs.

This keeps source values auditable while still allowing product metrics to use
clear, reviewed sign conventions.

## Dimensional Facts

The compact summary and statement views intentionally use only default-context
facts. Facts with explicit or typed dimensions are preserved in `filing.json`,
but they are excluded from `filing-view.json` statement projection and counted
as `counts.dimensional_facts_excluded`.

This prevents segment, geography, product, customer, share-class, or other
breakdown facts from being mistaken for consolidated company totals. For
example, a cloud revenue fact scoped to a product axis must not replace the
company-level revenue fact in the income statement.

Future work can use dimensional facts once the product has a separate model and
UI for breakdowns. That model should keep the axis, member, typed dimension
content, period, normalized unit, and source references attached to every value.
It should present those facts as segment or analysis views, not as substitutes
for default-context statement rows.

## Accounting Identity Checks

Accounting identity checks run when `filing-view.json` is built. The same
checks are also evaluated by `taxonomy lint` so saved compact views and CLI
audits report the same warnings.

The checks compare already-selected compact statement rows. They never mutate
reported facts and never invent replacement values. A failed check is a
data-quality finding that can point to rounding, taxonomy mapping, duplicate
selection, sign policy, or source presentation issues.

Current checks:

- `assets = liabilities_and_equity`
- `assets = liabilities + equity_including_noncontrolling_interest`
- `assets = liabilities + equity`

Checks only compare facts for the same period and normalized unit. Values are
parsed exactly as rational numbers. Tolerance is derived from XBRL `decimals`:
each reported input contributes its maximum rounding drift, and the combined
drift is allowed before a warning is emitted.

The balance-sheet checks prefer a direct `liabilities_and_equity` total when
the filing provides one. In that case the broader
`assets = liabilities + equity_including_noncontrolling_interest` and
`assets = liabilities + equity` fallbacks are skipped for the same period and
unit. This avoids false warnings where a direct total reconciles but component
subtotals follow issuer-specific presentation.

`StockholdersEquityIncludingPortionAttributableToNoncontrollingInterest` and
`PartnersCapitalIncludingPortionAttributableToNoncontrollingInterest` are
therefore mapped to a separate supplemental
`equity_including_noncontrolling_interest` metric, not to plain `equity`. That
metric can support NCI-aware identity checks without quietly changing
debt-to-equity semantics or making NCI-inclusive equity a required coverage
metric.

Future work can add more identities after their sign conventions and source
evidence are reviewed, for example gross profit, cash reconciliation, or free
cash flow. Gross profit specifically should wait for calculation-link or
statement-presentation evidence because `revenue - cost_of_revenue` can differ
from reported gross profit for legitimate issuer presentation reasons. Those
formulas should stay explicit in code rather than being inferred from labels.
