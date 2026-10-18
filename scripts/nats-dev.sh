#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
state_dir="${root_dir}/var/nats"
store_dir="${state_dir}/store"
pid_file="${state_dir}/nats.pid"
log_file="${state_dir}/nats.log"

is_running() {
  [[ -f "${pid_file}" ]] && kill -0 "$(<"${pid_file}")" 2>/dev/null
}

start() {
  if ! command -v nats-server >/dev/null 2>&1; then
    echo "nats-server is not installed or is not on PATH" >&2
    exit 1
  fi
  if is_running; then
    echo "NATS is already running with PID $(<"${pid_file}")"
    return
  fi

  mkdir -p "${store_dir}"
  nats-server -js -sd "${store_dir}" -p 4222 -m 8222 \
    >"${log_file}" 2>&1 &
  echo "$!" >"${pid_file}"
  echo "NATS started; log: ${log_file}"
}

stop() {
  if ! is_running; then
    rm -f "${pid_file}"
    echo "NATS is not running"
    return
  fi

  pid="$(<"${pid_file}")"
  kill "${pid}"
  wait "${pid}" 2>/dev/null || true
  rm -f "${pid_file}"
  echo "NATS stopped"
}

reset() {
  if is_running; then
    echo "stop NATS before resetting its data" >&2
    exit 1
  fi
  rm -rf "${store_dir}"
  rm -f "${pid_file}" "${log_file}"
  echo "NATS development data removed"
}

status() {
  if is_running; then
    echo "NATS is running with PID $(<"${pid_file}")"
  else
    echo "NATS is not running"
    exit 1
  fi
}

case "${1:-}" in
  start) start ;;
  stop) stop ;;
  reset) reset ;;
  status) status ;;
  *) echo "usage: $0 {start|stop|reset|status}" >&2; exit 2 ;;
esac
