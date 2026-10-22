#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
bin_dir="${root_dir}/.tools/bin"
nats_server_version="v2.15.1"

mkdir -p "${bin_dir}"

echo "Installing nats-server ${nats_server_version} into ${bin_dir}"
GOBIN="${bin_dir}" go install \
  "github.com/nats-io/nats-server/v2@${nats_server_version}"

"${bin_dir}/nats-server" --version
