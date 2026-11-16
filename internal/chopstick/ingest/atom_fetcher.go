package ingest

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html/charset"

	pipeline "wingman.com/chopstick/internal/chopstick/pipeline"
	"wingman.com/chopstick/internal/ctxlog"
	"wingman.com/chopstick/internal/edgar"
	"wingman.com/chopstick/internal/outbound"
)

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
	client      *http.Client
	userAgent   string
	feedURL     string
	deadLetters []pipeline.DeadLetter
	requestGate *edgar.RequestGate
}

func NewSECFetcher(userHeader string) *SECFetcher {
	return NewSECFetcherWithGate(userHeader, nil)
}

const defaultFeedURL = "https://www.sec.gov/cgi-bin/browse-edgar?action=getcurrent&type=&company=&datea=&dateb=&owner=include&count=100&output=atom"

func NewSECFetcherWithURL(userHeader, feedURL string) *SECFetcher {
	return newSECFetcher(userHeader, feedURL, nil)
}

// NewSECFetcherWithGate shares the application-wide SEC request budget.
func NewSECFetcherWithGate(userHeader string,
	requestGate *edgar.RequestGate,
) *SECFetcher {
	return newSECFetcher(userHeader, defaultFeedURL, requestGate)
}

func newSECFetcher(userHeader, feedURL string,
	requestGate *edgar.RequestGate,
) *SECFetcher {
	if requestGate == nil {
		requestGate = edgar.NewRequestGate()
	}

	return &SECFetcher{
		client:      outbound.NewClient(30 * time.Second),
		userAgent:   userHeader,
		feedURL:     feedURL,
		requestGate: requestGate,
	}
}

// Regex helpers to extract SEC-specific fields out of summary/title markup.
var (
	accessionRegex       = regexp.MustCompile(`edgar/data/\d+/(\d{10}-\d{2}-\d{6})`)
	accessionDigitsRegex = regexp.MustCompile(`edgar/data/\d+/(\d{18})`)
	cikRegex             = regexp.MustCompile(`(?i)\bCIK\s*[:=]\s*([0-9]{1,10})`)
	formTypeRegex        = regexp.MustCompile(`(?i)\bForm Type\s*:\s*([^\s]+)`)
	titleCIKRegex        = regexp.MustCompile(`\(([0-9]{10})\)`)
	titleCompanyRegex    = regexp.MustCompile(`^\s*.+?\s+-\s+(.*?)\s+\([0-9]{10}\)`)
	atomTagRegex         = regexp.MustCompile(`<[^>]+>`)
)

func (f *SECFetcher) FetchLatest(ctx context.Context) ([]pipeline.Event, error) {
	f.deadLetters = nil
	ctxlog.FromContext(ctx).Debug("fetching SEC Atom feed", "url", f.feedURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.feedURL, nil)
	if err != nil {
		return nil, err
	}

	// SEC strictly blocks requests missing proper User-Agent credentials
	req.Header.Set("User-Agent", f.userAgent)

	if err := f.requestGate.Wait(ctx); err != nil {
		return nil, err
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed fetching feed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		upstream := edgar.NewHTTPError(resp, f.feedURL)
		if delay := f.requestGate.Throttle(upstream.StatusCode,
			upstream.RetryAfter); delay > 0 {
			upstream.RetryAfter = delay
			ctxlog.FromContext(ctx).Warn(
				"SEC request blocked; request gate paused",
				"status", upstream.StatusCode, "retry_in", delay,
				"url", f.feedURL)
		}

		return nil, upstream
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

	var events []pipeline.Event

	skipped := 0

	for _, entry := range feed.Entries {
		event, ok := parseAtomEntry(entry)
		if ok {
			events = append(events, event)
		} else {
			skipped++

			f.deadLetters = append(f.deadLetters, pipeline.DeadLetter{
				FeedURL: f.feedURL,
				Title:   entry.Title,
				Link:    entry.Link.Href,
				Reason:  "entry could not be normalized",
			})
		}
	}

	ctxlog.FromContext(ctx).Debug("SEC Atom feed parsed", "entries", len(feed.Entries),
		"events", len(events), "skipped", skipped)

	return events, nil
}

func (f *SECFetcher) DeadLetters() []pipeline.DeadLetter {
	return append([]pipeline.DeadLetter(nil), f.deadLetters...)
}

// Extracts metadata fields out of raw Atom HTML summary string.
func parseAtomEntry(entry AtomEntry) (pipeline.Event, bool) {
	accMatch := accessionRegex.FindStringSubmatch(entry.Link.Href)

	var accessionNo string
	if len(accMatch) >= 2 {
		accessionNo = accMatch[1]
	} else if digitsMatch := accessionDigitsRegex.FindStringSubmatch(entry.Link.Href); len(digitsMatch) >= 2 {
		digits := digitsMatch[1]
		accessionNo = digits[:10] + "-" + digits[10:12] + "-" + digits[12:]
	} else {
		return pipeline.Event{}, false
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

	var formType string
	if len(formMatch) >= 2 {
		formType = strings.TrimSpace(formMatch[1])
	} else {
		formType = strings.TrimSpace(entry.Category.Term)
	}

	companyName := strings.TrimSpace(entry.Title)
	if companyMatch := titleCompanyRegex.FindStringSubmatch(entry.Title); len(companyMatch) >= 2 {
		companyName = strings.TrimSpace(companyMatch[1])
	}

	filingPath := atomFilingPath(entry.Link.Href, cik, accessionNo)

	return pipeline.Event{
		SchemaVersion:   pipeline.EventSchemaVersion,
		EventID:         "filing.discovered:" + accessionNo,
		AccessionNumber: accessionNo,
		CIK:             cik,
		CompanyName:     companyName,
		FormType:        formType,
		FilingDate:      entry.Updated.Format("2006-01-02"),
		FilingPath:      filingPath,
		IndexPath:       "",
		FilingURL:       entry.Link.Href,
		SourceFeed:      "ATOM",
		Timestamp:       time.Now().UTC(),
	}, true
}

func atomFilingPath(href, cik, accession string) string {
	parsed, err := url.Parse(href)
	if err == nil {
		archivePath := strings.TrimPrefix(parsed.Path, "/Archives/")
		if directory := path.Dir(archivePath); directory != "." {
			return path.Join(directory, accession+".txt")
		}
	}

	return path.Join("edgar/data", strings.TrimLeft(cik, "0"),
		accession+".txt")
}
