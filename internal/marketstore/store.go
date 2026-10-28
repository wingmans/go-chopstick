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

	"wingman.com/chopstick/internal/marketstore/dbgen"
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
	Open          *float64
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
	db      *sql.DB
	queries *dbgen.Queries
}

// syncState represents the synchronization state of a security with a provider.
type syncState struct {
	bootstrapComplete      bool
	coveredFrom            string
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

	db, err := sql.Open(
		"sqlite3",
		path+"?_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate"+
			"&_foreign_keys=on",
	)
	if err != nil {
		return nil, fmt.Errorf("open market database: %w", err)
	}

	db.SetMaxOpenConns(1)

	if err := createSchema(ctx, db); err != nil {
		_ = db.Close()

		return nil, err
	}

	queries, err := dbgen.Prepare(ctx, db)
	if err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("prepare market queries: %w", err)
	}

	return &Store{db: db, queries: queries}, nil
}

// Close releases the database connection.
func (store *Store) Close() error {
	return errors.Join(store.queries.Close(), store.db.Close())
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

	queries := store.queries.WithTx(tx)

	for _, security := range securities {
		if err := validateSecurity(security); err != nil {
			return err
		}

		if err := queries.RegisterSecurity(
			ctx,
			dbgen.RegisterSecurityParams{
				Provider:   provider,
				SecurityID: security.ID,
				Symbol:     security.Symbol,
				Name:       security.Name,
				UpdatedAt:  formatTime(now),
			},
		); err != nil {
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
	case state.coveredFrom == "" ||
		state.coveredFrom > formatDate(bootstrapFrom):
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
	result, err := store.queries.ClaimWork(ctx, dbgen.ClaimWorkParams{
		Provider:   item.Provider,
		SecurityID: item.Security.ID,
		LeaseUntil: validString(formatTime(leaseUntil)),
		UpdatedAt:  formatTime(now),
	})
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

	queries := store.queries.WithTx(tx)

	for _, price := range prices {
		if err := queries.UpsertPrice(ctx, dbgen.UpsertPriceParams{
			Provider:        item.Provider,
			SecurityID:      item.Security.ID,
			ObservationDate: formatDate(price.Date),
			Open:            nullablePrice(price.Open),
			Close:           price.Close,
			AdjustedClose:   price.AdjustedClose,
			UpdatedAt:       formatTime(now),
		}); err != nil {
			return false, fmt.Errorf("upsert price for %s: %w",
				item.Security.Symbol, err)
		}
	}

	actionsChanged, err := saveActions(ctx, queries, item, actions, now)
	if err != nil {
		return false, err
	}

	observedFrom, observedThrough := priceBounds(prices)
	coveredFrom := coverageStart(item)
	reconciliationRequired := actionsChanged &&
		item.Kind != WorkBootstrap && item.Kind != WorkReconcile

	if err := queries.AdvanceSyncState(ctx, dbgen.AdvanceSyncStateParams{
		Provider:               item.Provider,
		SecurityID:             item.Security.ID,
		CoveredFrom:            coveredFrom,
		CoveredThrough:         validString(formatDate(item.Through)),
		ObservedFrom:           nullDate(observedFrom),
		ObservedThrough:        nullDate(observedThrough),
		ReconciliationRequired: int64(boolInt(reconciliationRequired)),
		UpdatedAt:              formatTime(now),
	}); err != nil {
		return false, fmt.Errorf("advance sync state for %s: %w",
			item.Security.Symbol, err)
	}

	if item.Kind == WorkReconcile {
		if err := queries.ClearReconciliation(
			ctx,
			dbgen.ClearReconciliationParams{
				Provider:   item.Provider,
				SecurityID: item.Security.ID,
			},
		); err != nil {
			return false, fmt.Errorf("complete reconciliation for %s: %w",
				item.Security.Symbol, err)
		}
	}

	switch item.Kind {
	case WorkRepair:
		if err := queries.CompleteRepair(ctx, dbgen.CompleteRepairParams{
			CompletedAt: validString(formatTime(now)),
			Provider:    item.Provider,
			SecurityID:  item.Security.ID,
			FromDate:    formatDate(item.From),
			ThroughDate: formatDate(item.Through),
		}); err != nil {
			return false, fmt.Errorf("complete repair for %s: %w",
				item.Security.Symbol, err)
		}
	case WorkBootstrap, WorkReconcile:
		if err := queries.CompleteCoveredRepairs(
			ctx,
			dbgen.CompleteCoveredRepairsParams{
				CompletedAt: validString(formatTime(now)),
				Provider:    item.Provider,
				SecurityID:  item.Security.ID,
				FromDate:    formatDate(item.From),
				ThroughDate: formatDate(item.Through),
			},
		); err != nil {
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

func saveActions(
	ctx context.Context,
	queries *dbgen.Queries,
	item WorkItem,
	actions []CorporateAction,
	now time.Time,
) (bool, error) {
	if item.Kind == WorkBootstrap || item.Kind == WorkReconcile {
		if err := queries.DeleteActions(ctx, dbgen.DeleteActionsParams{
			Provider:   item.Provider,
			SecurityID: item.Security.ID,
		}); err != nil {
			return false, fmt.Errorf("replace actions for %s: %w",
				item.Security.Symbol, err)
		}
	}

	changed := false

	for _, action := range actions {
		actionChanged, err := actionDiffers(ctx, queries, item, action)
		if err != nil {
			return false, err
		}

		changed = changed || actionChanged

		var reconciledAt sql.NullString
		if item.Kind == WorkBootstrap || item.Kind == WorkReconcile {
			reconciledAt = validString(formatTime(now))
		}

		if err := queries.UpsertAction(ctx, dbgen.UpsertActionParams{
			Provider:     item.Provider,
			SecurityID:   item.Security.ID,
			SourceID:     action.SourceID,
			ActionDate:   formatDate(action.Date),
			ActionType:   action.Type,
			Amount:       nullableNumber(action.Amount),
			Numerator:    nullableNumber(action.Numerator),
			Denominator:  nullableNumber(action.Denominator),
			ReconciledAt: reconciledAt,
			UpdatedAt:    formatTime(now),
		}); err != nil {
			return false, fmt.Errorf("upsert action for %s: %w",
				item.Security.Symbol, err)
		}
	}

	return changed, nil
}

func actionDiffers(
	ctx context.Context,
	queries *dbgen.Queries,
	item WorkItem,
	action CorporateAction,
) (bool, error) {
	stored, err := queries.MarketAction(ctx, dbgen.MarketActionParams{
		Provider:   item.Provider,
		SecurityID: item.Security.ID,
		SourceID:   action.SourceID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}

	if err != nil {
		return false, fmt.Errorf("inspect action for %s: %w",
			item.Security.Symbol, err)
	}

	return stored.ActionDate != formatDate(action.Date) ||
		stored.ActionType != action.Type ||
		!sameNumber(stored.Amount, action.Amount) ||
		!sameNumber(stored.Numerator, action.Numerator) ||
		!sameNumber(stored.Denominator, action.Denominator), nil
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

	err := store.queries.RecordFailure(ctx, dbgen.RecordFailureParams{
		Provider:      item.Provider,
		SecurityID:    item.Security.ID,
		NextAttemptAt: validString(formatTime(now.Add(delay))),
		FailureCount:  int64(failureCount),
		LastError:     validString(message),
		UpdatedAt:     formatTime(now),
	})
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

	err := store.queries.QueueRepair(ctx, dbgen.QueueRepairParams{
		Provider:    provider,
		SecurityID:  security.ID,
		FromDate:    formatDate(from),
		ThroughDate: formatDate(through),
		CreatedAt:   formatTime(now),
	})
	if err != nil {
		return fmt.Errorf("queue repair for %s: %w", security.Symbol, err)
	}

	return nil
}

// loadSyncState loads the current synchronization state for a given provider and security ID.
func (store *Store) loadSyncState(ctx context.Context, provider string, securityID string) (syncState, error) {
	stored, err := store.queries.LoadSyncState(
		ctx,
		dbgen.LoadSyncStateParams{
			Provider:   provider,
			SecurityID: securityID,
		},
	)
	if errors.Is(err, sql.ErrNoRows) {
		return store.syncStateWithRepair(ctx, syncState{}, provider,
			securityID)
	}

	if err != nil {
		return syncState{}, fmt.Errorf("load market sync state: %w", err)
	}

	state := syncState{
		bootstrapComplete:      stored.BootstrapComplete != 0,
		coveredFrom:            stored.CoveredFrom.String,
		coveredThrough:         stored.CoveredThrough.String,
		observedThrough:        stored.ObservedThrough.String,
		reconciliationRequired: stored.ReconciliationRequired != 0,
		nextAttempt:            parseOptionalTime(stored.NextAttemptAt.String),
		leaseUntil:             parseOptionalTime(stored.LeaseUntil.String),
		failureCount:           int(stored.FailureCount),
		lastError:              stored.LastError.String,
	}

	return store.syncStateWithRepair(ctx, state, provider, securityID)
}

func (store *Store) syncStateWithRepair(
	ctx context.Context,
	state syncState,
	provider string,
	securityID string,
) (syncState, error) {
	repair, err := store.queries.PendingRepair(
		ctx,
		dbgen.PendingRepairParams{
			Provider:   provider,
			SecurityID: securityID,
		},
	)
	if errors.Is(err, sql.ErrNoRows) {
		return state, nil
	}

	if err != nil {
		return syncState{}, fmt.Errorf("load pending market repair: %w", err)
	}

	state.repairFrom = validString(repair.FromDate)
	state.repairThrough = validString(repair.ThroughDate)

	return state, nil
}

func (store *Store) hasPrices(
	ctx context.Context,
	provider string,
	securityID string,
) (bool, error) {
	exists, err := store.queries.HasPrices(ctx, dbgen.HasPricesParams{
		Provider:   provider,
		SecurityID: securityID,
	})
	if err != nil {
		return false, fmt.Errorf("check stored market prices: %w", err)
	}

	return exists, nil
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

		if (price.Open != nil && !positiveFinite(*price.Open)) ||
			!positiveFinite(price.Close) ||
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

func coverageStart(item WorkItem) sql.NullString {
	if item.Kind == WorkBootstrap || item.Kind == WorkReconcile {
		return validString(formatDate(item.From))
	}

	return sql.NullString{}
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

func nullableNumber(value float64) sql.NullFloat64 {
	if value == 0 {
		return sql.NullFloat64{}
	}

	return sql.NullFloat64{Float64: value, Valid: true}
}

func sameNumber(stored sql.NullFloat64, value float64) bool {
	if value == 0 {
		return !stored.Valid
	}

	return stored.Valid && stored.Float64 == value
}

func nullDate(value time.Time) sql.NullString {
	if value.IsZero() {
		return sql.NullString{}
	}

	return validString(formatDate(value))
}

func nullablePrice(value *float64) sql.NullFloat64 {
	if value == nil {
		return sql.NullFloat64{}
	}

	return sql.NullFloat64{Float64: *value, Valid: true}
}

func validString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: true}
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
