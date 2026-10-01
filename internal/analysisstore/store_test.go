package analysisstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	dividendview "wingman.com/fetch-ecb/internal/dividend"
	"wingman.com/fetch-ecb/internal/edgar"
	filingview "wingman.com/fetch-ecb/internal/filing"
)

func testStore(t *testing.T) *Store {
	t.Helper()

	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "analysis.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })

	return store
}

func TestFilingViewRoundTripAndReplacement(t *testing.T) {
	store := testStore(t)
	view := filingview.View{
		SchemaVersion: filingview.SchemaVersion,
		Metadata: filingview.Metadata{
			CIK: "789019", Accession: "0000000001", FormType: "10-Q",
			FilingDate: "2026-09-30", Company: "Example Corp.",
		},
		Counts: filingview.Counts{Facts: 3, Status: "complete"},
	}

	if err := store.SaveFilingView(t.Context(), view); err != nil {
		t.Fatal(err)
	}

	view.Counts.Facts = 4
	if err := store.SaveFilingView(t.Context(), view); err != nil {
		t.Fatal(err)
	}

	got, err := store.LoadFilingView(t.Context(), "0000789019", view.Metadata.Accession)
	if err != nil {
		t.Fatal(err)
	}

	if got.Metadata.CIK != "0000789019" || got.Counts.Facts != 4 {
		t.Fatalf("unexpected filing view: %+v", got)
	}
}

func TestParsedFilingRoundTrip(t *testing.T) {
	store := testStore(t)
	filing := &edgar.ParsedFiling{
		Metadata: edgar.FilingMetadata{
			CIK: "789019", Accession: "0000000002", FormType: "8-K",
			FilingDate: "2026-10-01",
		},
		Status: edgar.ParseComplete,
	}

	if err := store.SaveParsedFiling(t.Context(), filing); err != nil {
		t.Fatal(err)
	}

	got, err := store.LoadParsedFiling(t.Context(), "789019", filing.Metadata.Accession)
	if err != nil {
		t.Fatal(err)
	}

	if got.Metadata.CIK != "0000789019" || got.Metadata.FormType != "8-K" {
		t.Fatalf("unexpected parsed filing: %+v", got.Metadata)
	}
}

func TestDividendViewRoundTripAndIndexes(t *testing.T) {
	store := testStore(t)
	view := dividendview.View{
		SchemaVersion: dividendview.SchemaVersion,
		CIK:           "789019",
		Company:       "Example Corp.",
		Events: []dividendview.Event{{
			ID: "event-1", PayableDate: "2026-10-01", AmountPerShare: "0.50",
			Type: "cash_common", Status: "announced", Confidence: "high",
		}},
		Observations: []dividendview.Observation{
			{
				Kind: "cash_paid", Period: "2026-09-30", ContextID: "ctx-1",
				ContextStart: "2026-01-01", ContextEnd: "2026-09-30", Value: "100",
				Accession: "0000000001", FormType: "10-Q", FilingDate: "2026-09-30",
			},
			{
				Kind: "cash_paid", Period: "2026-09-30", ContextID: "ctx-2",
				ContextStart: "2025-01-01", ContextEnd: "2026-09-30", Value: "200",
				Accession: "0000000001", FormType: "10-Q", FilingDate: "2026-09-30",
			},
		},
	}

	if err := store.SaveDividendView(t.Context(), view); err != nil {
		t.Fatal(err)
	}

	got, err := store.LoadDividendView(t.Context(), "0000789019")
	if err != nil {
		t.Fatal(err)
	}

	if got.CIK != "0000789019" || len(got.Events) != 1 || len(got.Observations) != 2 {
		t.Fatalf("unexpected dividend view: %+v", got)
	}
}

func TestDividendViewFailureRollsBackAllRows(t *testing.T) {
	store := testStore(t)
	view := dividendview.View{
		SchemaVersion: dividendview.SchemaVersion,
		CIK:           "789019",
		Events: []dividendview.Event{
			{ID: "duplicate", Type: "cash_common", Status: "announced", Confidence: "high"},
			{ID: "duplicate", Type: "cash_common", Status: "announced", Confidence: "high"},
		},
	}

	if err := store.SaveDividendView(t.Context(), view); err == nil {
		t.Fatal("expected duplicate event failure")
	}

	if _, err := store.LoadDividendView(t.Context(), "0000789019"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed transaction error = %v, want os.ErrNotExist", err)
	}

	if _, err := store.LoadDividendView(t.Context(), "0000789019"); err == nil {
		t.Fatal("failed transaction left a dividend view behind")
	}
}
