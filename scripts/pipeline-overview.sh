#!/usr/bin/env bash
set -uo pipefail

data_dir="${CHOPSTICK_DATA_DIR:-./data}"
polling_db="${data_dir}/polling.db"
feed_db="${data_dir}/feed.db"
market_db="${data_dir}/market.db"
market_batches="${data_dir}/market/yahoo/batches"
refresh_seconds="${OVERVIEW_INTERVAL_SECONDS:-60}"
once=false

if [[ "${1:-}" == "--once" ]]; then
  once=true
elif [[ $# -ne 0 ]]; then
  echo "usage: $0 [--once]" >&2
  exit 2
fi

if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "sqlite3 is required for the pipeline overview" >&2
  exit 1
fi

has_table() {
  local database="$1"
  local table="$2"

  [[ -f "${database}" ]] &&
    [[ "$(sqlite3 -cmd ".timeout 2000" -cmd "PRAGMA query_only=ON" \
      "${database}" \
      "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='${table}';" \
      2>/dev/null)" == "1" ]]
}

has_column() {
  local database="$1"
  local table="$2"
  local column="$3"

  [[ "$(sqlite3 -cmd ".timeout 2000" -cmd "PRAGMA query_only=ON" \
    "${database}" \
    "SELECT COUNT(*) FROM pragma_table_info('${table}') WHERE name='${column}';" \
    2>/dev/null)" == "1" ]]
}

polling_overview() {
  echo "POLLING"
  if ! has_table "${polling_db}" "processed_accessions"; then
    echo "  ${polling_db} has not been initialized"
    return
  fi

  sqlite3 -cmd ".timeout 2000" -cmd "PRAGMA query_only=ON" \
    "${polling_db}" <<'SQL'
.headers off
.mode column
.width 27 12
SELECT 'Observed accessions', printf('%d', COUNT(*))
FROM processed_accessions
UNION ALL
SELECT 'Observed in last hour', printf('%d', COUNT(*))
FROM processed_accessions
WHERE processed_at >= datetime('now', '-1 hour')
UNION ALL
SELECT 'Atom / batch', printf('%d / %d',
  SUM(CASE WHEN source_feed = 'ATOM' THEN 1 ELSE 0 END),
  SUM(CASE WHEN source_feed = 'BATCH' THEN 1 ELSE 0 END))
FROM processed_accessions
UNION ALL
SELECT 'Outbox pending / sent', printf('%d / %d',
  SUM(CASE WHEN published_at IS NULL THEN 1 ELSE 0 END),
  SUM(CASE WHEN published_at IS NOT NULL THEN 1 ELSE 0 END))
FROM polling_outbox
UNION ALL
SELECT 'Unresolved failures', printf('%d', COUNT(*))
FROM work_failures
WHERE resolved_at IS NULL
UNION ALL
SELECT 'Polling dead letters', printf('%d', COUNT(*))
FROM polling_dead_letters;

.print ''
.print 'Top observed forms'
.headers on
.width 12 10
SELECT form_type AS form, COUNT(*) AS observed
FROM processed_accessions
GROUP BY form_type
ORDER BY observed DESC, form_type
LIMIT 6;
SQL
}

feed_overview() {
  echo
  echo "NEWS FEED"
  if ! has_table "${feed_db}" "feed_items"; then
    echo "  ${feed_db} has not been initialized"
    return
  fi

  if ! has_column "${feed_db}" "feed_items" "selected"; then
    sqlite3 -cmd ".timeout 2000" -cmd "PRAGMA query_only=ON" \
      "${feed_db}" <<'SQL'
.headers off
.mode column
.width 27 12
SELECT 'Feed items', printf('%d', COUNT(*)) FROM feed_items
UNION ALL
SELECT 'Latest stream sequence', printf('%d', COALESCE(MAX(stream_sequence), 0))
FROM feed_items;
.print '  Selection columns will appear after the news service starts.'
SQL
    return
  fi

  sqlite3 -cmd ".timeout 2000" -cmd "PRAGMA query_only=ON" \
    "${feed_db}" <<'SQL'
.headers off
.mode column
.width 27 12
SELECT 'Feed items', printf('%d', COUNT(*))
FROM feed_items
UNION ALL
SELECT 'Selected / observed only', printf('%d / %d',
  SUM(CASE WHEN selected = 1 THEN 1 ELSE 0 END),
  SUM(CASE WHEN selected = 0 THEN 1 ELSE 0 END))
FROM feed_items
UNION ALL
SELECT 'On-demand selections', printf('%d', COUNT(*))
FROM feed_items
WHERE selection_mode = 'on-demand'
UNION ALL
SELECT 'Latest stream sequence', printf('%d', COALESCE(MAX(stream_sequence), 0))
FROM feed_items
UNION ALL
SELECT 'Latest filing date', COALESCE(MAX(filing_date), '-')
FROM feed_items;

.print ''
.print 'Latest decisions'
.headers on
.width 11 7 10 32
SELECT filing_date AS filed, form_type AS form,
  CASE WHEN selected = 1 THEN selection_mode ELSE 'observed' END AS decision,
  substr(company_name, 1, 32) AS company
FROM feed_items
ORDER BY stream_sequence DESC
LIMIT 5;
SQL
}

market_overview() {
  echo
  echo "MARKET DATA"
  if ! has_table "${market_db}" "market_sync_state"; then
    echo "  ${market_db} has not been initialized"
    return
  fi

  if ! has_column "${market_db}" "market_sync_state" "covered_from"; then
    echo "  Restart a Yahoo command to migrate market coverage tracking"
    return
  fi

  sqlite3 -cmd ".timeout 2000" -cmd "PRAGMA query_only=ON" \
    "${market_db}" <<'SQL'
.headers off
.mode column
.width 27 42
WITH metrics AS (
  SELECT
    (SELECT COUNT(*) FROM market_securities
      WHERE provider = 'yahoo') AS securities,
    (SELECT COUNT(DISTINCT security_id) FROM market_prices
      WHERE provider = 'yahoo') AS priced,
    (SELECT COUNT(*) FROM market_prices
      WHERE provider = 'yahoo') AS prices,
    (SELECT COUNT(*) FROM market_prices
      WHERE provider = 'yahoo' AND open IS NULL) AS missing_opens,
    (SELECT COUNT(*) FROM market_corporate_actions
      WHERE provider = 'yahoo') AS actions,
    (SELECT COUNT(*) FROM market_sync_state
      WHERE provider = 'yahoo' AND covered_from IS NOT NULL) AS history_known,
    (SELECT MIN(covered_through) FROM market_sync_state
      WHERE provider = 'yahoo') AS all_checked,
    (SELECT MAX(covered_through) FROM market_sync_state
      WHERE provider = 'yahoo') AS newest_check,
    (SELECT MAX(covered_from) FROM market_sync_state
      WHERE provider = 'yahoo') AS common_start,
    (SELECT MIN(observation_date) FROM market_prices
      WHERE provider = 'yahoo') AS first_price,
    (SELECT MAX(observation_date) FROM market_prices
      WHERE provider = 'yahoo') AS last_price,
    (SELECT COUNT(*) FROM market_sync_state
      WHERE provider = 'yahoo' AND failure_count > 0) AS retries,
    (SELECT COUNT(*) FROM market_sync_state
      WHERE provider = 'yahoo' AND reconciliation_required = 1) AS reconciles,
    (SELECT COUNT(*) FROM market_sync_state
      WHERE provider = 'yahoo'
        AND datetime(lease_until) > datetime('now')) AS leased,
    (SELECT COUNT(*) FROM market_sync_state
      WHERE provider = 'yahoo'
        AND datetime(next_attempt_at) > datetime('now')) AS deferred
)
SELECT 'Pulse',
  CASE
    WHEN retries > 0 THEN printf('ATTENTION  %d retr%s', retries,
      CASE WHEN retries = 1 THEN 'y' ELSE 'ies' END)
    WHEN reconciles > 0 THEN printf('RECONCILING  %d securities', reconciles)
    WHEN priced < securities THEN printf('LOADING  %d/%d priced',
      priced, securities)
    WHEN history_known < securities THEN printf('BACKFILL  %d/%d histories',
      history_known, securities)
    ELSE 'READY  watermarks and observations agree'
  END
FROM metrics
UNION ALL
SELECT 'History progress',
  printf('[%s%s] %d/%d',
    substr('####################', 1,
      CASE WHEN securities = 0 OR history_known = 0 THEN 0
        WHEN history_known * 20 / securities = 0 THEN 1
        ELSE history_known * 20 / securities END),
    substr('....................', 1,
      20 - CASE WHEN securities = 0 OR history_known = 0 THEN 0
        WHEN history_known * 20 / securities = 0 THEN 1
        ELSE history_known * 20 / securities END),
    history_known, securities)
FROM metrics
UNION ALL
SELECT 'Universe / priced', printf('%d / %d', securities, priced)
FROM metrics
UNION ALL
SELECT 'Common requested span',
  CASE WHEN securities > 0 AND history_known = securities
    THEN printf('%s -> %s', common_start, all_checked)
    ELSE '-'
  END
FROM metrics
UNION ALL
SELECT 'Checked through',
  CASE WHEN all_checked = newest_check THEN COALESCE(all_checked, '-')
    ELSE printf('%s .. %s', all_checked, newest_check)
  END
FROM metrics
UNION ALL
SELECT 'Observed price span',
  printf('%s -> %s', COALESCE(first_price, '-'), COALESCE(last_price, '-'))
FROM metrics
UNION ALL
SELECT 'Prices / actions', printf('%d / %d', prices, actions)
FROM metrics
UNION ALL
SELECT 'Missing opens', printf('%d', missing_opens)
FROM metrics
UNION ALL
SELECT 'Retry / reconcile', printf('%d / %d', retries, reconciles)
FROM metrics
UNION ALL
SELECT 'Leased / deferred', printf('%d / %d', leased, deferred)
FROM metrics;
SQL

  local batch_count=0
  [[ -d "${market_batches}" ]] &&
    batch_count="$(find "${market_batches}" -type f -name '*.csv' 2>/dev/null |
      wc -l | tr -d ' ')"

  printf '  %-27s %s\n' "Retained CSV batches" "${batch_count}"
  printf '  %-27s %s\n' "Daily rendezvous" "21:00 America/New_York"
}

while true; do
  if [[ -t 1 ]]; then
    printf '\033[2J\033[H'
  fi
  printf 'EDGAR PIPELINE OVERVIEW  %s\n' "$(date '+%Y-%m-%d %H:%M:%S %Z')"
  printf 'Refresh: %ss  Data: %s\n\n' "${refresh_seconds}" "${data_dir}"

  polling_overview
  feed_overview
  market_overview

  if [[ "${once}" == true ]]; then
    break
  fi
  sleep "${refresh_seconds}"
done
