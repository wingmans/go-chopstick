#!/usr/bin/env bash

set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLI_PATH="$ROOT_DIR/bin/edgar"
SET_NAME="golden"

cd "$ROOT_DIR"

echo "Preparing golden constituent set"
mkdir -p data/sets
cp golden/golden.json data/sets/golden.json

if [[ ! -x "$CLI_PATH" ]]; then
  echo "error: $CLI_PATH is missing; run 'make build' first" >&2
  exit 1
fi

echo "Step 1: downloading indexes from 2024 and stitching master.tsv"
"$CLI_PATH" index \
  --from-year 2024 \
  --refresh-latest \
  --stitch

test -s data/indexes/master.tsv

echo "Downloading 2026 filings for set $SET_NAME"
"$CLI_PATH" filings \
  --set "$SET_NAME" \
  --year 2026 \
  --form-type 10-K \
  --form-type 10-Q \
  --form-type 8-K

echo "Reprocessing 2026 filings for set $SET_NAME"
"$CLI_PATH" parse \
  --set "$SET_NAME" \
  --year 2026 \
  --reprocess

if ! find data/parsed -type f -name filing.json -print -quit | grep -q .; then
  echo "error: no parsed filings were produced" >&2
  exit 1
fi

echo "EDGAR end-to-end test completed"
