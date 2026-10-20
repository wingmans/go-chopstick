#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
nats_server="${root_dir}/.tools/bin/nats-server"
data_dir="${CHOPSTICK_DATA_DIR:-data}"
[[ "${data_dir}" = /* ]] || data_dir="${root_dir}/${data_dir#./}"
state_dir="${data_dir}/nats"
store_dir="${state_dir}/store"
pid_file="${state_dir}/nats.pid"
log_file="${state_dir}/nats.log"

running_pid() {
  [[ -f "${pid_file}" ]] && kill -0 "$(<"${pid_file}")" 2>/dev/null && {
    echo "$(<"${pid_file}")"
    return
  }

  return 1
}

is_running() {
  running_pid >/dev/null
}

start() {
  [[ -x "${nats_server}" ]] || {
    echo "project-local nats-server is not installed" >&2
    echo "run scripts/install-tools.sh first" >&2
    return 1
  }

  pid="$(running_pid)" && {
    echo "NATS is already running with PID ${pid}"
    return
  }

  mkdir -p "${store_dir}"
  "${nats_server}" -js -sd "${store_dir}" -p 4222 -m 8222 \
    >"${log_file}" 2>&1 &
  echo "$!" >"${pid_file}"

  sleep 0.2
  is_running || {
    rm -f "${pid_file}"
    echo "NATS failed to start; log: ${log_file}" >&2
    tail -n 10 "${log_file}" >&2
    return 1
  }

  echo "NATS started; log: ${log_file}"
}

stop() {
  pid="$(running_pid)" || {
    rm -f "${pid_file}"
    echo "NATS is not running"
    return
  }

  kill "${pid}"
  wait "${pid}" 2>/dev/null || true
  rm -f "${pid_file}"
  echo "NATS stopped"
}

reset() {
  ! is_running || {
    echo "stop NATS before resetting its data" >&2
    return 1
  }

  rm -rf "${store_dir}"
  rm -f "${pid_file}" "${log_file}"
  echo "NATS development data removed"
}

status() {
  pid="$(running_pid)" && {
    echo "NATS is running with PID ${pid}"
    return
  }

  echo "NATS is not running"
  return 1
}

case "${1:-}" in
  start) start ;;
  stop) stop ;;
  reset) reset ;;
  status) status ;;
  *) echo "usage: $0 {start|stop|reset|status}" >&2; exit 2 ;;
esac
