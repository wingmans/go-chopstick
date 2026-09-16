# Remaining Work

This is the current roadmap after the first end-to-end and taxonomy passes.

## Data Quality

- Implement deterministic duplicate-fact selection.
- Normalize units and define sign handling.
- Preserve or explicitly exclude dimensional facts.
- Add accounting identity checks.
- Expand and review the taxonomy across the 19-company validation set.
- Add stronger golden expected-value fixtures.

## Legacy XBRL Compatibility

- Add a safe parser-only normalization copy for legacy malformed XBRL
  documents.
- Keep the pristine raw SEC filing unchanged.
- Retain the original bytes, normalization rules, and diagnostics for
  lineage and reproducibility.
- Add focused fixtures before enabling normalization broadly.

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
