# `filing-view.json`

`filing-view.json` is the compact, derived read model used by the local web
dashboard. It is stored beside the verbose parsed filing:

```text
data/parsed/{10-digit-cik}/{accession}/
  filing.json
  filing-view.json
```

`filing.json` remains the source-level parser result. `filing-view.json` can
be rebuilt at any time and must never be treated as a replacement for the raw
SEC submission or the verbose parsed result.

## Top-Level Fields

| Field | Purpose |
| --- | --- |
| `schema_version` | Version of the compact view format. |
| `parser_version` | Parser version that produced the view. |
| `taxonomy_version` | Taxonomy registry version used for the projection. |
| `source_path` | Local source reference retained for lineage. |
| `source_sha256` | Checksum of the original submission. |
| `metadata` | Filing identity, dates, CIK, form, and company. |
| `documents` | Lightweight inventory of filing documents. |
| `summary` | Selected financial facts grouped by period type. |
| `statements` | Compact income, balance-sheet, and cash-flow sections. |
| `ratios` | Derived ratios with their formulas and source periods. |
| `quality` | Data-quality findings produced during compact-view build. |
| `counts` | Document, instance, fact, context, and projection-exclusion counts. |
| `diagnostics` | Parser diagnostics retained for the UI and debugging. |

## Summary

`summary` currently contains groups such as:

```json
{
  "title": "Fiscal year",
  "periods": ["FY2026", "FY2025", "FY2024"],
  "rows": [
    {
      "label": "Revenue",
      "concept": "RevenueFromContractWithCustomerExcludingAssessedTax",
      "unit": "USD",
      "values": {
        "FY2026": {
          "value": "331839000000",
          "nil": false,
          "unit_ref": "U_USD"
        }
      }
    }
  ]
}
```

Values remain strings. This avoids floating-point loss and preserves the
source value exactly. The concept and normalized unit remain attached for
comparison, while each value retains the source unit reference so later
taxonomy work can be audited back to the original XBRL fact.

## Target Shape

The view now contains explicit sections for:

- income statement;
- balance sheet;
- cash flow;
- key ratios derived from those statements.

Each statement contains the same period groups used by the summary, such as
`Fiscal year`, `Quarterly`, `Year to date`, and `Instant`. Each row retains its
source concept and normalized unit. Each value also retains its context
reference, source unit reference, and decimals so displayed values can be
traced back to the verbose parsed filing.

Dimensional facts are excluded from summary and statement projection. They
remain available in `filing.json`, and the compact view records their count in
`counts.dimensional_facts_excluded`.

Ratios are explicitly derived values. They are only emitted when both source
values exist for the same period and can be parsed exactly. Examples include
gross margin, operating margin, net margin, current ratio, and liabilities to
equity. They are not XBRL facts and must not be mistaken for reported values.

`quality.identity_checks` records accounting identity checks evaluated during
the compact-view build. These findings currently focus on balance-sheet
identities, using normalized units and rounding tolerances from source
decimals. They are warnings only and do not change reported values.

## Taxonomy

The taxonomy needs improvement before the pages can be broadly comparable,
but it does not need to become a complete US-GAAP taxonomy now. A small,
manually editable concept registry is sufficient for the next step. It should
map common concepts to stable product names such as `revenue`, `net_income`,
`assets`, and `operating_cash_flow`, while retaining the original concept.

The registry must allow aliases. For example, revenue may appear as
`Revenues`, `SalesRevenueNet`, or
`RevenueFromContractWithCustomerExcludingAssessedTax`. The raw concept remains
the evidence; the product name is the comparison key.

## Validation Dataset

The existing golden Microsoft filing contains the annual values needed to
validate the first statement views. The end-to-end script loads the index from
2010 onward, processes the five-member golden set for 2026, and loads
historical 10-K data only for Microsoft. More Microsoft filings should be
validated before broadening the dataset to other companies. This gives us a
controlled way to detect period selection, concept mapping, sign, unit, and
restatement errors before introducing DuckDB or a larger storage layer.
The coverage package also keeps a small
`internal/coverage/testdata/golden-expected-coverage.json` fixture for expected
metric-presence behavior across clean, dimensional-evidence, and known hard-gap
cases.
