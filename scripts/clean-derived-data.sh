#!/usr/bin/env bash

set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$ROOT_DIR"

echo "Removing generated EDGAR data; keeping data/filings and cached index ZIPs"
rm -rf \
  data/indexes \
  data/parsed \
  data/sets \
  data/validation/runs
mkdir -p data/filings
mkdir -p data/cache/index-zips
