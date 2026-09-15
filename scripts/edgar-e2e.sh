#!/usr/bin/env bash

set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLI_PATH="$ROOT_DIR/bin/edgar"
SET_NAME="us-gaap-coverage"

cd "$ROOT_DIR"
export LOGLEVEL=debug

echo "Preparing US-GAAP coverage constituent set"
mkdir -p data/sets
cp golden/us-gaap-coverage.json data/sets/us-gaap-coverage.json

if [[ ! -x "$CLI_PATH" ]]; then
  echo "error: $CLI_PATH is missing; run 'make build' first" >&2
  exit 1
fi

echo "Step 1: downloading indexes from 2010 and stitching master.tsv"
"$CLI_PATH" index \
  --from-year 2010 \
  --refresh-latest \
  --stitch

test -s data/indexes/master.tsv

echo "Downloading 2026 filings for set $SET_NAME"
"$CLI_PATH" filings \
  --set "$SET_NAME" \
  --year 2026 \
  --form-type 10-K 
  
  # --form-type 10-Q \
  # --form-type 8-K

echo "Reprocessing 2026 filings for set $SET_NAME"
"$CLI_PATH" parse \
  --set "$SET_NAME" \
  --year 2026 \
  --reprocess

echo "Downloading historical 10-K filings for Microsoft"
"$CLI_PATH" filings \
  --cik 789019 \
  --form-type 10-K

echo "Reprocessing historical Microsoft 10-K filings"
"$CLI_PATH" parse \
  --cik 789019 \
  --form-type 10-K \
  --reprocess

if ! find data/parsed -type f -name filing.json -print -quit | grep -q .; then
  echo "error: no parsed filings were produced" >&2
  exit 1
fi

if ! find data/parsed -type f -name filing-view.json -print -quit | grep -q .; then
  echo "error: no compact filing views were produced" >&2
  exit 1
fi

msft_views="$(find data/parsed/0000789019 -type f -name filing-view.json 2>/dev/null | wc -l | tr -d ' ')"
if [[ "$msft_views" -lt 2 ]]; then
  echo "error: historical Microsoft filing views were not produced" >&2
  exit 1
fi

echo "EDGAR end-to-end test completed"
