#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

project_path() {
  case "$1" in
    /*) printf '%s\n' "$1" ;;
    *) printf '%s/%s\n' "$root_dir" "${1#./}" ;;
  esac
}

gateway_dir="$(project_path "${CHOP_GATEWAY_DIR:-../go-proxy-app/throttling}")"
rules_file="$(project_path "${CHOP_GATEWAY_RULES:-infra/outbound.rules}")"
data_dir="$(project_path "${CHOPSTICK_DATA_DIR:-data}")"
credentials_file="$(project_path "${CHOP_GATEWAY_CREDENTIALS:-$data_dir/run/outbound-gateway.env}")"
gateway_image="${CHOP_GATEWAY_IMAGE:-go-proxy-app-throttling:0.1.0}"

check_repository() {
  [[ -d "$gateway_dir" ]] || {
    printf 'gateway repository not found: %s\n' "$gateway_dir" >&2
    return 1
  }
}

check_policy() {
  [[ -f "$rules_file" ]] || {
    printf 'gateway policy not found: %s\n' "$rules_file" >&2
    return 1
  }
}

compose() {
  docker compose --project-directory "$gateway_dir" "$@"
}

build() {
  make -C "$gateway_dir" build PROXY_IMAGE="$gateway_image"
}

provision() {
  docker image inspect "$gateway_image" >/dev/null 2>&1 || build
  bash "$root_dir/scripts/gateway-credentials.sh" ensure "$credentials_file"
  . "$credentials_file"

  local proxy_hash
  proxy_hash="$(docker run --rm "$gateway_image" caddy hash-password \
    --plaintext "$CHOP_OUTBOUND_GATEWAY_PASS")"

  PROXY_USER="$CHOP_OUTBOUND_GATEWAY_USER" PROXY_HASH="$proxy_hash" \
    make -C "$gateway_dir" up RULES_FILE="$rules_file" PROXY_IMAGE="$gateway_image"
}

matches_container() {
  [[ -n "$1" && -f "$credentials_file" ]] || return 1

  local metadata
  metadata="$(docker inspect --format \
    '{{.Config.Image}} {{index .Config.Labels "app.proxy.policy-sha256"}}' "$1")" || return 1
  [[ "$metadata" == "$gateway_image $2" ]]
}

start() {
  check_policy

  local container policy_hash
  container="$(compose ps -a -q caddy)"
  policy_hash="$(shasum -a 256 "$rules_file" | awk '{print $1}')"

  matches_container "$container" "$policy_hash" || {
    provision
    return
  }

  compose start --wait caddy
}

reset() {
  check_policy
  build
  make -C "$gateway_dir" down PROXY_IMAGE="$gateway_image"
  bash "$root_dir/scripts/gateway-credentials.sh" rotate "$credentials_file"
  provision
}

case "${1:-}" in
  start) check_repository; start ;;
  stop) check_repository; compose stop caddy ;;
  status) check_repository; compose ps ;;
  reset) check_repository; reset ;;
  *) printf 'usage: %s {start|stop|status|reset}\n' "$0" >&2; exit 2 ;;
esac
