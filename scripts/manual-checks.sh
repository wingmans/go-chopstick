#!/usr/bin/env bash

set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASELINE="${BASELINE:-$ROOT_DIR/golden/manual-checks.json}"
PARSED_DIR="${PARSED_DIR:-$ROOT_DIR/data/parsed}"
TOLERANCE="${TOLERANCE:-0.011}"

cd "$ROOT_DIR"

if ! command -v jq >/dev/null 2>&1; then
  echo "error: jq is required" >&2
  exit 1
fi

if [[ ! -f "$BASELINE" ]]; then
  echo "error: baseline not found: $BASELINE" >&2
  exit 1
fi

if [[ ! -d "$PARSED_DIR" ]]; then
  echo "error: parsed directory not found: $PARSED_DIR" >&2
  exit 1
fi

jq -e '.schema_version == 1 and (.companies | type == "array")' "$BASELINE" >/dev/null

declare -a METRICS=(
  revenue
  net_income
  diluted_eps
  total_assets
  cash_and_cash_equivalents
  total_liabilities
  equity
  operating_cash_flow
  investing_cash_flow
  financing_cash_flow
)

actual_value() {
  local view_path="$1"
  local metric="$2"
  local fiscal_year="$3"
  local period="FY$fiscal_year"

  jq -r --arg metric "$metric" --arg period "$period" --arg fiscal_year "$fiscal_year" '
    def row($statement; $key):
      $statement.groups[]?.rows[]? | select(.key == $key);
    def value($statement; $key; $period):
      [row($statement; $key).values[$period].value // empty][0] // "";
    def instant_value($statement; $key; $year):
      [row($statement; $key).values | to_entries[] |
        select(.key | startswith($year)) | .value.value][0] // "";
    if $metric == "revenue" then value(.statements.income; "revenue"; $period)
    elif $metric == "net_income" then value(.statements.income; "net_income"; $period)
    elif $metric == "diluted_eps" then value(.statements.income; "eps_diluted"; $period)
    elif $metric == "total_assets" then instant_value(.statements.balance_sheet; "assets"; $fiscal_year)
    elif $metric == "cash_and_cash_equivalents" then instant_value(.statements.balance_sheet; "cash"; $fiscal_year)
    elif $metric == "total_liabilities" then instant_value(.statements.balance_sheet; "liabilities"; $fiscal_year)
    elif $metric == "equity" then instant_value(.statements.balance_sheet; "equity"; $fiscal_year)
    elif $metric == "operating_cash_flow" then value(.statements.cash_flow; "operating_cash_flow"; $period)
    elif $metric == "investing_cash_flow" then value(.statements.cash_flow; "investing_cash_flow"; $period)
    elif $metric == "financing_cash_flow" then value(.statements.cash_flow; "financing_cash_flow"; $period)
    else ""
    end
  ' "$view_path"
}

expected_value() {
  local ticker="$1"
  local metric="$2"
  local fiscal_year="$3"

  jq -r --arg ticker "$ticker" --arg metric "$metric" --arg fiscal_year "$fiscal_year" '
    .companies[] | select(.ticker == $ticker) | .fiscal_years[$fiscal_year][$metric] // ""
  ' "$BASELINE"
}

find_view() {
  local cik="$1"
  local accession="$2"
  local path

  while IFS= read -r -d '' path; do
    if jq -e --arg accession "$accession" \
      '.metadata.form_type == "10-K" and .metadata.accession == $accession' \
      "$path" >/dev/null; then
      printf '%s\n' "$path"
      return 0
    fi
  done < <(find "$PARSED_DIR/$cik" -type f -name filing-view.json -print0 2>/dev/null)

  return 1
}

compare_value() {
  local expected="$1"
  local actual="$2"

  awk -v expected="$expected" -v actual="$actual" -v tolerance="$TOLERANCE" '
    BEGIN {
      if (expected == "" || actual == "") exit 1
      difference = expected - actual
      if (difference < 0) difference = -difference
      exit(difference <= tolerance ? 0 : 1)
    }
  '
}

failures=0
comparisons=0
exceptions=0

printf 'Manual baseline: %s\n' "$BASELINE"
printf 'Parsed views:   %s\n' "$PARSED_DIR"
printf 'Tolerance:      %s\n\n' "$TOLERANCE"

while IFS=$'\t' read -r ticker cik fiscal_year accession balance_accession; do
  view_path="$(find_view "$cik" "$accession" || true)"
  if [[ -z "$view_path" ]]; then
    echo "FAIL $ticker FY$fiscal_year: no matching 10-K filing-view.json"
    failures=$((failures + 1))
    continue
  fi

  report_date="$(jq -r '.metadata.report_date' "$view_path")"
  actual_accession="$(jq -r '.metadata.accession' "$view_path")"
  balance_path="$view_path"
  if [[ -n "$balance_accession" && "$balance_accession" != "null" ]]; then
    balance_path="$(find_view "$cik" "$balance_accession" || true)"
    if [[ -z "$balance_path" ]]; then
      echo "FAIL $ticker FY$fiscal_year: balance source $balance_accession not found"
      failures=$((failures + 1))
      continue
    fi
  fi
  printf '%s FY%s (%s) %s\n' "$ticker" "$fiscal_year" "$report_date" "$actual_accession"

  for metric in "${METRICS[@]}"; do
    expected="$(expected_value "$ticker" "$metric" "$fiscal_year")"
    if [[ "$expected" == "null" || -z "$expected" ]]; then
      printf '  SKIP %-30s approved exception\n' "$metric"
      exceptions=$((exceptions + 1))
      continue
    fi

    actual_path="$view_path"
    case "$metric" in
      total_assets|cash_and_cash_equivalents|total_liabilities|equity)
        actual_path="$balance_path"
        ;;
    esac
    actual_raw="$(actual_value "$actual_path" "$metric" "$fiscal_year")"
    actual="$actual_raw"
    if [[ "$metric" != "diluted_eps" && -n "$actual_raw" ]]; then
      actual="$(awk -v value="$actual_raw" 'BEGIN { printf "%.12g", value / 1000000000 }')"
    fi

    comparisons=$((comparisons + 1))
    if compare_value "$expected" "$actual"; then
      printf '  PASS %-30s expected=%s actual=%s\n' "$metric" "$expected" "$actual"
    else
      printf '  FAIL %-30s expected=%s actual=%s\n' "$metric" "$expected" "${actual:-missing}"
      failures=$((failures + 1))
    fi
  done
done < <(jq -r '
  .companies[] | . as $company | .fiscal_years | to_entries[] |
  [$company.ticker, $company.cik, .key,
   (.value.source_accession // $company.source_accession),
   (.value.balance_source_accession // "")] | @tsv
' "$BASELINE")

printf '\nCompared: %d, exceptions: %d, failures: %d\n' "$comparisons" "$exceptions" "$failures"

if (( failures > 0 )); then
  exit 1
fi
