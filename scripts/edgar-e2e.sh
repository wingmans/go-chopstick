#!/usr/bin/env bash

set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CLI_PATH="$ROOT_DIR/bin/edgar"

# Keep this list small: the script is an end-to-end smoke test, not a full archive sync.
CIKS=(
  0000789019 # Microsoft
  0000320193 # Apple
  0001018724 # Amazon
  0001652044 # Alphabet
  0001318605 # Tesla
)

cd "$ROOT_DIR"

if [[ ! -x "$CLI_PATH" ]]; then
  echo "error: $CLI_PATH is missing; run 'make build' first" >&2
  exit 1
fi

echo "Downloading 2026 indexes, refreshing the latest quarter, and stitching master.tsv"
"$CLI_PATH" index \
  --from-year 2026 \
  --refresh-latest \
  --stitch

test -s data/indexes/master.tsv

for cik in "${CIKS[@]}"; do
  echo "Downloading 2026 filings for CIK $cik"
  "$CLI_PATH" filings \
    --cik "$cik" \
    --year 2026 \
    --form-type 10-K \
    --form-type 10-Q 
    # --form-type 8-K
done

echo "Reprocessing all locally available filings"
"$CLI_PATH" parse \
  --reprocess

if ! find data/parsed -type f -name filing.json -print -quit | grep -q .; then
  echo "error: no parsed filings were produced" >&2
  exit 1
fi

echo "EDGAR end-to-end test completed"
