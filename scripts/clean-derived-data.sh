#!/usr/bin/env bash

set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$ROOT_DIR"

echo "Removing generated EDGAR data; keeping raw filing and index caches"
rm -rf \
  data/cache/indexes \
  data/indexes \
  data/filings \
  data/parsed \
  data/sets \
  data/validation \
  data/analysis.db \
  data/analysis.db-shm \
  data/analysis.db-wal \
  data/polling.db \
  data/polling.db-shm \
  data/polling.db-wal \
  var/analysis.db \
  var/analysis.db-shm \
  var/analysis.db-wal \
  var/polling.db \
  var/polling.db-shm \
  var/polling.db-wal
mkdir -p data/cache/filings
mkdir -p data/cache/index-zips

if [[ -e data/cache/indexes || -e data/indexes || -e data/parsed || -e data/analysis.db ]]; then
  echo "error: derived EDGAR data remains after cleanup" >&2
  exit 1
fi
