package main

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html/charset"

	"wingman.com/fetch-ecb/internal/ctxlog"
	pw "wingman.com/fetch-ecb/internal/pollingworker"
)

type FilingEvent = pw.FilingEvent

// Atom Feed XML Schema Mapping.
type AtomFeed struct {
	XMLName xml.Name    `xml:"feed"`
	Entries []AtomEntry `xml:"entry"`
}

type AtomEntry struct {
	Title   string    `xml:"title"`
	Updated time.Time `xml:"updated"`
	Link    struct {
		Href string `xml:"href,attr"`
	} `xml:"link"`
	Summary struct {
		Text string `xml:",chardata"`
	} `xml:"summary"`
	Category struct {
		Term string `xml:"term,attr"`
	} `xml:"category"`
}

// SEC Atom Parser / Fetcher Worker.
type SECFetcher struct {
	client    *http.Client
	userAgent string
	feedURL   string
}

func NewSECFetcher(userHeader string) *SECFetcher {
	return NewSECFetcherWithURL(userHeader, defaultFeedURL)
}

const defaultFeedURL = "https://www.sec.gov/cgi-bin/browse-edgar?action=getcurrent&type=&company=&datea=&dateb=&owner=include&count=100&output=atom"

func NewSECFetcherWithURL(userHeader, feedURL string) *SECFetcher {
	return &SECFetcher{
		client:    &http.Client{Timeout: 10 * time.Second},
		userAgent: userHeader,
		feedURL:   feedURL,
	}
}

// Regex helpers to extract SEC-specific fields out of summary/title markup.
var (
	accessionRegex       = regexp.MustCompile(`edgar/data/\d+/(\d{10}-\d{2}-\d{6})`)
	accessionDigitsRegex = regexp.MustCompile(`edgar/data/\d+/(\d{18})`)
	cikRegex             = regexp.MustCompile(`(?i)\bCIK\s*[:=]\s*([0-9]{1,10})`)
	formTypeRegex        = regexp.MustCompile(`(?i)\bForm Type\s*:\s*([^\s]+)`)
	titleCIKRegex        = regexp.MustCompile(`\(([0-9]{10})\)`)
	atomTagRegex         = regexp.MustCompile(`<[^>]+>`)
)

func (f *SECFetcher) FetchLatest(ctx context.Context) ([]pw.FilingEvent, error) {
	ctxlog.FromContext(ctx).Debug("fetching SEC Atom feed", "url", f.feedURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.feedURL, nil)
	if err != nil {
		return nil, err
	}

	// SEC strictly blocks requests missing proper User-Agent credentials
	req.Header.Set("User-Agent", f.userAgent)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed fetching feed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sec returned unexpected status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	decoder := xml.NewDecoder(bytes.NewReader(body))
	decoder.CharsetReader = charset.NewReaderLabel

	var feed AtomFeed
	if err := decoder.Decode(&feed); err != nil {
		return nil, fmt.Errorf("xml unmarshal error: %w", err)
	}

	var events []FilingEvent
	skipped := 0

	for _, entry := range feed.Entries {
		event, ok := parseAtomEntry(entry)
		if ok {
			events = append(events, event)
		} else {
			skipped++
		}
	}
	ctxlog.FromContext(ctx).Debug("SEC Atom feed parsed", "entries", len(feed.Entries),
		"events", len(events), "skipped", skipped)

	return events, nil
}

// Extracts metadata fields out of raw Atom HTML summary string.
func parseAtomEntry(entry AtomEntry) (FilingEvent, bool) {
	accMatch := accessionRegex.FindStringSubmatch(entry.Link.Href)
	accessionNo := ""
	if len(accMatch) >= 2 {
		accessionNo = accMatch[1]
	} else if digitsMatch := accessionDigitsRegex.FindStringSubmatch(entry.Link.Href); len(digitsMatch) >= 2 {
		digits := digitsMatch[1]
		accessionNo = digits[:10] + "-" + digits[10:12] + "-" + digits[12:]
	} else {
		return FilingEvent{}, false
	}

	summary := atomTagRegex.ReplaceAllString(html.UnescapeString(entry.Summary.Text), " ")

	cikMatch := cikRegex.FindStringSubmatch(summary)

	cik := ""
	if len(cikMatch) >= 2 {
		cik = cikMatch[1]
	} else if titleMatch := titleCIKRegex.FindStringSubmatch(entry.Title); len(titleMatch) >= 2 {
		cik = titleMatch[1]
	}

	formMatch := formTypeRegex.FindStringSubmatch(summary)

	formType := ""
	if len(formMatch) >= 2 {
		formType = strings.TrimSpace(formMatch[1])
	} else {
		formType = strings.TrimSpace(entry.Category.Term)
	}

	return FilingEvent{
		AccessionNumber: accessionNo,
		CIK:             cik,
		FormType:        formType,
		FilingDate:      entry.Updated.Format("2006-01-02"),
		SourceFeed:      "RSS",
		Timestamp:       entry.Updated,
	}, true
}
