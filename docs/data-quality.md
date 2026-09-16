# Data Quality Policy

This note records the compact filing-view policy for normalized units and sign
handling. Parsed filings remain source-level records; this policy applies when
building `filing-view.json`.

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

- `gross_profit = revenue - cost_of_revenue`
- `assets = liabilities + equity`

Checks only compare facts for the same period and normalized unit. Values are
parsed exactly as rational numbers. Tolerance is derived from XBRL `decimals`:
each reported input contributes its maximum rounding drift, and the combined
drift is allowed before a warning is emitted.

Future work can add more identities after their sign conventions are reviewed,
for example cash reconciliation or free cash flow. Those formulas should stay
explicit in code rather than being inferred from labels.
