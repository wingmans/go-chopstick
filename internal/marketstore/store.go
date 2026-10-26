// Package marketstore persists market prices, corporate actions, and the
// operational state needed to incrementally update them.
package marketstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const dateLayout = "2006-01-02"

// WorkKind identifies why a security needs to be fetched.
type WorkKind string

const (
	// WorkBootstrap loads all available history for a security without prices.
	WorkBootstrap WorkKind = "bootstrap"
	// WorkIncremental advances a security beyond its coverage watermark.
	WorkIncremental WorkKind = "incremental"
	// WorkReconcile refreshes history after a corporate action changed.
	WorkReconcile WorkKind = "reconcile"
	// WorkRepair fills an explicitly identified historical gap.
	WorkRepair WorkKind = "repair"
)

// Security maps a stable security ID to one provider symbol.
type Security struct {
	ID     string
	Symbol string
	Name   string
}

// Price is one daily provider observation.
type Price struct {
	Date          time.Time
	Open          float64
	Close         float64
	AdjustedClose float64
}

// CorporateAction is one provider dividend or split event.
type CorporateAction struct {
	SourceID    string
	Date        time.Time
	Type        string
	Amount      float64
	Numerator   float64
	Denominator float64
}

// WorkItem describes one independently retryable provider request.
type WorkItem struct {
	Provider     string
	Security     Security
	Kind         WorkKind
	From         time.Time
	Through      time.Time
	EligibleAt   time.Time
	LeaseUntil   time.Time
	FailureCount int
	LastError    string
}

// Store is an unpartitioned SQLite market-data store.
type Store struct {
	db *sql.DB
}

// syncState represents the synchronization state of a security with a provider.
type syncState struct {
	bootstrapComplete      bool
	coveredThrough         string
	observedThrough        string
	reconciliationRequired bool
	nextAttempt            time.Time
	leaseUntil             time.Time
	failureCount           int
	lastError              string
	repairFrom             sql.NullString
	repairThrough          sql.NullString
}

// Open opens or creates the market-data tables in the given SQLite database.
func Open(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("market database path is required")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create market database directory: %w", err)
	}

	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open market database: %w", err)
	}

	db.SetMaxOpenConns(1)

	store := &Store{db: db}
	if err := createschema(ctx, db); err != nil {
		_ = db.Close()

		return nil, err
	}

	return store, nil
}

func createschema(ctx context.Context, db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS market_securities (
			provider TEXT NOT NULL,
			security_id TEXT NOT NULL,
			symbol TEXT NOT NULL,
			name TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (provider, security_id),
			UNIQUE (provider, symbol)
		)`,
		`CREATE TABLE IF NOT EXISTS market_prices (
			provider TEXT NOT NULL,
			security_id TEXT NOT NULL,
			observation_date TEXT NOT NULL,
			currency TEXT NOT NULL,
			open REAL NOT NULL,
			close REAL NOT NULL,
			adjusted_close REAL NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (provider, security_id, observation_date),
			FOREIGN KEY (provider, security_id)
				REFERENCES market_securities (provider, security_id)
		)`,
		`CREATE INDEX IF NOT EXISTS market_prices_date
			ON market_prices (provider, observation_date, security_id)`,
		`CREATE TABLE IF NOT EXISTS market_corporate_actions (
			provider TEXT NOT NULL,
			security_id TEXT NOT NULL,
			source_id TEXT NOT NULL,
			action_date TEXT NOT NULL,
			action_type TEXT NOT NULL,
			amount REAL,
			numerator REAL,
			denominator REAL,
			reconciled_at TEXT,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (provider, security_id, source_id),
			FOREIGN KEY (provider, security_id)
				REFERENCES market_securities (provider, security_id)
		)`,
		`CREATE INDEX IF NOT EXISTS market_actions_date
			ON market_corporate_actions
			(provider, security_id, action_date)`,
		`CREATE TABLE IF NOT EXISTS market_sync_state (
			provider TEXT NOT NULL,
			security_id TEXT NOT NULL,
			bootstrap_complete INTEGER NOT NULL DEFAULT 0,
			covered_through TEXT,
			observed_from TEXT,
			observed_through TEXT,
			reconciliation_required INTEGER NOT NULL DEFAULT 0,
			next_attempt_at TEXT,
			failure_count INTEGER NOT NULL DEFAULT 0,
			last_error TEXT,
			lease_until TEXT,
			updated_at TEXT NOT NULL,
			PRIMARY KEY (provider, security_id),
			FOREIGN KEY (provider, security_id)
				REFERENCES market_securities (provider, security_id)
		)`,
		`CREATE TABLE IF NOT EXISTS market_repairs (
			provider TEXT NOT NULL,
			security_id TEXT NOT NULL,
			from_date TEXT NOT NULL,
			through_date TEXT NOT NULL,
			completed_at TEXT,
			created_at TEXT NOT NULL,
			PRIMARY KEY
				(provider, security_id, from_date, through_date),
			FOREIGN KEY (provider, security_id)
				REFERENCES market_securities (provider, security_id)
		)`,
	}

	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		return fmt.Errorf("enable market database foreign keys: %w", err)
	}

	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply market database schema: %w", err)
		}
	}

	return nil
}

// Close releases the database connection.
func (store *Store) Close() error {
	return store.db.Close()
}

// RegisterSecurities upserts the current provider mappings from a manifest.
func (store *Store) RegisterSecurities(ctx context.Context, provider string, securities []Security, now time.Time) error {
	if provider == "" {
		return errors.New("market provider is required")
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin security registration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, security := range securities {
		if err := validateSecurity(security); err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO market_securities
				(provider, security_id, symbol, name, updated_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(provider, security_id) DO UPDATE SET
				symbol=excluded.symbol,
				name=excluded.name,
				updated_at=excluded.updated_at`,
			provider, security.ID, security.Symbol, security.Name,
			formatTime(now)); err != nil {
			return fmt.Errorf("register security %s: %w", security.Symbol, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit security registration: %w", err)
	}

	return nil
}

// PendingWork returns incomplete work for the supplied desired universe.
func (store *Store) PendingWork(
	ctx context.Context,
	provider string,
	securities []Security,
	bootstrapFrom time.Time,
	through time.Time,
) ([]WorkItem, error) {
	if through.Before(bootstrapFrom) {
		return nil, errors.New("work target precedes bootstrap start")
	}

	items := make([]WorkItem, 0, len(securities))

	for _, security := range securities {
		item, pending, err := pendingSecurity(
			ctx, store, provider, security, bootstrapFrom, through,
		)
		if err != nil {
			return nil, err
		}

		if pending {
			items = append(items, item)
		}
	}

	sort.Slice(items, func(left, right int) bool {
		leftPriority := workPriority(items[left].Kind)
		rightPriority := workPriority(items[right].Kind)

		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}

		if !items[left].EligibleAt.Equal(items[right].EligibleAt) {
			return items[left].EligibleAt.Before(items[right].EligibleAt)
		}

		return items[left].Security.Symbol < items[right].Security.Symbol
	})

	return items, nil
}

// pendingSecurity determines if there is pending work for a given security and returns the corresponding work item if applicable.
func pendingSecurity(
	ctx context.Context,
	store *Store,
	provider string,
	security Security,
	bootstrapFrom time.Time,
	through time.Time,
) (WorkItem, bool, error) {
	state, err := store.loadSyncState(ctx, provider, security.ID)
	if err != nil {
		return WorkItem{}, false, err
	}

	hasPrices, err := store.hasPrices(ctx, provider, security.ID)
	if err != nil {
		return WorkItem{}, false, err
	}

	item := WorkItem{
		Provider:     provider,
		Security:     security,
		Through:      through,
		EligibleAt:   state.nextAttempt,
		LeaseUntil:   state.leaseUntil,
		FailureCount: state.failureCount,
		LastError:    state.lastError,
	}

	switch {
	case !hasPrices || !state.bootstrapComplete:
		item.Kind = WorkBootstrap
		item.From = bootstrapFrom
	case state.reconciliationRequired:
		item.Kind = WorkReconcile
		item.From = bootstrapFrom
	case state.repairFrom.Valid:
		item.Kind = WorkRepair
		item.From = mustDate(state.repairFrom.String)
		item.Through = mustDate(state.repairThrough.String)
	case state.coveredThrough == "" || state.coveredThrough < formatDate(through):
		item.Kind = WorkIncremental
		item.From = bootstrapFrom

		if state.observedThrough != "" {
			// Re-fetch the most recent observation. This catches a provider
			// correction or a candle that was incomplete when first seen.
			item.From = mustDate(state.observedThrough)
		} else if state.coveredThrough != "" {
			item.From = mustDate(state.coveredThrough)
		}
	default:
		return WorkItem{}, false, nil
	}

	return item, true, nil
}

// Claim obtains a temporary lease for one work item.
func (store *Store) Claim(ctx context.Context, item WorkItem, now time.Time, leaseUntil time.Time) (bool, error) {
	result, err := store.db.ExecContext(ctx, `
		INSERT INTO market_sync_state
			(provider, security_id, lease_until, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(provider, security_id) DO UPDATE SET
			lease_until=excluded.lease_until,
			updated_at=excluded.updated_at
		WHERE market_sync_state.lease_until IS NULL
			OR market_sync_state.lease_until <= ?`,
		item.Provider, item.Security.ID, formatTime(leaseUntil), formatTime(now),
		formatTime(now))
	if err != nil {
		return false, fmt.Errorf("claim %s work for %s: %w", item.Kind,
			item.Security.Symbol, err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect work claim: %w", err)
	}

	return rows == 1, nil
}

// Save atomically upserts one work result and advances its sync state.
func (store *Store) Save(ctx context.Context, item WorkItem, prices []Price, actions []CorporateAction, now time.Time) (bool, error) {
	if err := validateBatch(item, prices, actions); err != nil {
		return false, err
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin market data transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, price := range prices {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO market_prices
				(provider, security_id, observation_date, currency, open,
				 close, adjusted_close, updated_at)
			VALUES (?, ?, ?, 'USD', ?, ?, ?, ?)
			ON CONFLICT(provider, security_id, observation_date) DO UPDATE SET
				currency=excluded.currency,
				open=excluded.open,
				close=excluded.close,
				adjusted_close=excluded.adjusted_close,
				updated_at=excluded.updated_at`,
			item.Provider, item.Security.ID, formatDate(price.Date), price.Open,
			price.Close, price.AdjustedClose, formatTime(now)); err != nil {
			return false, fmt.Errorf("upsert price for %s: %w",
				item.Security.Symbol, err)
		}
	}

	actionsChanged, err := saveActions(ctx, tx, item, actions, now)
	if err != nil {
		return false, err
	}

	observedFrom, observedThrough := priceBounds(prices)
	reconciliationRequired := actionsChanged &&
		item.Kind != WorkBootstrap && item.Kind != WorkReconcile

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO market_sync_state
			(provider, security_id, bootstrap_complete, covered_through,
			 observed_from, observed_through, reconciliation_required,
			 failure_count, updated_at)
		VALUES (?, ?, 1, ?, ?, ?, ?, 0, ?)
		ON CONFLICT(provider, security_id) DO UPDATE SET
			bootstrap_complete=1,
			covered_through=CASE
				WHEN market_sync_state.covered_through IS NULL
					OR market_sync_state.covered_through < excluded.covered_through
				THEN excluded.covered_through
				ELSE market_sync_state.covered_through END,
			observed_from=CASE
				WHEN excluded.observed_from IS NULL
				THEN market_sync_state.observed_from
				WHEN market_sync_state.observed_from IS NULL
					OR excluded.observed_from < market_sync_state.observed_from
				THEN excluded.observed_from
				ELSE market_sync_state.observed_from END,
			observed_through=CASE
				WHEN excluded.observed_through IS NULL
				THEN market_sync_state.observed_through
				WHEN market_sync_state.observed_through IS NULL
					OR excluded.observed_through > market_sync_state.observed_through
				THEN excluded.observed_through
				ELSE market_sync_state.observed_through END,
			reconciliation_required=CASE
				WHEN ? = 1 THEN 0
				WHEN market_sync_state.reconciliation_required = 1
					OR excluded.reconciliation_required = 1 THEN 1
				ELSE 0 END,
			next_attempt_at=NULL,
			failure_count=0,
			last_error=NULL,
			lease_until=NULL,
			updated_at=excluded.updated_at`,
		item.Provider, item.Security.ID, formatDate(item.Through),
		nullDate(observedFrom), nullDate(observedThrough),
		boolInt(reconciliationRequired), formatTime(now),
		boolInt(item.Kind == WorkReconcile)); err != nil {
		return false, fmt.Errorf("advance sync state for %s: %w",
			item.Security.Symbol, err)
	}

	switch item.Kind {
	case WorkRepair:
		if _, err := tx.ExecContext(ctx, `
			UPDATE market_repairs SET completed_at = ?
			WHERE provider = ? AND security_id = ? AND from_date = ?
				AND through_date = ?`,
			formatTime(now), item.Provider, item.Security.ID,
			formatDate(item.From), formatDate(item.Through)); err != nil {
			return false, fmt.Errorf("complete repair for %s: %w",
				item.Security.Symbol, err)
		}
	case WorkBootstrap, WorkReconcile:
		if _, err := tx.ExecContext(ctx, `
			UPDATE market_repairs SET completed_at = ?
			WHERE provider = ? AND security_id = ? AND completed_at IS NULL
				AND from_date >= ? AND through_date <= ?`,
			formatTime(now), item.Provider, item.Security.ID,
			formatDate(item.From), formatDate(item.Through)); err != nil {
			return false, fmt.Errorf("complete covered repairs for %s: %w",
				item.Security.Symbol, err)
		}
	case WorkIncremental:
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit market data for %s: %w",
			item.Security.Symbol, err)
	}

	return actionsChanged, nil
}

func saveActions(ctx context.Context, tx *sql.Tx, item WorkItem, actions []CorporateAction, now time.Time) (bool, error) {
	if item.Kind == WorkBootstrap || item.Kind == WorkReconcile {
		if _, err := tx.ExecContext(ctx, `DELETE FROM market_corporate_actions
			WHERE provider = ? AND security_id = ?`, item.Provider,
			item.Security.ID); err != nil {
			return false, fmt.Errorf("replace actions for %s: %w",
				item.Security.Symbol, err)
		}
	}

	changed := false

	for _, action := range actions {
		actionChanged, err := actionDiffers(ctx, tx, item, action)
		if err != nil {
			return false, err
		}

		changed = changed || actionChanged

		reconciledAt := any(nil)
		if item.Kind == WorkBootstrap || item.Kind == WorkReconcile {
			reconciledAt = formatTime(now)
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO market_corporate_actions
				(provider, security_id, source_id, action_date, action_type,
				 amount, numerator, denominator, reconciled_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(provider, security_id, source_id) DO UPDATE SET
				action_date=excluded.action_date,
				action_type=excluded.action_type,
				amount=excluded.amount,
				numerator=excluded.numerator,
				denominator=excluded.denominator,
				reconciled_at=CASE
					WHEN excluded.reconciled_at IS NOT NULL
					THEN excluded.reconciled_at
					WHEN market_corporate_actions.action_date
						!= excluded.action_date
						OR market_corporate_actions.action_type
						!= excluded.action_type
						OR market_corporate_actions.amount
						IS NOT excluded.amount
						OR market_corporate_actions.numerator
						IS NOT excluded.numerator
						OR market_corporate_actions.denominator
						IS NOT excluded.denominator
					THEN NULL
					ELSE market_corporate_actions.reconciled_at END,
				updated_at=excluded.updated_at`,
			item.Provider, item.Security.ID, action.SourceID,
			formatDate(action.Date), action.Type, nullableNumber(action.Amount),
			nullableNumber(action.Numerator), nullableNumber(action.Denominator),
			reconciledAt, formatTime(now)); err != nil {
			return false, fmt.Errorf("upsert action for %s: %w",
				item.Security.Symbol, err)
		}
	}

	return changed, nil
}

func actionDiffers(ctx context.Context, tx *sql.Tx, item WorkItem, action CorporateAction) (bool, error) {
	var (
		date, actionType               string
		amount, numerator, denominator sql.NullFloat64
	)

	err := tx.QueryRowContext(ctx, `
		SELECT action_date, action_type, amount, numerator, denominator
		FROM market_corporate_actions
		WHERE provider = ? AND security_id = ? AND source_id = ?`,
		item.Provider, item.Security.ID, action.SourceID).Scan(
		&date, &actionType, &amount, &numerator, &denominator,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}

	if err != nil {
		return false, fmt.Errorf("inspect action for %s: %w",
			item.Security.Symbol, err)
	}

	return date != formatDate(action.Date) || actionType != action.Type ||
		!sameNumber(amount, action.Amount) ||
		!sameNumber(numerator, action.Numerator) ||
		!sameNumber(denominator, action.Denominator), nil
}

// Fail records a retryable work failure and releases its lease.
func (store *Store) Fail(
	ctx context.Context,
	item WorkItem,
	workErr error,
	now time.Time,
) error {
	failureCount := item.FailureCount + 1
	delay := retryDelay(failureCount)

	message := workErr.Error()
	if len(message) > 1000 {
		message = message[:1000]
	}

	_, err := store.db.ExecContext(ctx, `
		INSERT INTO market_sync_state
			(provider, security_id, next_attempt_at, failure_count,
			 last_error, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider, security_id) DO UPDATE SET
			next_attempt_at=excluded.next_attempt_at,
			failure_count=excluded.failure_count,
			last_error=excluded.last_error,
			lease_until=NULL,
			updated_at=excluded.updated_at`,
		item.Provider, item.Security.ID, formatTime(now.Add(delay)),
		failureCount, message, formatTime(now))
	if err != nil {
		return fmt.Errorf("record failure for %s: %w", item.Security.Symbol, err)
	}

	return nil
}

// QueueRepair records a historical range for future repair work.
func (store *Store) QueueRepair(ctx context.Context, provider string, security Security, from time.Time, through time.Time, now time.Time) error {
	if through.Before(from) {
		return errors.New("repair target precedes repair start")
	}

	_, err := store.db.ExecContext(ctx, `
		INSERT INTO market_repairs
			(provider, security_id, from_date, through_date, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(provider, security_id, from_date, through_date)
		DO UPDATE SET completed_at=NULL`,
		provider, security.ID, formatDate(from), formatDate(through),
		formatTime(now))
	if err != nil {
		return fmt.Errorf("queue repair for %s: %w", security.Symbol, err)
	}

	return nil
}

// loadSyncState loads the current synchronization state for a given provider and security ID.
func (store *Store) loadSyncState(ctx context.Context, provider string, securityID string) (syncState, error) {
	var (
		state                                            syncState
		bootstrap, reconcile                             int
		covered, observed, nextAttempt, lease, lastError sql.NullString
	)

	err := store.db.QueryRowContext(ctx, `
		SELECT s.bootstrap_complete, s.covered_through, s.observed_through,
			s.reconciliation_required, s.next_attempt_at,
			s.failure_count, s.last_error, s.lease_until,
			(SELECT r.from_date FROM market_repairs r
			 WHERE r.provider = s.provider
				AND r.security_id = s.security_id
				AND r.completed_at IS NULL
			 ORDER BY r.from_date LIMIT 1),
			(SELECT r.through_date FROM market_repairs r
			 WHERE r.provider = s.provider
				AND r.security_id = s.security_id
				AND r.completed_at IS NULL
			 ORDER BY r.from_date LIMIT 1)
		FROM market_sync_state s
		WHERE s.provider = ? AND s.security_id = ?`,
		provider, securityID).Scan(
		&bootstrap, &covered, &observed, &reconcile, &nextAttempt,
		&state.failureCount, &lastError, &lease,
		&state.repairFrom, &state.repairThrough,
	)
	if errors.Is(err, sql.ErrNoRows) {
		repairFrom, repairThrough, repairErr := store.pendingRepair(
			ctx, provider, securityID,
		)
		state.repairFrom = repairFrom
		state.repairThrough = repairThrough

		return state, repairErr
	}

	if err != nil {
		return syncState{}, fmt.Errorf("load market sync state: %w", err)
	}

	state.bootstrapComplete = bootstrap != 0
	state.coveredThrough = covered.String
	state.observedThrough = observed.String
	state.reconciliationRequired = reconcile != 0
	state.lastError = lastError.String
	state.nextAttempt = parseOptionalTime(nextAttempt.String)
	state.leaseUntil = parseOptionalTime(lease.String)

	return state, nil
}

func (store *Store) pendingRepair(ctx context.Context, provider string, securityID string) (sql.NullString, sql.NullString, error) {
	var from, through sql.NullString

	err := store.db.QueryRowContext(ctx, `
		SELECT from_date, through_date FROM market_repairs
		WHERE provider = ? AND security_id = ? AND completed_at IS NULL
		ORDER BY from_date LIMIT 1`, provider, securityID).Scan(&from, &through)
	if errors.Is(err, sql.ErrNoRows) {
		return from, through, nil
	}

	if err != nil {
		return from, through, fmt.Errorf("load pending market repair: %w", err)
	}

	return from, through, nil
}

func (store *Store) hasPrices(
	ctx context.Context,
	provider string,
	securityID string,
) (bool, error) {
	var exists int

	if err := store.db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM market_prices
		WHERE provider = ? AND security_id = ?
	)`, provider, securityID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check stored market prices: %w", err)
	}

	return exists != 0, nil
}

func validateSecurity(security Security) error {
	if strings.TrimSpace(security.ID) == "" ||
		strings.TrimSpace(security.Symbol) == "" ||
		strings.TrimSpace(security.Name) == "" {
		return errors.New("security ID, symbol, and name are required")
	}

	return nil
}

func validateBatch(item WorkItem, prices []Price, actions []CorporateAction) error {
	fromDate := formatDate(item.From)
	throughDate := formatDate(item.Through)

	for _, price := range prices {
		priceDate := formatDate(price.Date)
		if priceDate < fromDate || priceDate > throughDate {
			return fmt.Errorf("price date %s is outside work range",
				priceDate)
		}

		if !positiveFinite(price.Open) || !positiveFinite(price.Close) ||
			!positiveFinite(price.AdjustedClose) {
			return fmt.Errorf("invalid price on %s", formatDate(price.Date))
		}
	}

	for _, action := range actions {
		if action.SourceID == "" ||
			(action.Type != "dividend" && action.Type != "split") {
			return errors.New("invalid corporate action identity or type")
		}

		switch action.Type {
		case "dividend":
			if !positiveFinite(action.Amount) || action.Numerator != 0 ||
				action.Denominator != 0 {
				return errors.New("invalid dividend values")
			}
		case "split":
			if !positiveFinite(action.Numerator) ||
				!positiveFinite(action.Denominator) || action.Amount != 0 {
				return errors.New("invalid split values")
			}
		}

		actionDate := formatDate(action.Date)
		if actionDate < fromDate || actionDate > throughDate {
			return fmt.Errorf("action date %s is outside work range",
				actionDate)
		}
	}

	return nil
}

func positiveFinite(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func priceBounds(prices []Price) (time.Time, time.Time) {
	var first, last time.Time

	for _, price := range prices {
		if first.IsZero() || price.Date.Before(first) {
			first = price.Date
		}

		if last.IsZero() || price.Date.After(last) {
			last = price.Date
		}
	}

	return first, last
}

func workPriority(kind WorkKind) int {
	switch kind {
	case WorkBootstrap:
		return 0
	case WorkRepair:
		return 1
	case WorkReconcile:
		return 2
	case WorkIncremental:
		return 3
	default:
		return 4
	}
}

func retryDelay(failureCount int) time.Duration {
	shift := min(max(failureCount-1, 0), 6)

	return time.Minute * time.Duration(1<<shift)
}

func nullableNumber(value float64) any {
	if value == 0 {
		return nil
	}

	return value
}

func sameNumber(stored sql.NullFloat64, value float64) bool {
	if value == 0 {
		return !stored.Valid
	}

	return stored.Valid && stored.Float64 == value
}

func nullDate(value time.Time) any {
	if value.IsZero() {
		return nil
	}

	return formatDate(value)
}

func boolInt(value bool) int {
	if value {
		return 1
	}

	return 0
}

func formatDate(value time.Time) string {
	return value.UTC().Format(dateLayout)
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseOptionalTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)

	return parsed
}

func mustDate(value string) time.Time {
	parsed, _ := time.Parse(dateLayout, value)

	return parsed
}
