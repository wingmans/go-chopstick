package marketstore

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkLifecycle(t *testing.T) {
	store := testStore(t)
	ctx := t.Context()
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	from := testDate(t, "2020-01-01")
	through := testDate(t, "2026-10-23")
	security := Security{
		ID: "0000789019", Symbol: "MSFT", Name: "Microsoft Corporation",
	}

	if err := store.RegisterSecurities(
		ctx, "yahoo", []Security{security}, now,
	); err != nil {
		t.Fatalf("register security: %v", err)
	}

	work := requireWork(t, store, security, from, through, WorkBootstrap)

	claimed, err := store.Claim(ctx, work, now, now.Add(time.Hour))
	if err != nil || !claimed {
		t.Fatalf("claim bootstrap: claimed=%v error=%v", claimed, err)
	}

	prices := []Price{
		{
			Date: testDate(t, "2020-01-02"), Open: new(10.0),
			Close: 11, AdjustedClose: 9,
		},
		{
			Date: through, Open: new(20.0), Close: 21,
			AdjustedClose: 19,
		},
	}
	actions := []CorporateAction{{
		SourceID: "dividend:1",
		Date:     testDate(t, "2024-02-14"),
		Type:     "dividend",
		Amount:   0.75,
	}}

	changed, err := store.Save(ctx, work, prices, actions, now)
	if err != nil {
		t.Fatalf("save bootstrap: %v", err)
	}

	if !changed {
		t.Fatal("bootstrap did not report its corporate action")
	}

	if pending := pendingWork(t, store, security, from, through); len(pending) != 0 {
		t.Fatalf("pending work after bootstrap = %+v", pending)
	}

	nextThrough := testDate(t, "2026-10-26")

	work = requireWork(t, store, security, from, nextThrough, WorkIncremental)
	if got := work.From.Format(dateLayout); got != "2026-10-23" {
		t.Fatalf("incremental start = %s, want 2026-10-23", got)
	}

	newAction := CorporateAction{
		SourceID: "dividend:2",
		Date:     testDate(t, "2026-10-26"),
		Type:     "dividend",
		Amount:   0.80,
	}

	changed, err = store.Save(ctx, work, []Price{{
		Date: nextThrough, Open: new(21.0), Close: 22, AdjustedClose: 20,
	}}, []CorporateAction{newAction}, now)
	if err != nil || !changed {
		t.Fatalf("save incremental: changed=%v error=%v", changed, err)
	}

	work = requireWork(t, store, security, from, nextThrough, WorkReconcile)
	if !work.From.Equal(from) {
		t.Fatalf("reconciliation starts at %s, want %s", work.From, from)
	}

	if err := store.QueueRepair(
		ctx, "yahoo", security, testDate(t, "2024-03-01"),
		testDate(t, "2024-03-08"), now,
	); err != nil {
		t.Fatalf("queue covered repair: %v", err)
	}

	_, err = store.Save(ctx, work, prices, append(actions, newAction), now)
	if err != nil {
		t.Fatalf("save reconciliation: %v", err)
	}

	if pending := pendingWork(t, store, security, from, nextThrough); len(pending) != 0 {
		t.Fatalf("pending work after reconciliation = %+v", pending)
	}

	olderFrom := testDate(t, "2019-01-01")

	work = requireWork(
		t, store, security, olderFrom, nextThrough, WorkBootstrap,
	)
	if !work.From.Equal(olderFrom) {
		t.Fatalf("historical backfill starts at %s, want %s", work.From, olderFrom)
	}
}

func TestRepairFailureAndDataLossRemainDetectable(t *testing.T) {
	store := testStore(t)
	ctx := t.Context()
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	from := testDate(t, "2020-01-01")
	through := testDate(t, "2026-10-23")
	security := Security{ID: "0000320193", Symbol: "AAPL", Name: "Apple Inc."}

	if err := store.RegisterSecurities(
		ctx, "yahoo", []Security{security}, now,
	); err != nil {
		t.Fatalf("register security: %v", err)
	}

	work := requireWork(t, store, security, from, through, WorkBootstrap)
	if _, err := store.Save(ctx, work, []Price{{
		Date: through, Open: new(10.0), Close: 11, AdjustedClose: 9,
	}}, nil, now); err != nil {
		t.Fatalf("save bootstrap: %v", err)
	}

	repairFrom := testDate(t, "2024-03-01")
	repairThrough := testDate(t, "2024-03-08")

	if err := store.QueueRepair(
		ctx, "yahoo", security, repairFrom, repairThrough, now,
	); err != nil {
		t.Fatalf("queue repair: %v", err)
	}

	work = requireWork(t, store, security, from, through, WorkRepair)
	if !work.From.Equal(repairFrom) || !work.Through.Equal(repairThrough) {
		t.Fatalf("repair range = %s through %s", work.From, work.Through)
	}

	if err := store.Fail(ctx, work, errors.New("temporary failure"), now); err != nil {
		t.Fatalf("record failure: %v", err)
	}

	work = requireWork(t, store, security, from, through, WorkRepair)
	if work.FailureCount != 1 || !work.EligibleAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("failed work = %+v", work)
	}

	if _, err := store.db.ExecContext(ctx, `DELETE FROM market_prices
		WHERE provider = 'yahoo' AND security_id = ?`, security.ID); err != nil {
		t.Fatalf("delete prices: %v", err)
	}

	work = requireWork(t, store, security, from, through, WorkBootstrap)
	if work.FailureCount != 1 {
		t.Fatalf("bootstrap after data loss lost failure state: %+v", work)
	}
}

func TestOpenRejectsUnversionedDatabase(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "market.db")

	database, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open old database: %v", err)
	}

	if _, err := database.ExecContext(t.Context(), `CREATE TABLE
		market_sync_state (provider TEXT NOT NULL)`); err != nil {
		t.Fatalf("create old database: %v", err)
	}

	if err := database.Close(); err != nil {
		t.Fatalf("close old database: %v", err)
	}

	_, err = Open(t.Context(), path)
	if err == nil {
		t.Fatal("Open() succeeded for an unversioned market database")
	}

	if !strings.Contains(err.Error(), "make rebuild-market") {
		t.Fatalf("error %q does not explain how to rebuild", err)
	}
}

func TestMarketSchemaRecordsCurrentVersion(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "market.db")

	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	database, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	var version int
	if err := database.QueryRowContext(
		t.Context(),
		"PRAGMA user_version",
	).Scan(&version); err != nil {
		t.Fatal(err)
	}

	if version != marketSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version,
			marketSchemaVersion)
	}
}

func TestSaveAllowsMissingPriceOpen(t *testing.T) {
	store := testStore(t)
	now := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	security := Security{ID: "msft", Symbol: "MSFT", Name: "Microsoft"}

	if err := store.RegisterSecurities(
		t.Context(), "yahoo", []Security{security}, now,
	); err != nil {
		t.Fatal(err)
	}

	item := WorkItem{
		Provider: "yahoo",
		Security: security,
		Kind:     WorkBootstrap,
		From:     testDate(t, "2026-10-23"),
		Through:  testDate(t, "2026-10-26"),
	}

	if _, err := store.Save(t.Context(), item, []Price{{
		Date:          item.Through,
		Close:         12,
		AdjustedClose: 10,
	}}, nil, now); err != nil {
		t.Fatalf("save nullable open: %v", err)
	}

	var nullOpens int
	if err := store.db.QueryRowContext(t.Context(), `SELECT COUNT(*)
		FROM market_prices WHERE open IS NULL`).Scan(&nullOpens); err != nil {
		t.Fatal(err)
	}

	if nullOpens != 1 {
		t.Fatalf("null opens = %d, want 1", nullOpens)
	}
}

func testStore(t *testing.T) *Store {
	t.Helper()

	store, err := Open(
		context.Background(), filepath.Join(t.TempDir(), "market.db"),
	)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() { _ = store.Close() })

	return store
}

func requireWork(
	t *testing.T,
	store *Store,
	security Security,
	from time.Time,
	through time.Time,
	kind WorkKind,
) WorkItem {
	t.Helper()

	items := pendingWork(t, store, security, from, through)
	if len(items) != 1 || items[0].Kind != kind {
		t.Fatalf("pending work = %+v, want one %s item", items, kind)
	}

	return items[0]
}

func pendingWork(
	t *testing.T,
	store *Store,
	security Security,
	from time.Time,
	through time.Time,
) []WorkItem {
	t.Helper()

	items, err := store.PendingWork(
		t.Context(), "yahoo", []Security{security}, from, through,
	)
	if err != nil {
		t.Fatalf("plan work: %v", err)
	}

	return items
}

func testDate(t *testing.T, value string) time.Time {
	t.Helper()

	parsed, err := time.Parse(dateLayout, value)
	if err != nil {
		t.Fatalf("parse date: %v", err)
	}

	return parsed
}
