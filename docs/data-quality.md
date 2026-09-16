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
