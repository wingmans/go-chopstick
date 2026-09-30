// Package dividendview builds a company-level dividend register from local
// parsed filings.
package dividendview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"wingman.com/fetch-ecb/internal/edgar"
)

const (
	SchemaVersion = 1
	datePattern   = `(?:January|February|March|April|May|June|July|August|September|October|November|December)\s+\d{1,2},\s+\d{4}`
)

type View struct {
	SchemaVersion int           `json:"schema_version"`
	CIK           string        `json:"cik"`
	Company       string        `json:"company"`
	Events        []Event       `json:"events"`
	Observations  []Observation `json:"observations"`
}

type Event struct {
	ID              string   `json:"id"`
	DeclarationDate string   `json:"declaration_date,omitempty"`
	ExDate          string   `json:"ex_dividend_date,omitempty"`
	RecordDate      string   `json:"record_date,omitempty"`
	PayableDate     string   `json:"payable_date,omitempty"`
	AmountPerShare  string   `json:"amount_per_share,omitempty"`
	Currency        string   `json:"currency,omitempty"`
	Type            string   `json:"type"`
	Status          string   `json:"status"`
	Confidence      string   `json:"confidence"`
	Sources         []Source `json:"sources"`
}

type Observation struct {
	Kind       string `json:"kind"`
	Period     string `json:"period,omitempty"`
	Value      string `json:"value"`
	Unit       string `json:"unit,omitempty"`
	Accession  string `json:"accession"`
	FormType   string `json:"form_type"`
	FilingDate string `json:"filing_date"`
}

type Source struct {
	Accession  string `json:"accession"`
	FormType   string `json:"form_type"`
	FilingDate string `json:"filing_date"`
	Document   string `json:"document,omitempty"`
	Evidence   string `json:"evidence,omitempty"`
}

var (
	tagPattern         = regexp.MustCompile(`<[^>]*>`)
	amountPattern      = regexp.MustCompile(`(?i)(?:dividend|dividends)[^$]{0,180}\$([0-9]+(?:\.[0-9]+)?)\s*(?:per|a)\s+share`)
	exDatePattern      = regexp.MustCompile(`(?i)ex[- ]dividend(?: date)?\s*(?:is|:)?\s*(` + datePattern + `)`)
	recordDatePattern  = regexp.MustCompile(`(?i)record date\s*(?:is|:)?\s*(` + datePattern + `)`)
	payableDatePattern = regexp.MustCompile(`(?i)payable(?: date)?\s*(?:is|on|:)?\s*(` + datePattern + `)`)
)

// Rebuild scans all parsed filings for a company and atomically writes its
// aggregate dividend view below the company's parsed-filing directory.
func Rebuild(ctx context.Context, parsedDir, filingsDir, cik string) (string, error) {
	if strings.TrimSpace(parsedDir) == "" || strings.TrimSpace(cik) == "" {
		return "", errors.New("dividend view requires a parsed directory and CIK")
	}

	canonical := canonicalCIK(cik)
	root := filepath.Join(parsedDir, canonical)
	view := View{
		SchemaVersion: SchemaVersion,
		CIK:           canonical,
		Events:        []Event{},
		Observations:  []Observation{},
	}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if err := ctx.Err(); err != nil {
			return err
		}

		if info.IsDir() || info.Name() != "filing.json" {
			return nil
		}

		filing, err := edgar.LoadParsedFiling(path)
		if err != nil {
			return fmt.Errorf("load %s: %w", path, err)
		}

		if canonicalCIK(filing.Metadata.CIK) != canonical {
			return nil
		}

		if view.Company == "" && len(filing.Metadata.Filers) > 0 {
			view.Company = filing.Metadata.Filers[0].Name
		}

		events, observations, err := filingData(parsedDir, filingsDir, filing)
		if err != nil {
			return fmt.Errorf("extract dividends from %s: %w", path, err)
		}

		view.Events = append(view.Events, events...)
		view.Observations = append(view.Observations, observations...)

		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return "", os.ErrNotExist
	}

	if err != nil {
		return "", err
	}

	dedupeEvents(&view)
	sort.Slice(view.Observations, func(i, j int) bool {
		if view.Observations[i].FilingDate != view.Observations[j].FilingDate {
			return view.Observations[i].FilingDate < view.Observations[j].FilingDate
		}

		return view.Observations[i].Accession < view.Observations[j].Accession
	})

	path := filepath.Join(root, "dividend-view.json")
	if err := os.MkdirAll(root, 0o750); err != nil {
		return "", fmt.Errorf("create dividend directory: %w", err)
	}

	file, err := os.CreateTemp(root, ".dividend-view-*.part")
	if err != nil {
		return "", fmt.Errorf("create dividend view: %w", err)
	}

	temporary := file.Name()
	defer func() { _ = file.Close(); _ = os.Remove(temporary) }()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(view); err != nil {
		return "", fmt.Errorf("encode dividend view: %w", err)
	}

	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close dividend view: %w", err)
	}

	if err := os.Rename(temporary, path); err != nil {
		return "", fmt.Errorf("store dividend view: %w", err)
	}

	return path, nil
}

func Load(path string) (View, error) {
	file, err := os.Open(path)
	if err != nil {
		return View{}, err
	}
	defer func() { _ = file.Close() }()

	var view View
	if err := json.NewDecoder(file).Decode(&view); err != nil {
		return View{}, fmt.Errorf("decode dividend view: %w", err)
	}

	if view.SchemaVersion != SchemaVersion {
		return View{}, fmt.Errorf("unsupported dividend view schema %d", view.SchemaVersion)
	}

	return view, nil
}

func filingData(parsedDir, filingsDir string, filing *edgar.ParsedFiling) ([]Event, []Observation, error) {
	var (
		events       []Event
		observations []Observation
	)

	for _, instance := range filing.Instances {
		contexts := make(map[string]edgar.FactContext, len(instance.Contexts))
		for _, context := range instance.Contexts {
			contexts[context.ID] = context
		}

		for _, fact := range instance.Facts {
			kind := ""

			switch fact.Concept.Local {
			case "CommonStockDividendsPerShareDeclared":
				kind = "per_share_declared"
			case "DividendsCommonStockCash", "PaymentsOfDividendsCommonStock":
				kind = "cash_paid"
			}

			if kind == "" {
				continue
			}

			period := ""
			if context, ok := contexts[fact.ContextRef]; ok {
				period = context.Instant
				if period == "" {
					period = context.EndDate
				}
			}

			observations = append(observations, Observation{
				Kind: kind, Period: period, Value: fact.Value,
				Unit: fact.UnitRef, Accession: filing.Metadata.Accession,
				FormType:   filing.Metadata.FormType,
				FilingDate: filing.Metadata.FilingDate,
			})
		}
	}

	source, err := sourcePath(parsedDir, filingsDir, filing)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return events, observations, nil
		}

		return nil, observations, err
	}

	file, err := os.Open(source) // #nosec G703 -- sourcePath validates the path.
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return events, observations, nil
		}

		return nil, observations, err
	}

	defer func() { _ = file.Close() }()

	for index, document := range filing.Documents {
		if document.ContentLength <= 0 {
			continue
		}

		data, err := io.ReadAll(io.NewSectionReader(file, document.ContentOffset,
			document.ContentLength))
		if err != nil {
			return nil, observations, err
		}

		text := cleanText(string(data))

		match := amountPattern.FindStringSubmatchIndex(text)
		if match == nil {
			continue
		}

		amount := text[match[2]:match[3]]
		events = append(events, eventFromText(filing, index, text, amount))
	}

	return events, observations, nil
}

func eventFromText(filing *edgar.ParsedFiling, document int, text, amount string) Event {
	source := Source{
		Accession: filing.Metadata.Accession,
		FormType:  filing.Metadata.FormType, FilingDate: filing.Metadata.FilingDate,
		Document: strconv.Itoa(document), Evidence: evidence(text),
	}
	event := Event{
		DeclarationDate: filing.Metadata.FilingDate, AmountPerShare: amount,
		Currency: "USD", Type: "cash_common", Status: "announced",
		Confidence: "medium", Sources: []Source{source},
	}
	event.ExDate = labeledDate(exDatePattern, text)
	event.RecordDate = labeledDate(recordDatePattern, text)
	event.PayableDate = labeledDate(payableDatePattern, text)
	event.ID = eventID(filing.Metadata.Accession, event)

	return event
}

func cleanText(value string) string {
	value = html.UnescapeString(tagPattern.ReplaceAllString(value, " "))

	return strings.Join(strings.Fields(value), " ")
}

func labeledDate(pattern *regexp.Regexp, text string) string {
	match := pattern.FindStringSubmatch(text)
	if len(match) == 2 {
		return match[1]
	}

	return ""
}

func evidence(text string) string {
	text = strings.TrimSpace(text)
	if len(text) > 320 {
		return text[:320] + "..."
	}

	return text
}

func eventID(accession string, event Event) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		accession,
		event.DeclarationDate, event.ExDate, event.RecordDate,
		event.PayableDate, event.AmountPerShare,
	}, "|")))

	return hex.EncodeToString(sum[:])[:16]
}

func dedupeEvents(view *View) {
	seen := make(map[string]int, len(view.Events))

	events := make([]Event, 0, len(view.Events))
	for _, event := range view.Events {
		if index, ok := seen[event.ID]; ok {
			events[index].Sources = append(events[index].Sources, event.Sources...)

			continue
		}

		seen[event.ID] = len(events)
		events = append(events, event)
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].DeclarationDate < events[j].DeclarationDate
	})
	view.Events = events
}

func sourcePath(parsedDir, filingsDir string, filing *edgar.ParsedFiling) (string, error) {
	switch filing.SourceBase {
	case "absolute":
		resolved, err := filepath.Abs(filing.SourcePath)
		if err != nil {
			return "", err
		}

		return resolved, nil
	case "filings":
		if filingsDir == "" {
			filingsDir = filepath.Join(filepath.Dir(parsedDir), "filings")
		}

		root, err := filepath.Abs(filingsDir)
		if err != nil {
			return "", err
		}

		source, err := filepath.Abs(filepath.Join(root,
			filepath.FromSlash(filing.SourcePath)))
		if err != nil {
			return "", err
		}

		relative, err := filepath.Rel(root, source)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return "", os.ErrNotExist
		}

		return source, nil
	default:
		return "", os.ErrNotExist
	}
}

func canonicalCIK(value string) string {
	value = strings.TrimSpace(value)

	value = strings.TrimLeft(value, "0")
	if value == "" {
		value = "0"
	}

	if len(value) > 10 {
		return ""
	}

	return strings.Repeat("0", 10-len(value)) + value
}
