#!/bin/bash
set -euo pipefail

usage() {
  echo "usage: gateway-credentials.sh <ensure|rotate> <credentials-file>" >&2
  exit 2
}

if [ "$#" -ne 2 ]; then
  usage
fi

action="$1"
credentials_file="$2"

case "$action" in
  ensure)
    if [ -f "$credentials_file" ]; then
      exit 0
    fi
    ;;
  rotate)
    ;;
  *)
    usage
    ;;
esac

credentials_dir="$(dirname "$credentials_file")"
mkdir -p "$credentials_dir"
chmod 750 "$credentials_dir"

temporary_file="$(mktemp "$credentials_dir/.outbound-gateway.XXXXXX")"
trap 'rm -f "$temporary_file"' EXIT

user="chopstick-dev-$(openssl rand -hex 8)"
password="$(openssl rand -hex 32)"

{
  printf 'CHOP_OUTBOUND_GATEWAY_USER=%s\n' "$user"
  printf 'CHOP_OUTBOUND_GATEWAY_PASS=%s\n' "$password"
} >"$temporary_file"

chmod 600 "$temporary_file"
mv "$temporary_file" "$credentials_file"
trap - EXIT

echo "generated disposable gateway credentials at $credentials_file"
