package market

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	marketstore "wingman.com/chopstick/internal/market/store"
)

type testFetcher struct {
	result fetchResult
	calls  int
}

func (fetcher *testFetcher) Fetch(
	_ context.Context,
	_ string,
	_ time.Time,
	_ time.Time,
) (fetchResult, error) {
	fetcher.calls++

	return fetcher.result, nil
}

type testBatches struct {
	result   fetchResult
	recovery batchRecovery
}

func (batches *testBatches) Write(
	_ context.Context,
	_ marketstore.WorkItem,
	prices []marketstore.Price,
	actions []marketstore.CorporateAction,
) (string, error) {
	batches.result = fetchResult{Prices: prices, Actions: actions}

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
) (batchRecovery, error) {
	return batches.recovery, nil
}

func TestSyncCompletesRequestedUniverse(t *testing.T) {
	ctx := t.Context()
	store, security, cfg, now := testRepository(t)
	price := marketstore.Price{
		Date:          cfg.Through,
		Open:          new(10.0),
		Close:         11,
		AdjustedClose: 9,
	}
	fetcher := &testFetcher{result: fetchResult{
		Prices: []marketstore.Price{price},
	}}

	runner := syncRunner{
		Store:   store,
		Fetcher: fetcher,
		Batches: &testBatches{},
		Now:     func() time.Time { return now },
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

func TestSyncReturnsWithFailedWorkPending(t *testing.T) {
	store, _, cfg, now := testRepository(t)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	fetcher := &testFetcher{}
	runner := syncRunner{
		Store:   store,
		Fetcher: fetcher,
		Batches: &testBatches{},
		Now:     func() time.Time { return now },
	}

	summary, err := runner.Sync(ctx, cfg)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}

	if fetcher.calls != 1 || summary.Failed != 1 || summary.Pending != 1 {
		t.Fatalf("fetch calls=%d summary=%+v", fetcher.calls, summary)
	}
}

func TestSyncRecoversCSVBeforeFetching(t *testing.T) {
	store, _, cfg, now := testRepository(t)
	fetcher := &testFetcher{}
	batches := &testBatches{recovery: batchRecovery{
		From:    cfg.BootstrapFrom,
		Through: cfg.Through,
		Prices: []marketstore.Price{{
			Date:          cfg.Through,
			Open:          new(10.0),
			Close:         11,
			AdjustedClose: 9,
		}},
		Paths: []string{"bootstrap.csv"},
	}}

	runner := syncRunner{
		Store:   store,
		Fetcher: fetcher,
		Batches: batches,
		Now:     func() time.Time { return now },
	}

	summary, err := runner.Sync(t.Context(), cfg)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}

	if fetcher.calls != 0 || summary.Recovered != 1 || summary.Pending != 0 {
		t.Fatalf("fetch calls=%d summary=%+v", fetcher.calls, summary)
	}
}

func TestRunBatchRejectsSuspiciousPriceGap(t *testing.T) {
	store, security, cfg, now := testRepository(t)
	fetcher := &testFetcher{result: fetchResult{
		Prices: []marketstore.Price{
			testPrice(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)),
			testPrice(cfg.Through),
		},
	}}
	batches := &testBatches{}
	runner := syncRunner{
		Store:   store,
		Fetcher: fetcher,
		Batches: batches,
		Now:     func() time.Time { return now },
	}

	summary, err := runner.RunBatch(t.Context(), cfg)
	if err != nil {
		t.Fatalf("run batch: %v", err)
	}

	if summary.Failed != 1 || summary.Completed != 0 {
		t.Fatalf("summary = %+v", summary)
	}

	if len(batches.result.Prices) != 0 {
		t.Fatalf("invalid prices reached batch storage: %+v", batches.result)
	}

	items, err := store.PendingWork(
		t.Context(), cfg.Provider, []marketstore.Security{security},
		cfg.BootstrapFrom, cfg.Through,
	)
	if err != nil {
		t.Fatalf("pending work: %v", err)
	}

	if len(items) != 1 ||
		!strings.Contains(items[0].LastError, "suspicious gap") {
		t.Fatalf("pending work = %+v", items)
	}
}

func TestValidateFetchedPrices(t *testing.T) {
	from := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	item := marketstore.WorkItem{
		Kind:     marketstore.WorkIncremental,
		From:     from,
		Through:  from.AddDate(0, 0, 20),
		Security: marketstore.Security{Symbol: "AAPL"},
	}

	tests := []struct {
		name    string
		prices  []marketstore.Price
		wantErr string
	}{
		{
			name: "continuous",
			prices: []marketstore.Price{
				testPrice(from.AddDate(0, 0, 2)),
				testPrice(from.AddDate(0, 0, 6)),
				testPrice(from.AddDate(0, 0, 13)),
				testPrice(from.AddDate(0, 0, 20)),
			},
		},
		{
			name: "missing middle",
			prices: []marketstore.Price{
				testPrice(from),
				testPrice(from.AddDate(0, 0, 8)),
				testPrice(from.AddDate(0, 0, 20)),
			},
			wantErr: "suspicious gap",
		},
		{
			name: "stale tail",
			prices: []marketstore.Price{
				testPrice(from),
				testPrice(from.AddDate(0, 0, 7)),
			},
			wantErr: "is stale",
		},
		{
			name: "skipped incremental start",
			prices: []marketstore.Price{
				testPrice(from.AddDate(0, 0, 8)),
				testPrice(from.AddDate(0, 0, 15)),
				testPrice(from.AddDate(0, 0, 20)),
			},
			wantErr: "skips work start",
		},
		{
			name: "unordered",
			prices: []marketstore.Price{
				testPrice(from),
				testPrice(from),
				testPrice(from.AddDate(0, 0, 20)),
			},
			wantErr: "duplicate or unordered",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateFetchedPrices(item, test.prices)
			if test.wantErr == "" && err != nil {
				t.Fatalf("validate prices: %v", err)
			}

			if test.wantErr != "" &&
				(err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func testPrice(date time.Time) marketstore.Price {
	return marketstore.Price{
		Date: date, Open: new(10.0), Close: 11, AdjustedClose: 9,
	}
}

func testRepository(
	t *testing.T,
) (*marketstore.Store, marketstore.Security, syncConfig, time.Time) {
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
		CIK: "0000789019", Symbol: "MSFT", Name: "Microsoft",
	}

	if err := store.RegisterSecurities(
		t.Context(),
		"yahoo",
		[]marketstore.Security{security},
		now,
	); err != nil {
		t.Fatalf("register security: %v", err)
	}

	return store, security, syncConfig{
		Provider:      "yahoo",
		Securities:    []marketstore.Security{security},
		BootstrapFrom: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Through:       time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC),
		MaxItems:      25,
	}, now
}
