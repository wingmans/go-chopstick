package ingest

import (
	"context"
	"net/http"
	"time"

	pipeline "wingman.com/chopstick/internal/edgar/pipeline"
	"wingman.com/chopstick/internal/ingest/sec"
)

type AtomEntry = sec.AtomEntry

// SECFetcher adapts source notices to durable pipeline events.
type SECFetcher struct {
	source      *sec.AtomFetcher
	deadLetters []pipeline.DeadLetter
}

func NewSECFetcher(userHeader string) *SECFetcher {
	return &SECFetcher{source: sec.NewAtomFetcher(userHeader)}
}

func NewSECFetcherWithURL(userHeader, feedURL string) *SECFetcher {
	return &SECFetcher{
		source: sec.NewAtomFetcherWithURL(userHeader, feedURL),
	}
}

// NewSECFetcherWithGate shares the application-wide SEC request budget.
func NewSECFetcherWithGate(userHeader string,
	requestGate *sec.RequestGate,
) *SECFetcher {
	return &SECFetcher{
		source: sec.NewAtomFetcherWithGate(userHeader, requestGate),
	}
}

func newSECFetcherWithClient(client *http.Client, userHeader, feedURL string,
	requestGate *sec.RequestGate,
) *SECFetcher {
	return &SECFetcher{source: sec.NewAtomFetcherWithClient(
		client, userHeader, feedURL, requestGate,
	)}
}

func (fetcher *SECFetcher) FetchLatest(
	ctx context.Context,
) ([]pipeline.Event, error) {
	notices, err := fetcher.source.FetchLatest(ctx)
	if err != nil {
		return nil, err
	}

	events := make([]pipeline.Event, 0, len(notices))
	for _, notice := range notices {
		events = append(events, noticeEvent(notice))
	}

	rejected := fetcher.source.RejectedEntries()

	fetcher.deadLetters = make([]pipeline.DeadLetter, 0, len(rejected))
	for _, entry := range rejected {
		fetcher.deadLetters = append(fetcher.deadLetters,
			pipeline.DeadLetter{
				FeedURL: entry.FeedURL,
				Title:   entry.Title,
				Link:    entry.Link,
				Reason:  entry.Reason,
			})
	}

	return events, nil
}

func (fetcher *SECFetcher) DeadLetters() []pipeline.DeadLetter {
	return append([]pipeline.DeadLetter(nil), fetcher.deadLetters...)
}

func parseAtomEntry(entry AtomEntry) (pipeline.Event, bool) {
	notice, ok := sec.ParseAtomEntry(entry)
	if !ok {
		return pipeline.Event{}, false
	}

	return noticeEvent(notice), true
}

func noticeEvent(notice sec.FilingNotice) pipeline.Event {
	return pipeline.Event{
		SchemaVersion:   pipeline.EventSchemaVersion,
		EventID:         "filing.discovered:" + notice.AccessionNumber,
		AccessionNumber: notice.AccessionNumber,
		CIK:             notice.CIK,
		CompanyName:     notice.CompanyName,
		FormType:        notice.FormType,
		FilingDate:      notice.FilingDate,
		FilingPath:      notice.FilingPath,
		FilingURL:       notice.FilingURL,
		SourceFeed:      "ATOM",
		Timestamp:       time.Now().UTC(),
	}
}
