package pollingworker

import (
	"errors"
	"testing"
)

func TestWorkFailureLifecycle(t *testing.T) {
	t.Parallel()

	store, err := NewDedupeServiceContext(t.Context(),
		t.TempDir()+"/polling.db")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })

	for want := 1; want <= 2; want++ {
		attempts, err := store.RecordWorkFailure(t.Context(), "download",
			"0000000001-26-000001", "edgar.filing.discovered.10-K",
			[]byte(`{"accession_number":"0000000001-26-000001"}`),
			errors.New("temporary failure"))
		if err != nil {
			t.Fatal(err)
		}

		if attempts != want {
			t.Fatalf("attempts = %d, want %d", attempts, want)
		}
	}

	failures, err := store.WorkFailures(t.Context(), "download")
	if err != nil {
		t.Fatal(err)
	}

	if len(failures) != 1 || failures[0].Attempts != 2 {
		t.Fatalf("unexpected failures: %+v", failures)
	}

	if err := store.ResolveWorkFailure(t.Context(), "download",
		"0000000001-26-000001"); err != nil {
		t.Fatal(err)
	}

	failures, err = store.WorkFailures(t.Context(), "download")
	if err != nil {
		t.Fatal(err)
	}

	if len(failures) != 0 {
		t.Fatalf("resolved failures still visible: %+v", failures)
	}
}

func TestRetryWorkFailuresPublishesUnresolvedItems(t *testing.T) {
	t.Parallel()

	store, err := NewDedupeServiceContext(t.Context(),
		t.TempDir()+"/polling.db")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })

	if _, err := store.RecordWorkFailure(t.Context(), "process",
		"0000000001-26-000001", "edgar.filing.downloaded.10-K",
		[]byte(`{"accession_number":"0000000001-26-000001"}`),
		errors.New("parse failure")); err != nil {
		t.Fatal(err)
	}

	publisher := &recordingPublisher{}

	count, err := store.RetryWorkFailures(t.Context(), "process", publisher)
	if err != nil {
		t.Fatal(err)
	}

	if count != 1 || publisher.messages != 1 {
		t.Fatalf("retried=%d published=%d, want 1 and 1", count,
			publisher.messages)
	}

	failures, err := store.WorkFailures(t.Context(), "process")
	if err != nil {
		t.Fatal(err)
	}

	if len(failures) != 1 || failures[0].Attempts != 0 ||
		failures[0].LastError != "retry queued" {
		t.Fatalf("unexpected queued retry: %+v", failures)
	}
}
