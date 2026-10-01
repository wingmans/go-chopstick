#!/usr/bin/env bash

set -Eeuo pipefail

SECONDS=0
on_exit() {
	local status=$?
	echo "EDGAR end-to-end duration: ${SECONDS}s"
	return "$status"
}
trap on_exit EXIT

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLI_PATH="$ROOT_DIR/bin/edgar"
SET_NAME="us-gaap"
FROM_YEAR="${FROM_YEAR:-2016}"
REFRESH_LATEST="${REFRESH_LATEST:-0}"

if [[ -n "${FORM_TYPE:-}" ]]; then
	FORM_TYPES=("$FORM_TYPE")
else
	FORM_TYPES=(10-K 10-Q 8-K)
fi

cd "$ROOT_DIR"
export LOGLEVEL=debug

echo "Preparing US-GAAP coverage constituent set"
mkdir -p data/sets
cp golden/us-gaap.json data/sets/us-gaap.json

if [[ ! -x "$CLI_PATH" ]]; then
  echo "error: $CLI_PATH is missing; run 'make build' first" >&2
  exit 1
fi

echo "Step 1: downloading indexes from $FROM_YEAR and stitching master.tsv"
INDEX_ARGS=(--from-year "$FROM_YEAR" --stitch)
if [[ "$REFRESH_LATEST" == "1" ]]; then
  INDEX_ARGS+=(--refresh-latest)
fi
"$CLI_PATH" index \
  "${INDEX_ARGS[@]}"

test -s data/cache/indexes/master.tsv

for form_type in "${FORM_TYPES[@]}"; do
	echo "Downloading and processing $form_type filings from $FROM_YEAR onward"
	"$CLI_PATH" filings \
		--set "$SET_NAME" \
		--form-type "$form_type" \
		--from-year "$FROM_YEAR"
done

if [[ ! -s data/analysis.db ]]; then
  echo "error: analysis database was not produced" >&2
	exit 1
fi

if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "error: sqlite3 is required to inspect analysis.db" >&2
  exit 1
fi

if [[ "$(sqlite3 data/analysis.db \
  'SELECT COUNT(*) FROM filing_views;')" -eq 0 ]]; then
  echo "error: analysis database contains no filing views" >&2
  exit 1
fi

echo "EDGAR end-to-end test completed"
