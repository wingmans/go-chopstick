package pollingworker

import (
	"context"
	"errors"
	"testing"
)

type recordingPublisher struct {
	messages int
}

type failingPublisher struct{}

func (failingPublisher) Publish(context.Context, string, string, []byte) error {
	return errors.New("NATS unavailable")
}

func (p *recordingPublisher) Publish(_ context.Context, _, _ string,
	_ []byte,
) error {
	p.messages++

	return nil
}

func TestPublishFailureLeavesOutboxPending(t *testing.T) {
	t.Parallel()

	store, err := NewDedupeServiceContext(t.Context(),
		t.TempDir()+"/polling.db")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })

	if err := store.SaveAccessionContext(t.Context(), FilingEvent{
		AccessionNumber: "0000000001-26-000002",
		CIK:             "1",
		FormType:        "10-Q",
		SourceFeed:      "ATOM",
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.PublishPending(t.Context(), failingPublisher{}); err == nil {
		t.Fatal("PublishPending() succeeded, want failure")
	}

	pending, err := store.PendingOutbox(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if pending != 1 {
		t.Fatalf("pending outbox after failure = %d, want 1", pending)
	}
}

func TestSaveBatchCreatesOneOutboxMessagePerAccession(t *testing.T) {
	t.Parallel()

	store, err := NewDedupeServiceContext(t.Context(),
		t.TempDir()+"/polling.db")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })

	event := FilingEvent{
		AccessionNumber: "0000000001-26-000001",
		CIK:             "1",
		FormType:        "10-K",
		SourceFeed:      "ATOM",
	}
	if _, err := store.SaveBatchContext(t.Context(),
		[]FilingEvent{event, event}); err != nil {
		t.Fatal(err)
	}

	pending, err := store.PendingOutbox(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if pending != 1 {
		t.Fatalf("pending outbox = %d, want 1", pending)
	}

	publisher := &recordingPublisher{}
	if err := store.PublishPending(t.Context(), publisher); err != nil {
		t.Fatal(err)
	}

	if publisher.messages != 1 {
		t.Fatalf("published messages = %d, want 1", publisher.messages)
	}

	pending, err = store.PendingOutbox(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if pending != 0 {
		t.Fatalf("pending outbox after publish = %d, want 0", pending)
	}
}
