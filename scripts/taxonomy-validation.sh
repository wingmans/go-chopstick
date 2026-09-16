#!/usr/bin/env bash

set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLI_PATH="${CLI_PATH:-$ROOT_DIR/bin/edgar}"
SET_NAME="${SET_NAME:-us-gaap-coverage}"
FORM_TYPE="${FORM_TYPE:-10-K}"
FROM_YEAR="${FROM_YEAR:-2015}"
TO_YEAR="${TO_YEAR:-}"
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
complete.
MARKDOWN

YEAR_ARGS=(--from-year "$FROM_YEAR")
if [[ -n "$TO_YEAR" ]]; then
  YEAR_ARGS+=(--to-year "$TO_YEAR")
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

run_step "download and stitch SEC indexes" \
  "$CLI_PATH" index \
    --from-year "$FROM_YEAR" \
    --refresh-latest \
    --stitch

test -s data/indexes/master.tsv

run_step_allow_failure "download $FORM_TYPE filings for $SET_NAME" \
  "$CLI_PATH" filings \
    --set "$SET_NAME" \
    --form-type "$FORM_TYPE"

run_step_allow_failure "reprocess parsed filings and compact views" \
  "$CLI_PATH" parse \
    --set "$SET_NAME" \
    --form-type "$FORM_TYPE" \
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

RUN_STATUS="complete"
if [[ "${#SOFT_FAILURES[@]}" -gt 0 ]]; then
  RUN_STATUS="review"
fi

cat > "$RUN_DIR/summary.md" <<MARKDOWN
# Taxonomy Validation Run

- Run ID: \`$RUN_ID\`
- Set: \`$SET_NAME\`
- Form type: \`$FORM_TYPE\`
- Year filter: \`$FROM_YEAR\` through \`${TO_YEAR:-latest}\`
- Status: $RUN_STATUS

## Artifacts

- \`coverage-report.json\`
- \`taxonomy-coverage.json\`
- \`lint-report.json\`
- \`taxonomy.json\`
- \`constituent-set.json\`
- \`run.log\`

This run captures review evidence only. It does not apply taxonomy changes.
MARKDOWN

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
