package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pw "wingman.com/fetch-ecb/internal/pollingworker"
)

func TestParseAtomEntryAcceptsNumericAccessionPath(t *testing.T) {
	entry := AtomEntry{}
	entry.Link.Href = "https://www.sec.gov/Archives/edgar/data/320193/000032019326000001-index.htm"
	entry.Updated = time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	entry.Summary.Text = "CIK=0000320193 <b>Form Type:</b> 8-K"

	event, ok := parseAtomEntry(entry)
	if !ok {
		t.Fatal("parseAtomEntry rejected numeric accession path")
	}
	if event.AccessionNumber != "0000320193-26-000001" {
		t.Fatalf("accession = %q", event.AccessionNumber)
	}
}

func TestParseAtomEntryExtractsSECMetadata(t *testing.T) {
	entry := AtomEntry{}
	entry.Link.Href = "https://www.sec.gov/Archives/edgar/data/320193/000032019326000001-index.htm"
	entry.Summary.Text = "<b>Form Type:</b> 8-K<br><b>CIK:</b> 0000320193"

	event, ok := parseAtomEntry(entry)
	if !ok {
		t.Fatal("parseAtomEntry rejected SEC summary")
	}
	if event.CIK != "0000320193" {
		t.Fatalf("CIK = %q", event.CIK)
	}
	if event.FormType != "8-K" {
		t.Fatalf("form type = %q", event.FormType)
	}
}

func TestParseAtomEntryExtractsSECAtomMetadata(t *testing.T) {
	entry := AtomEntry{
		Title: "4 - Bernes Marshall (0002045034) (Reporting)",
	}
	entry.Link.Href = "https://www.sec.gov/Archives/edgar/data/2045034/000204503426000009/0002045034-26-000009-index.htm"
	entry.Category.Term = "4"

	event, ok := parseAtomEntry(entry)
	if !ok {
		t.Fatal("parseAtomEntry rejected SEC Atom entry")
	}
	if event.CIK != "0002045034" {
		t.Fatalf("CIK = %q", event.CIK)
	}
	if event.FormType != "4" {
		t.Fatalf("form type = %q", event.FormType)
	}
}

func TestPollingIngestionDeduplicatesFeedEntries(t *testing.T) {
	const feed = `<?xml version="1.0"?>
<feed>
  <entry>
    <updated>2026-09-28T10:00:00-04:00</updated>
    <link href="https://www.sec.gov/Archives/edgar/data/320193/000032019326000001/"/>
    <summary><![CDATA[CIK=0000320193 <b>Form Type:</b> 8-K]]></summary>
  </entry>
</feed>`

	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		if got := r.Header.Get("User-Agent"); got != "test-agent" {
			t.Errorf("User-Agent = %q, want %q", got, "test-agent")
		}

		w.Header().Set("Content-Type", "application/atom+xml")
		_, _ = w.Write([]byte(feed))
	}))
	defer server.Close()

	fetcher := NewSECFetcherWithURL("test-agent", server.URL)

	store, err := pw.NewSQLiteStore(t.TempDir() + "/polling.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	out := make(chan pw.FilingEvent, 2)
	worker := pw.NewWorker(fetcher, store, out, 0)

	if err := worker.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}

	if err := worker.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}

	select {
	case event := <-out:
		if event.AccessionNumber != "0000320193-26-000001" {
			t.Fatalf("accession = %q", event.AccessionNumber)
		}
	case <-t.Context().Done():
		t.Fatal("test context unexpectedly canceled")
	}

	select {
	case event := <-out:
		t.Fatalf("unexpected duplicate event: %+v", event)
	default:
	}
}
