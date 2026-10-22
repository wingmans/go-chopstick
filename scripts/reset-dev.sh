#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
data_dir="${CHOPSTICK_DATA_DIR:-data}"
[[ "${data_dir}" = /* ]] || data_dir="${root_dir}/${data_dir#./}"

[[ "${data_dir}" != / && "${data_dir}" != "${root_dir}" ]] || {
  echo "refusing to reset unsafe data directory: ${data_dir}" >&2
  exit 1
}

echo "Removing all application data under ${data_dir}"
rm -rf -- "${data_dir}"
mkdir -p "${data_dir}"
echo "Application data reset complete"
