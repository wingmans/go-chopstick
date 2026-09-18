#!/usr/bin/env bash

set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLI_PATH="${CLI_PATH:-$ROOT_DIR/bin/edgar}"
SET_NAME="${SET_NAME:-us-gaap-coverage}"
FORM_TYPE="${FORM_TYPE:-10-K}"
FROM_YEAR="${FROM_YEAR:-2015}"
TO_YEAR="${TO_YEAR:-}"
REFRESH_LATEST="${REFRESH_LATEST:-0}"
RUN_ID="${RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
RUN_DIR="${RUN_DIR:-$ROOT_DIR/data/validation/runs/$RUN_ID}"
RUN_LOG="$RUN_DIR/run.log"
SOFT_FAILURES=()

cd "$ROOT_DIR"
export LOGLEVEL="${LOGLEVEL:-debug}"

if [[ ! -x "$CLI_PATH" ]]; then
  echo "error: $CLI_PATH is missing; run 'make build' first" >&2
  exit 1
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "error: jq is required to build human-readable validation summaries" >&2
  exit 1
fi

mkdir -p data/sets "$RUN_DIR"
cp "golden/$SET_NAME.json" "data/sets/$SET_NAME.json"
cp "golden/$SET_NAME.json" "$RUN_DIR/constituent-set.json"
cp internal/filingview/taxonomy.json "$RUN_DIR/taxonomy.json"
: > "$RUN_LOG"

cat > "$RUN_DIR/summary.md" <<MARKDOWN
# Taxonomy Validation Run

- Run ID: \`$RUN_ID\`
- Set: \`$SET_NAME\`
- Form type: \`$FORM_TYPE\`
- Year filter: \`$FROM_YEAR\` through \`${TO_YEAR:-latest}\`
- Status: started

## Artifacts

- \`constituent-set.json\`
- \`taxonomy.json\`
- \`run.log\`

Report artifacts are added after the download, parse, and validation steps
complete. JSON files are for tooling; text files are summary views for quick
review.
MARKDOWN

YEAR_ARGS=(--from-year "$FROM_YEAR")
if [[ -n "$TO_YEAR" ]]; then
  YEAR_ARGS+=(--to-year "$TO_YEAR")
fi

INDEX_REFRESH_ARGS=()
if [[ "$REFRESH_LATEST" == "1" ]]; then
  INDEX_REFRESH_ARGS+=(--refresh-latest)
fi

# Run a step and append both the command and output to the run log.
run_step() {
  local label="$1"
  shift

  {
    printf '\n== %s ==\n' "$label"
    printf '+'
    printf ' %q' "$@"
    printf '\n'
  } | tee -a "$RUN_LOG"

  "$@" 2>&1 | tee -a "$RUN_LOG"
}

run_step_allow_failure() {
  local label="$1"
  local status
  shift

  {
    printf '\n== %s ==\n' "$label"
    printf '+'
    printf ' %q' "$@"
    printf '\n'
  } | tee -a "$RUN_LOG"

  set +e
  "$@" 2>&1 | tee -a "$RUN_LOG"
  status="${PIPESTATUS[0]}"
  set -e

  if [[ "$status" -ne 0 ]]; then
    SOFT_FAILURES+=("$label exited with status $status")
    printf 'warning: %s exited with status %s; continuing to validation reports\n' \
      "$label" "$status" | tee -a "$RUN_LOG"
  fi
}

append_jq_lines() {
  local fallback="$1"
  local file="$2"
  local filter="$3"
  local output

  output="$(jq -r "$filter" "$file")"
  if [[ -n "$output" ]]; then
    printf '%s\n' "$output"
  else
    printf '%s\n' "$fallback"
  fi
}

write_coverage_text_summary() {
  local path="$RUN_DIR/coverage-report.txt"

  {
    printf '# Expected Filing Coverage Summary\n\n'
    printf 'Detailed JSON: `coverage-report.json`\n\n'
    printf '## Totals\n\n'
    jq -r '
      .summary |
      "- expected: \(.expected)\n" +
      "- downloaded: \(.downloaded)\n" +
      "- parsed: \(.parsed)\n" +
      "- missing: \(.missing)\n" +
      "- unparsed: \(.unparsed)\n" +
      "- filings with missing metrics: \(.filings_with_missing_metrics)\n" +
      "- filings with missing core metrics: \(.filings_with_missing_metrics_by_tier.core // 0)\n" +
      "- filings with missing industry-sensitive metrics: \(.filings_with_missing_metrics_by_tier.industry_sensitive // 0)"
    ' "$RUN_DIR/coverage-report.json"

    printf '\n## Top Missing Core Metrics\n\n'
    append_jq_lines '_None._' "$RUN_DIR/coverage-report.json" '
      [.filings[] | .missing_metrics_by_tier.core[]?] |
      group_by(.) |
      map({metric: .[0], count: length}) |
      sort_by(-.count, .metric) |
      .[:12][] |
      "- \(.count) \(.metric)"
    '

    printf '\n## Core Missing Metrics With Related Evidence\n\n'
    append_jq_lines '_None._' "$RUN_DIR/coverage-report.json" '
      [.filings[] |
        (.missing_metric_related_evidence // {}) |
        to_entries[] |
        .key as $metric |
        .value[]? |
        {metric: $metric, related: .}
      ] |
      group_by(.metric + "\u0000" + .related) |
      map({metric: .[0].metric, related: .[0].related, count: length}) |
      sort_by(-.count, .metric, .related) |
      .[:12][] |
      "- \(.count) \(.metric) with \(.related)"
    '

    printf '\n## Core Missing Metrics With Dimensional Evidence\n\n'
    append_jq_lines '_None._' "$RUN_DIR/coverage-report.json" '
      [.filings[] |
        (.missing_metric_dimensional_evidence // {}) |
        to_entries[] |
        .key as $metric |
        .value[]? |
        {metric: $metric, related: .}
      ] |
      group_by(.metric + "\u0000" + .related) |
      map({metric: .[0].metric, related: .[0].related, count: length}) |
      sort_by(-.count, .metric, .related) |
      .[:12][] |
      "- \(.count) \(.metric) with dimensional \(.related)"
    '

    printf '\n## Top Hard Missing Core Metrics\n\n'
    append_jq_lines '_None._' "$RUN_DIR/coverage-report.json" '
      [.filings[] |
        . as $filing |
        .missing_metrics_by_tier.core[]? |
        select((($filing.missing_metric_related_evidence // {})[.] // []) | length == 0) |
        select((($filing.missing_metric_dimensional_evidence // {})[.] // []) | length == 0)
      ] |
      group_by(.) |
      map({metric: .[0], count: length}) |
      sort_by(-.count, .metric) |
      .[:12][] |
      "- \(.count) \(.metric)"
    '

    printf '\n## Top Industry-Sensitive Gaps\n\n'
    append_jq_lines '_None._' "$RUN_DIR/coverage-report.json" '
      [.filings[] | .missing_metrics_by_tier.industry_sensitive[]?] |
      group_by(.) |
      map({metric: .[0], count: length}) |
      sort_by(-.count, .metric) |
      .[:12][] |
      "- \(.count) \(.metric)"
    '

    printf '\n## Core Missing Hotspots\n\n'
    append_jq_lines '_None._' "$RUN_DIR/coverage-report.json" '
      [.filings[] |
        select((.missing_metrics_by_tier.core // []) | length > 0) |
        {
          cik,
          company,
          metrics: (.missing_metrics_by_tier.core | join(", ")),
          count: (.missing_metrics_by_tier.core | length)
        }
      ] |
      group_by(.cik + "\u0000" + .company + "\u0000" + .metrics) |
      map({cik: .[0].cik, company: .[0].company, metrics: .[0].metrics, filings: length, count: .[0].count}) |
      sort_by(-.filings, -.count, .cik, .metrics) |
      .[:20][] |
      "- \(.filings) filing(s) \(.cik) \(.company): \(.metrics)"
    '
  } > "$path"
}

write_taxonomy_coverage_text_summary() {
  local path="$RUN_DIR/taxonomy-coverage.txt"

  {
    printf '# Source Taxonomy Coverage Summary\n\n'
    printf 'Detailed JSON: `taxonomy-coverage.json`\n\n'
    printf '## Totals\n\n'
    jq -r '
      .summary as $s |
      ([.filings[].coverage.facts] | add // 0) as $facts |
      ([.filings[].coverage.mapped] | add // 0) as $mapped |
      ([.filings[].coverage.dimensional] | add // 0) as $dimensional |
      ([.filings[].coverage.duplicate_facts] | add // 0) as $duplicates |
      "- selected: \($s.selected)\n" +
      "- failed: \($s.failed)\n" +
      "- finding groups: \($s.findings)\n" +
      "- source facts: \($facts)\n" +
      "- mapped source facts: \($mapped)\n" +
      "- dimensional facts: \($dimensional)\n" +
      "- duplicate fact candidates: \($duplicates)"
    ' "$RUN_DIR/taxonomy-coverage.json"

    printf '\n## Top Unmapped Concepts\n\n'
    append_jq_lines '_None._' "$RUN_DIR/taxonomy-coverage.json" '
      [.filings[] | .coverage.unmapped[]? | {concept, count}] |
      group_by(.concept) |
      map({concept: .[0].concept, count: (map(.count) | add)}) |
      sort_by(-.count, .concept) |
      .[:25][] |
      "- \(.count) \(.concept)"
    '

    printf '\n## Top Company Extension Concepts\n\n'
    append_jq_lines '_None._' "$RUN_DIR/taxonomy-coverage.json" '
      [.filings[] | .coverage.extensions[]? | {concept, count}] |
      group_by(.concept) |
      map({concept: .[0].concept, count: (map(.count) | add)}) |
      sort_by(-.count, .concept) |
      .[:25][] |
      "- \(.count) \(.concept)"
    '

    printf '\n## Filing Hotspots By Unmapped Groups\n\n'
    append_jq_lines '_None._' "$RUN_DIR/taxonomy-coverage.json" '
      [.filings[] |
        {
          cik,
          accession,
          filing_date,
          unmapped: (.coverage.unmapped | length),
          extensions: (.coverage.extensions | length),
          dimensional: .coverage.dimensional,
          facts: .coverage.facts
        }
      ] |
      sort_by(-.unmapped, -.extensions, .cik, .accession) |
      .[:20][] |
      "- \(.unmapped) unmapped, \(.extensions) extension groups; \(.cik) \(.accession) \(.filing_date)"
    '
  } > "$path"
}

write_lint_text_summary() {
  local path="$RUN_DIR/lint-report.txt"

  {
    printf '# Compact Taxonomy Lint Summary\n\n'
    printf 'Detailed JSON: `lint-report.json`\n\n'
    printf '## Totals\n\n'
    jq -r '
      .summary |
      "- selected: \(.selected)\n" +
      "- failed: \(.failed)\n" +
      "- unmapped findings: \(.findings)\n" +
      "- quality issues: \(.quality_issues // 0)"
    ' "$RUN_DIR/lint-report.json"

    printf '\n## Quality Issue Formulas\n\n'
    append_jq_lines '_None._' "$RUN_DIR/lint-report.json" '
      [.filings[] | .lint.quality_checks[]? | {name}] |
      group_by(.name) |
      map({name: .[0].name, count: length}) |
      sort_by(-.count, .name) |
      .[:12][] |
      "- \(.count) \(.name)"
    '

    printf '\n## Largest Quality Deltas\n\n'
    append_jq_lines '_None._' "$RUN_DIR/lint-report.json" '
      [.filings[] | . as $f | .lint.quality_checks[]? |
        {
          cik: $f.cik,
          accession: $f.accession,
          filing_date: $f.filing_date,
          name,
          period,
          unit,
          actual,
          expected,
          tolerance,
          delta: (((.actual | tonumber) - (.expected | tonumber)) | fabs)
        }
      ] |
      sort_by(-.delta, .cik, .accession, .period) |
      .[:20][] |
      "- delta=\(.delta) actual=\(.actual) expected=\(.expected) tolerance=\(.tolerance) \(.name) \(.period) \(.unit); \(.cik) \(.accession) \(.filing_date)"
    '

    printf '\n## Quality Evidence For Largest Deltas\n\n'
    append_jq_lines '_None._' "$RUN_DIR/lint-report.json" '
      [.filings[] | . as $f | .lint.quality_checks[]? |
        {
          cik: $f.cik,
          accession: $f.accession,
          filing_date: $f.filing_date,
          name,
          period,
          unit,
          delta: (((.actual | tonumber) - (.expected | tonumber)) | fabs),
          evidence: ((.evidence // []) |
            map(
              .metric + "=" + (.value // "") +
              " concept=" + (.concept // "") +
              " ctx=" + (.context_ref // "") +
              " sign=" + ((.sign // 0) | tostring)
            ) |
            join("; ")
          )
        }
      ] |
      sort_by(-.delta, .cik, .accession, .period) |
      .[:12][] |
      "- \(.cik) \(.accession) \(.period) \(.name): \(.evidence)"
    '

    printf '\n## Remaining Quality Issues\n\n'
    append_jq_lines '_None._' "$RUN_DIR/lint-report.json" '
      [.filings[] | . as $f | ($f.lint.quality_issues // [])[] | {issue: .}] |
      group_by(.issue) |
      map({issue: .[0].issue, count: length}) |
      sort_by(-.count, .issue) |
      .[:25][] |
      "- \(.count) \(.issue)"
    '

    printf '\n## Filings With Quality Issues\n\n'
    append_jq_lines '_None._' "$RUN_DIR/lint-report.json" '
      [.filings[] |
        select((.lint.quality_issues // []) | length > 0) |
        {
          cik,
          accession,
          filing_date,
          count: (.lint.quality_issues | length)
        }
      ] |
      sort_by(-.count, .cik, .accession) |
      .[:25][] |
      "- \(.count) issue(s); \(.cik) \(.accession) \(.filing_date)"
    '

    printf '\n## Compact Unmapped Concepts\n\n'
    append_jq_lines '_None._' "$RUN_DIR/lint-report.json" '
      [.filings[] | .lint.unmapped[]? | {concept, count}] |
      group_by(.concept) |
      map({concept: .[0].concept, count: (map(.count) | add)}) |
      sort_by(-.count, .concept) |
      .[:25][] |
      "- \(.count) \(.concept)"
    '
  } > "$path"
}

write_run_summary() {
  local status="$1"

  cat > "$RUN_DIR/summary.md" <<MARKDOWN
# Taxonomy Validation Run

- Run ID: \`$RUN_ID\`
- Set: \`$SET_NAME\`
- Form type: \`$FORM_TYPE\`
- Year filter: \`$FROM_YEAR\` through \`${TO_YEAR:-latest}\`
- Status: $status

## Highlights

MARKDOWN

  {
    jq -r '
      .summary |
      "- filing coverage: \(.parsed)/\(.expected) parsed, \(.missing) missing, \(.unparsed) unparsed\n" +
      "- missing metric filings: \(.filings_with_missing_metrics) total, " +
      "\(.filings_with_missing_metrics_by_tier.core // 0) core, " +
      "\(.filings_with_missing_metrics_by_tier.industry_sensitive // 0) industry-sensitive"
    ' "$RUN_DIR/coverage-report.json"
    jq -r '
      .summary |
      "- source taxonomy coverage: \(.selected) selected, \(.failed) failed, \(.findings) finding groups"
    ' "$RUN_DIR/taxonomy-coverage.json"
    jq -r '
      .summary |
      "- compact lint: \(.selected) selected, \(.failed) failed, \(.findings) unmapped findings, \(.quality_issues // 0) quality issues"
    ' "$RUN_DIR/lint-report.json"

    printf '\n## Human-Readable Summaries\n\n'
    printf -- '- `coverage-report.txt` summarizes expected filing coverage and missing metric tiers.\n'
    printf -- '- `taxonomy-coverage.txt` summarizes source-level unmapped concepts and hotspots.\n'
    printf -- '- `lint-report.txt` summarizes compact-view findings and remaining quality issues.\n'

    printf '\n## Detailed JSON\n\n'
    printf -- '- `coverage-report.json`\n'
    printf -- '- `taxonomy-coverage.json`\n'
    printf -- '- `lint-report.json`\n'

    printf '\n## Other Artifacts\n\n'
    printf -- '- `taxonomy.json`\n'
    printf -- '- `constituent-set.json`\n'
    printf -- '- `run.log`\n'

    printf '\nJSON files are for tooling and detailed review. Text files are summaries for quick human triage. This run captures review evidence only; it does not apply taxonomy changes.\n'
  } >> "$RUN_DIR/summary.md"
}

run_step "download and stitch SEC indexes" \
  "$CLI_PATH" index \
    --from-year "$FROM_YEAR" \
    --stitch \
    "${INDEX_REFRESH_ARGS[@]}"

test -s data/indexes/master.tsv

run_step_allow_failure "download $FORM_TYPE filings for $SET_NAME" \
  "$CLI_PATH" filings \
    --set "$SET_NAME" \
    --form-type "$FORM_TYPE" \
    "${YEAR_ARGS[@]}"

run_step_allow_failure "reprocess parsed filings and compact views" \
  "$CLI_PATH" parse \
    --set "$SET_NAME" \
    --form-type "$FORM_TYPE" \
    "${YEAR_ARGS[@]}" \
    --reprocess

if ! find data/parsed -type f -name filing.json -print -quit | grep -q .; then
  echo "error: no parsed filings were produced" >&2
  exit 1
fi

if ! find data/parsed -type f -name filing-view.json -print -quit | grep -q .; then
  echo "error: no compact filing views were produced" >&2
  exit 1
fi

run_step "expected filing coverage report" \
  "$CLI_PATH" coverage \
    --set "$SET_NAME" \
    --form-type "$FORM_TYPE" \
    "${YEAR_ARGS[@]}" \
    --out "$RUN_DIR/coverage-report.json"

run_step "source taxonomy coverage report" \
  "$CLI_PATH" taxonomy coverage \
    --set "$SET_NAME" \
    --form-type "$FORM_TYPE" \
    "${YEAR_ARGS[@]}" \
    --format json \
    --out "$RUN_DIR/taxonomy-coverage.json"

run_step "compact taxonomy lint report" \
  "$CLI_PATH" taxonomy lint \
    --set "$SET_NAME" \
    --form-type "$FORM_TYPE" \
    "${YEAR_ARGS[@]}" \
    --format json \
    --out "$RUN_DIR/lint-report.json"

run_step "human-readable validation summaries" \
  write_coverage_text_summary

run_step "human-readable source taxonomy summary" \
  write_taxonomy_coverage_text_summary

run_step "human-readable compact lint summary" \
  write_lint_text_summary

RUN_STATUS="complete"
if [[ "${#SOFT_FAILURES[@]}" -gt 0 ]]; then
  RUN_STATUS="review"
fi

write_run_summary "$RUN_STATUS"

if [[ "${#SOFT_FAILURES[@]}" -gt 0 ]]; then
  {
    printf '\n## Processing Warnings\n\n'
    for failure in "${SOFT_FAILURES[@]}"; do
      printf -- '- %s\n' "$failure"
    done
    printf '\nSee `run.log` for the underlying parser diagnostics.\n'
  } >> "$RUN_DIR/summary.md"
fi

echo "Taxonomy validation run completed: $RUN_DIR"
