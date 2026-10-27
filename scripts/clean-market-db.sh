#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
data_dir="${CHOPSTICK_DATA_DIR:-data}"
[[ "${data_dir}" = /* ]] || data_dir="${root_dir}/${data_dir#./}"

[[ "${data_dir}" != / && "${data_dir}" != "${root_dir}" ]] || {
  echo "refusing to clean unsafe data directory: ${data_dir}" >&2
  exit 1
}

database="${data_dir}/market.db"

echo "Removing the derived market database at ${database}"
rm -f -- \
  "${database}" \
  "${database}-shm" \
  "${database}-wal" \
  "${database}-journal"

echo "Preserved market CSV batches under ${data_dir}/market"
