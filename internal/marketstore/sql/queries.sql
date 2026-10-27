-- name: RegisterSecurity :exec
INSERT INTO market_securities (
    provider,
    security_id,
    symbol,
    name,
    updated_at
) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(provider, security_id) DO UPDATE SET
    symbol = excluded.symbol,
    name = excluded.name,
    updated_at = excluded.updated_at;

-- name: ClaimWork :execresult
INSERT INTO market_sync_state (
    provider,
    security_id,
    lease_until,
    updated_at
) VALUES (
    sqlc.arg(provider),
    sqlc.arg(security_id),
    sqlc.arg(lease_until),
    sqlc.arg(updated_at)
)
ON CONFLICT(provider, security_id) DO UPDATE SET
    lease_until = excluded.lease_until,
    updated_at = excluded.updated_at
WHERE market_sync_state.lease_until IS NULL
   OR market_sync_state.lease_until <= excluded.updated_at;

-- name: UpsertPrice :exec
INSERT INTO market_prices (
    provider,
    security_id,
    observation_date,
    currency,
    open,
    close,
    adjusted_close,
    updated_at
) VALUES (
    sqlc.arg(provider),
    sqlc.arg(security_id),
    sqlc.arg(observation_date),
    'USD',
    sqlc.narg(open),
    sqlc.arg(close),
    sqlc.arg(adjusted_close),
    sqlc.arg(updated_at)
)
ON CONFLICT(provider, security_id, observation_date) DO UPDATE SET
    currency = excluded.currency,
    open = excluded.open,
    close = excluded.close,
    adjusted_close = excluded.adjusted_close,
    updated_at = excluded.updated_at;

-- name: DeleteActions :exec
DELETE FROM market_corporate_actions
WHERE provider = sqlc.arg(provider)
  AND security_id = sqlc.arg(security_id);

-- name: MarketAction :one
SELECT action_date, action_type, amount, numerator, denominator
FROM market_corporate_actions
WHERE provider = sqlc.arg(provider)
  AND security_id = sqlc.arg(security_id)
  AND source_id = sqlc.arg(source_id);

-- name: UpsertAction :exec
INSERT INTO market_corporate_actions (
    provider,
    security_id,
    source_id,
    action_date,
    action_type,
    amount,
    numerator,
    denominator,
    reconciled_at,
    updated_at
) VALUES (
    sqlc.arg(provider),
    sqlc.arg(security_id),
    sqlc.arg(source_id),
    sqlc.arg(action_date),
    sqlc.arg(action_type),
    sqlc.narg(amount),
    sqlc.narg(numerator),
    sqlc.narg(denominator),
    sqlc.narg(reconciled_at),
    sqlc.arg(updated_at)
)
ON CONFLICT(provider, security_id, source_id) DO UPDATE SET
    action_date = excluded.action_date,
    action_type = excluded.action_type,
    amount = excluded.amount,
    numerator = excluded.numerator,
    denominator = excluded.denominator,
    reconciled_at = CASE
        WHEN excluded.reconciled_at IS NOT NULL
        THEN excluded.reconciled_at
        WHEN market_corporate_actions.action_date != excluded.action_date
          OR market_corporate_actions.action_type != excluded.action_type
          OR market_corporate_actions.amount IS NOT excluded.amount
          OR market_corporate_actions.numerator IS NOT excluded.numerator
          OR market_corporate_actions.denominator IS NOT excluded.denominator
        THEN NULL
        ELSE market_corporate_actions.reconciled_at
    END,
    updated_at = excluded.updated_at;

-- name: AdvanceSyncState :exec
INSERT INTO market_sync_state (
    provider,
    security_id,
    bootstrap_complete,
    covered_from,
    covered_through,
    observed_from,
    observed_through,
    reconciliation_required,
    failure_count,
    updated_at
) VALUES (
    sqlc.arg(provider),
    sqlc.arg(security_id),
    1,
    sqlc.narg(covered_from),
    sqlc.arg(covered_through),
    sqlc.narg(observed_from),
    sqlc.narg(observed_through),
    sqlc.arg(reconciliation_required),
    0,
    sqlc.arg(updated_at)
)
ON CONFLICT(provider, security_id) DO UPDATE SET
    bootstrap_complete = 1,
    covered_from = CASE
        WHEN excluded.covered_from IS NULL
        THEN market_sync_state.covered_from
        WHEN market_sync_state.covered_from IS NULL
          OR excluded.covered_from < market_sync_state.covered_from
        THEN excluded.covered_from
        ELSE market_sync_state.covered_from
    END,
    covered_through = CASE
        WHEN market_sync_state.covered_through IS NULL
          OR market_sync_state.covered_through < excluded.covered_through
        THEN excluded.covered_through
        ELSE market_sync_state.covered_through
    END,
    observed_from = CASE
        WHEN excluded.observed_from IS NULL
        THEN market_sync_state.observed_from
        WHEN market_sync_state.observed_from IS NULL
          OR excluded.observed_from < market_sync_state.observed_from
        THEN excluded.observed_from
        ELSE market_sync_state.observed_from
    END,
    observed_through = CASE
        WHEN excluded.observed_through IS NULL
        THEN market_sync_state.observed_through
        WHEN market_sync_state.observed_through IS NULL
          OR excluded.observed_through > market_sync_state.observed_through
        THEN excluded.observed_through
        ELSE market_sync_state.observed_through
    END,
    reconciliation_required = CASE
        WHEN market_sync_state.reconciliation_required = 1
          OR excluded.reconciliation_required = 1
        THEN 1
        ELSE 0
    END,
    next_attempt_at = NULL,
    failure_count = 0,
    last_error = NULL,
    lease_until = NULL,
    updated_at = excluded.updated_at;

-- name: ClearReconciliation :exec
UPDATE market_sync_state
SET reconciliation_required = 0
WHERE provider = sqlc.arg(provider)
  AND security_id = sqlc.arg(security_id);

-- name: CompleteRepair :exec
UPDATE market_repairs
SET completed_at = sqlc.arg(completed_at)
WHERE provider = sqlc.arg(provider)
  AND security_id = sqlc.arg(security_id)
  AND from_date = sqlc.arg(from_date)
  AND through_date = sqlc.arg(through_date);

-- name: CompleteCoveredRepairs :exec
UPDATE market_repairs
SET completed_at = sqlc.arg(completed_at)
WHERE provider = sqlc.arg(provider)
  AND security_id = sqlc.arg(security_id)
  AND completed_at IS NULL
  AND from_date >= sqlc.arg(from_date)
  AND through_date <= sqlc.arg(through_date);

-- name: RecordFailure :exec
INSERT INTO market_sync_state (
    provider,
    security_id,
    next_attempt_at,
    failure_count,
    last_error,
    updated_at
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(provider, security_id) DO UPDATE SET
    next_attempt_at = excluded.next_attempt_at,
    failure_count = excluded.failure_count,
    last_error = excluded.last_error,
    lease_until = NULL,
    updated_at = excluded.updated_at;

-- name: QueueRepair :exec
INSERT INTO market_repairs (
    provider,
    security_id,
    from_date,
    through_date,
    created_at
) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(provider, security_id, from_date, through_date)
DO UPDATE SET completed_at = NULL;

-- name: LoadSyncState :one
SELECT
    state.bootstrap_complete,
    state.covered_from,
    state.covered_through,
    state.observed_through,
    state.reconciliation_required,
    state.next_attempt_at,
    state.failure_count,
    state.last_error,
    state.lease_until
FROM market_sync_state state
WHERE state.provider = sqlc.arg(provider)
  AND state.security_id = sqlc.arg(security_id);

-- name: PendingRepair :one
SELECT from_date, through_date
FROM market_repairs
WHERE provider = sqlc.arg(provider)
  AND security_id = sqlc.arg(security_id)
  AND completed_at IS NULL
ORDER BY from_date
LIMIT 1;

-- name: HasPrices :one
SELECT EXISTS(
    SELECT 1
    FROM market_prices
    WHERE provider = sqlc.arg(provider)
      AND security_id = sqlc.arg(security_id)
);
