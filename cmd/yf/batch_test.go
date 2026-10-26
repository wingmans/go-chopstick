package main

import (
	"path/filepath"
	"strings"
	"testing"

	"wingman.com/fetch-ecb/internal/marketstore"
)

func TestCSVBatchStoreRoundTrip(t *testing.T) {
	store, err := newCSVBatchStore(t.TempDir())
	if err != nil {
		t.Fatalf("create CSV batch store: %v", err)
	}

	item := marketstore.WorkItem{
		Security: marketstore.Security{
			ID: "0000789019:MSFT", Symbol: "MSFT", Name: "Microsoft",
		},
		Kind:    marketstore.WorkBootstrap,
		From:    mustDate(t, "2024-01-01"),
		Through: mustDate(t, "2024-12-31"),
	}
	prices := []marketstore.Price{{
		Date:          mustDate(t, "2024-01-02"),
		Open:          10.25,
		Close:         11.5,
		AdjustedClose: 9.75,
	}}
	actions := []marketstore.CorporateAction{
		{
			SourceID: "dividend:1",
			Date:     mustDate(t, "2024-02-14"),
			Type:     "dividend",
			Amount:   0.75,
		},
		{
			SourceID:    "split:1",
			Date:        mustDate(t, "2024-06-01"),
			Type:        "split",
			Numerator:   2,
			Denominator: 1,
		},
	}

	path, err := store.Write(t.Context(), item, prices, actions)
	if err != nil {
		t.Fatalf("write CSV batch: %v", err)
	}

	wantPath := filepath.Join(
		store.root, "MSFT", "bootstrap_2024-01-01_2024-12-31.csv",
	)
	if path != wantPath {
		t.Fatalf("batch path = %q, want %q", path, wantPath)
	}

	gotPrices, gotActions, err := store.Read(t.Context(), item, path)
	if err != nil {
		t.Fatalf("read CSV batch: %v", err)
	}

	if len(gotPrices) != 1 || gotPrices[0] != prices[0] {
		t.Fatalf("round-trip prices = %+v", gotPrices)
	}

	if len(gotActions) != 2 || gotActions[0] != actions[0] ||
		gotActions[1] != actions[1] {
		t.Fatalf("round-trip actions = %+v", gotActions)
	}
}

func TestCSVBatchStoreRejectsWrongIdentity(t *testing.T) {
	store, err := newCSVBatchStore(t.TempDir())
	if err != nil {
		t.Fatalf("create CSV batch store: %v", err)
	}

	item := marketstore.WorkItem{
		Security: marketstore.Security{
			ID: "0000789019:MSFT", Symbol: "MSFT", Name: "Microsoft",
		},
		Kind:    marketstore.WorkIncremental,
		From:    mustDate(t, "2024-01-02"),
		Through: mustDate(t, "2024-01-02"),
	}

	path, err := store.Write(t.Context(), item, []marketstore.Price{{
		Date:          item.From,
		Open:          10,
		Close:         11,
		AdjustedClose: 9,
	}}, nil)
	if err != nil {
		t.Fatalf("write CSV batch: %v", err)
	}

	wrongItem := item
	wrongItem.Security.Symbol = "AAPL"

	_, _, err = store.Read(t.Context(), wrongItem, path)
	if err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("identity error = %v", err)
	}
}

func TestCSVBatchStoreRecoversContiguousPrefix(t *testing.T) {
	store, err := newCSVBatchStore(t.TempDir())
	if err != nil {
		t.Fatalf("create CSV batch store: %v", err)
	}

	security := marketstore.Security{
		ID: "0000789019:MSFT", Symbol: "MSFT", Name: "Microsoft",
	}
	bootstrap := marketstore.WorkItem{
		Security: security,
		Kind:     marketstore.WorkBootstrap,
		From:     mustDate(t, "2026-01-01"),
		Through:  mustDate(t, "2026-01-02"),
	}
	incremental := marketstore.WorkItem{
		Security: security,
		Kind:     marketstore.WorkIncremental,
		From:     mustDate(t, "2026-01-02"),
		Through:  mustDate(t, "2026-01-05"),
	}

	if _, err := store.Write(t.Context(), bootstrap, []marketstore.Price{{
		Date:          bootstrap.Through,
		Open:          10,
		Close:         11,
		AdjustedClose: 9,
	}}, nil); err != nil {
		t.Fatalf("write bootstrap: %v", err)
	}

	if _, err := store.Write(t.Context(), incremental, []marketstore.Price{{
		Date:          incremental.Through,
		Open:          12,
		Close:         13,
		AdjustedClose: 11,
	}}, nil); err != nil {
		t.Fatalf("write incremental: %v", err)
	}

	target := bootstrap
	target.Through = mustDate(t, "2026-01-10")

	recovery, err := store.Recover(t.Context(), target)
	if err != nil {
		t.Fatalf("recover CSV batches: %v", err)
	}

	if got := recovery.Through.Format(dateLayout); got != "2026-01-05" {
		t.Fatalf("recovered through = %s, want 2026-01-05", got)
	}

	if len(recovery.Paths) != 2 || len(recovery.Prices) != 2 {
		t.Fatalf("recovery = %+v", recovery)
	}
}
