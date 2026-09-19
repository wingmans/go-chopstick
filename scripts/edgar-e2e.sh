#!/usr/bin/env bash

set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLI_PATH="$ROOT_DIR/bin/edgar"
SET_NAME="us-gaap-coverage"
REFRESH_LATEST="${REFRESH_LATEST:-0}"

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
INDEX_ARGS=(--from-year 2010 --stitch)
if [[ "$REFRESH_LATEST" == "1" ]]; then
  INDEX_ARGS+=(--refresh-latest)
fi
"$CLI_PATH" index \
  "${INDEX_ARGS[@]}"

test -s data/indexes/master.tsv

echo "Downloading filings from 2010 onward for set $SET_NAME"
"$CLI_PATH" filings \
  --set "$SET_NAME" \
  --form-type 10-K

echo "Reprocessing filings from 2010 onward for set $SET_NAME"
"$CLI_PATH" parse \
  --set "$SET_NAME" \
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

echo "EDGAR end-to-end test completed"
