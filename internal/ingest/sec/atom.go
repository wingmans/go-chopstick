package sec

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

	"wingman.com/chopstick/internal/platform/ctxlog"
	"wingman.com/chopstick/internal/platform/outbound"
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

// FilingNotice is one filing discovered in the SEC Atom feed.
type FilingNotice struct {
	AccessionNumber string
	CIK             string
	CompanyName     string
	FormType        string
	FilingDate      string
	FilingPath      string
	FilingURL       string
}

// RejectedEntry records a feed entry that could not be normalized.
type RejectedEntry struct {
	FeedURL string
	Title   string
	Link    string
	Reason  string
}

// SEC Atom Parser / Fetcher Worker.
type AtomFetcher struct {
	client      *http.Client
	userAgent   string
	feedURL     string
	deadLetters []RejectedEntry
	requestGate *RequestGate
}

func NewAtomFetcher(userHeader string) *AtomFetcher {
	return NewAtomFetcherWithGate(userHeader, nil)
}

const defaultFeedURL = "https://www.sec.gov/cgi-bin/browse-edgar?action=getcurrent&type=&company=&datea=&dateb=&owner=include&count=100&output=atom"

func NewAtomFetcherWithURL(userHeader, feedURL string) *AtomFetcher {
	return newAtomFetcher(userHeader, feedURL, nil)
}

// NewAtomFetcherWithGate shares the application-wide SEC request budget.
func NewAtomFetcherWithGate(userHeader string,
	requestGate *RequestGate,
) *AtomFetcher {
	return newAtomFetcher(userHeader, defaultFeedURL, requestGate)
}

func newAtomFetcher(userHeader, feedURL string,
	requestGate *RequestGate,
) *AtomFetcher {
	if requestGate == nil {
		requestGate = NewRequestGate()
	}

	return &AtomFetcher{
		client:      outbound.NewClient(30 * time.Second),
		userAgent:   userHeader,
		feedURL:     feedURL,
		requestGate: requestGate,
	}
}

// NewAtomFetcherWithClient creates a fetcher with an injected client.
func NewAtomFetcherWithClient(client *http.Client, userHeader, feedURL string,
	requestGate *RequestGate,
) *AtomFetcher {
	fetcher := newAtomFetcher(userHeader, feedURL, requestGate)
	fetcher.client = client

	return fetcher
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

func (f *AtomFetcher) FetchLatest(
	ctx context.Context,
) ([]FilingNotice, error) {
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
		upstream := NewHTTPError(resp, f.feedURL)
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

	var notices []FilingNotice

	skipped := 0

	for _, entry := range feed.Entries {
		notice, ok := ParseAtomEntry(entry)
		if ok {
			notices = append(notices, notice)
		} else {
			skipped++

			f.deadLetters = append(f.deadLetters, RejectedEntry{
				FeedURL: f.feedURL,
				Title:   entry.Title,
				Link:    entry.Link.Href,
				Reason:  "entry could not be normalized",
			})
		}
	}

	ctxlog.FromContext(ctx).Debug("SEC Atom feed parsed", "entries", len(feed.Entries),
		"events", len(notices), "skipped", skipped)

	return notices, nil
}

// RejectedEntries returns entries rejected during the latest fetch.
func (f *AtomFetcher) RejectedEntries() []RejectedEntry {
	return append([]RejectedEntry(nil), f.deadLetters...)
}

// ParseAtomEntry extracts source metadata from one SEC Atom entry.
func ParseAtomEntry(entry AtomEntry) (FilingNotice, bool) {
	accMatch := accessionRegex.FindStringSubmatch(entry.Link.Href)

	var accessionNo string
	if len(accMatch) >= 2 {
		accessionNo = accMatch[1]
	} else if digitsMatch := accessionDigitsRegex.FindStringSubmatch(entry.Link.Href); len(digitsMatch) >= 2 {
		digits := digitsMatch[1]
		accessionNo = digits[:10] + "-" + digits[10:12] + "-" + digits[12:]
	} else {
		return FilingNotice{}, false
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

	return FilingNotice{
		AccessionNumber: accessionNo,
		CIK:             cik,
		CompanyName:     companyName,
		FormType:        formType,
		FilingDate:      entry.Updated.Format("2006-01-02"),
		FilingPath:      filingPath,
		FilingURL:       entry.Link.Href,
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
