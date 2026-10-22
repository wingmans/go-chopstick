package news

import (
	"testing"

	"wingman.com/fetch-ecb/internal/pollingworker"
)

func TestStoreOrdersByStreamSequenceAndFilters(t *testing.T) {
	t.Parallel()

	store, created, err := Open(t.Context(), t.TempDir()+"/feed.db")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })

	if !created {
		t.Fatal("new database was not reported as created")
	}

	for sequence, event := range map[uint64]pollingworker.FilingEvent{
		4: {
			AccessionNumber: "first", CIK: "1", FormType: "10-Q",
			FilingDate: "2026-01-01", SourceFeed: "ATOM",
		},
		9: {
			AccessionNumber: "second", CIK: "2", FormType: "10-K",
			FilingDate: "2026-01-02", SourceFeed: "BATCH",
		},
	} {
		if err := store.Apply(t.Context(), sequence,
			"event:"+event.AccessionNumber, event); err != nil {
			t.Fatal(err)
		}
	}

	items, err := store.List(t.Context(), Filter{FormType: "10-K"})
	if err != nil {
		t.Fatal(err)
	}

	if len(items) != 1 || items[0].Sequence != 9 {
		t.Fatalf("filtered items = %#v", items)
	}
}
