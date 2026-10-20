#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
data_dir="${CHOPSTICK_DATA_DIR:-data}"
[[ "${data_dir}" = /* ]] || data_dir="${root_dir}/${data_dir#./}"

[[ "${data_dir}" != / && "${data_dir}" != "${root_dir}" ]] || {
  echo "refusing to clean unsafe data directory: ${data_dir}" >&2
  exit 1
}

echo "Cleaning derived development data under ${data_dir}"
echo "Preserving downloaded filings, index archives, and constituent sets"

rm -rf -- \
  "${data_dir}/parsed" \
  "${data_dir}/validation" \
  "${data_dir}/logs" \
  "${data_dir}/nats" \
  "${data_dir}/tmp"
rm -f -- \
  "${data_dir}/cache/indexes/master.tsv" \
  "${data_dir}"/{polling,analysis,feed}.db{,-shm,-wal,-journal}

echo "Development processing state removed"
