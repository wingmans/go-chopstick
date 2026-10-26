package marketworker

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"wingman.com/fetch-ecb/internal/marketstore"
)

type testFetcher struct {
	result FetchResult
	calls  int
}

func (fetcher *testFetcher) Fetch(
	_ context.Context,
	_ string,
	_ time.Time,
	_ time.Time,
) (FetchResult, error) {
	fetcher.calls++

	return fetcher.result, nil
}

type testBatches struct {
	result   FetchResult
	recovery Recovery
}

func (batches *testBatches) Write(
	_ context.Context,
	_ marketstore.WorkItem,
	prices []marketstore.Price,
	actions []marketstore.CorporateAction,
) (string, error) {
	batches.result = FetchResult{Prices: prices, Actions: actions}

	return "batch.csv", nil
}

func (batches *testBatches) Read(
	_ context.Context,
	_ marketstore.WorkItem,
	_ string,
) ([]marketstore.Price, []marketstore.CorporateAction, error) {
	return batches.result.Prices, batches.result.Actions, nil
}

func (batches *testBatches) Recover(
	_ context.Context,
	_ marketstore.WorkItem,
) (Recovery, error) {
	return batches.recovery, nil
}

func TestSyncCompletesRequestedUniverse(t *testing.T) {
	ctx := t.Context()
	store, security, cfg, now := testRepository(t)
	price := marketstore.Price{
		Date:          cfg.Through,
		Open:          10,
		Close:         11,
		AdjustedClose: 9,
	}
	fetcher := &testFetcher{result: FetchResult{
		Prices: []marketstore.Price{price},
	}}

	runner := Runner{
		Repository: store,
		Fetcher:    fetcher,
		Batches:    &testBatches{},
		Now:        func() time.Time { return now },
	}

	summary, err := runner.Sync(ctx, cfg)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}

	if fetcher.calls != 1 || summary.Pending != 0 || summary.Completed != 1 {
		t.Fatalf("fetch calls=%d summary=%+v", fetcher.calls, summary)
	}

	items, err := store.PendingWork(
		ctx,
		cfg.Provider,
		[]marketstore.Security{security},
		cfg.BootstrapFrom,
		cfg.Through,
	)
	if err != nil || len(items) != 0 {
		t.Fatalf("pending work=%+v error=%v", items, err)
	}
}

func TestSyncRecoversCSVBeforeFetching(t *testing.T) {
	store, _, cfg, now := testRepository(t)
	fetcher := &testFetcher{}
	batches := &testBatches{recovery: Recovery{
		From:    cfg.BootstrapFrom,
		Through: cfg.Through,
		Prices: []marketstore.Price{{
			Date:          cfg.Through,
			Open:          10,
			Close:         11,
			AdjustedClose: 9,
		}},
		Paths: []string{"bootstrap.csv"},
	}}

	runner := Runner{
		Repository: store,
		Fetcher:    fetcher,
		Batches:    batches,
		Now:        func() time.Time { return now },
	}

	summary, err := runner.Sync(t.Context(), cfg)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}

	if fetcher.calls != 0 || summary.Recovered != 1 || summary.Pending != 0 {
		t.Fatalf("fetch calls=%d summary=%+v", fetcher.calls, summary)
	}
}

func testRepository(
	t *testing.T,
) (*marketstore.Store, marketstore.Security, Config, time.Time) {
	t.Helper()

	store, err := marketstore.Open(
		t.Context(),
		filepath.Join(t.TempDir(), "market.db"),
	)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() { _ = store.Close() })

	now := time.Date(2026, 10, 26, 21, 0, 0, 0, time.UTC)
	security := marketstore.Security{
		ID: "0000789019:MSFT", Symbol: "MSFT", Name: "Microsoft",
	}

	if err := store.RegisterSecurities(
		t.Context(),
		"yahoo",
		[]marketstore.Security{security},
		now,
	); err != nil {
		t.Fatalf("register security: %v", err)
	}

	return store, security, Config{
		Provider:      "yahoo",
		Securities:    []marketstore.Security{security},
		BootstrapFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Through:       time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC),
		MaxItems:      25,
	}, now
}
