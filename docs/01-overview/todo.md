# Remaining Work

This is the current roadmap after the first end-to-end and taxonomy passes.

## Data Quality

- [x] Implement deterministic duplicate-fact selection.
- [x] Normalize units and define sign handling.
- [x] Preserve or explicitly exclude dimensional facts.
- [x] Add accounting identity checks.
- [x] Expand and review the taxonomy across the 19-company validation set
  (core gaps reviewed; accepted limitations documented).
- [x] Add stronger golden expected-value fixtures.

## Legacy XBRL Compatibility

- [x] Add a safe parser-only normalization copy for legacy malformed XBRL
  documents.
- Keep the pristine raw SEC filing unchanged.
- Retain the original bytes, normalization rules, and diagnostics for
  lineage and reproducibility.
- [x] Add focused fixtures before enabling normalization broadly.

Near-future validation: after this compatibility path and its tests are
committed, do a clean full run from SEC download through parsing. That rerun is
useful for finding remaining legacy parser gaps, but doing it before the patch
lands would mix parser changes with data churn.

## Product Validation

- Perform manual validation across companies and filing years.
- Improve detail-page formatting and missing-value presentation.
- Add stronger expected-set validation to the E2E script.

## Taxonomy Maintenance

- Add structured JSON output to the taxonomy linter and coverage reports.
- Build the external taxonomy-tuner scan and review workflow.
- Use annual-report HTML as supporting evidence for candidate mappings.
- Emit deterministic, reviewable taxonomy patches without editing `edgar`.
- Review high-level concepts across the `us-gaap-coverage` set.
- Keep human approval mandatory for taxonomy changes.

## Storage And Deferred Features

- Keep JSON storage while query requirements evolve.
- Introduce DuckDB or another store only after taxonomy tuning and the
  required queries are understood.
- Improve index validation and checksum reporting.
- Consider a primary-document viewer later.
- Keep automatic taxonomy changes disabled; generate reviewable suggestions
  instead.
