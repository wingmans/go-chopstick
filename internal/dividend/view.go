// Package dividend builds a company-level dividend register from local
// parsed filings.
package dividend

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
	Kind         string `json:"kind"`
	Period       string `json:"period,omitempty"`
	ContextID    string `json:"context_id,omitempty"`
	ContextStart string `json:"context_start,omitempty"`
	ContextEnd   string `json:"context_end,omitempty"`
	Value        string `json:"value"`
	Unit         string `json:"unit,omitempty"`
	Accession    string `json:"accession"`
	FormType     string `json:"form_type"`
	FilingDate   string `json:"filing_date"`
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

// Save writes one already-built company dividend view without discovering any
// other files.
func Save(parsedDir string, view View) (string, error) {
	if strings.TrimSpace(parsedDir) == "" || strings.TrimSpace(view.CIK) == "" {
		return "", errors.New("dividend view requires a parsed directory and CIK")
	}

	root := filepath.Join(parsedDir, canonicalCIK(view.CIK))

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

// Extract extracts dividend information from one parsed filing. It reads only
// the source path recorded by that filing and never discovers other files.
func Extract(ctx context.Context, parsedDir, filingsDir string,
	filing *edgar.ParsedFiling,
) ([]Event, []Observation, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	return filingData(parsedDir, filingsDir, filing)
}

// Merge replaces the contribution from filing with freshly extracted data.
// This makes reprocessing one accession idempotent without scanning a company
// directory.
func Merge(ctx context.Context, current View, parsedDir, filingsDir string,
	filing *edgar.ParsedFiling,
) (View, error) {
	if filing == nil {
		return View{}, errors.New("dividend view requires a filing")
	}

	events, observations, err := Extract(ctx, parsedDir, filingsDir, filing)
	if err != nil {
		return View{}, err
	}

	accession := filing.Metadata.Accession

	merged := View{
		SchemaVersion: SchemaVersion,
		CIK:           canonicalCIK(filing.Metadata.CIK),
		Company:       current.Company,
		Events:        make([]Event, 0, len(current.Events)+len(events)),
		Observations:  make([]Observation, 0, len(current.Observations)+len(observations)),
	}
	if merged.Company == "" && len(filing.Metadata.Filers) > 0 {
		merged.Company = filing.Metadata.Filers[0].Name
	}

	for _, event := range current.Events {
		sources := make([]Source, 0, len(event.Sources))
		for _, source := range event.Sources {
			if source.Accession != accession {
				sources = append(sources, source)
			}
		}

		if len(sources) > 0 {
			event.Sources = sources
			merged.Events = append(merged.Events, event)
		}
	}

	for _, observation := range current.Observations {
		if observation.Accession != accession {
			merged.Observations = append(merged.Observations, observation)
		}
	}

	merged.Events = append(merged.Events, events...)
	merged.Observations = append(merged.Observations, observations...)
	dedupeEvents(&merged)
	dedupeObservations(&merged)
	sort.Slice(merged.Observations, func(i, j int) bool {
		if merged.Observations[i].FilingDate != merged.Observations[j].FilingDate {
			return merged.Observations[i].FilingDate < merged.Observations[j].FilingDate
		}

		return merged.Observations[i].Accession < merged.Observations[j].Accession
	})

	return merged, nil
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

			observation := Observation{
				Kind: kind, Period: period, ContextID: fact.ContextRef,
				Value: fact.Value, Unit: fact.UnitRef,
				Accession:  filing.Metadata.Accession,
				FormType:   filing.Metadata.FormType,
				FilingDate: filing.Metadata.FilingDate,
			}
			if context, ok := contexts[fact.ContextRef]; ok {
				observation.ContextStart = context.StartDate
				observation.ContextEnd = context.EndDate
				if observation.ContextEnd == "" {
					observation.ContextEnd = context.Instant
				}
			}
			observations = append(observations, observation)
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

func dedupeObservations(view *View) {
	seen := make(map[string]struct{}, len(view.Observations))
	observations := make([]Observation, 0, len(view.Observations))

	for _, observation := range view.Observations {
		key := strings.Join([]string{
			observation.Kind, observation.Period, observation.ContextID,
			observation.ContextStart, observation.ContextEnd, observation.Value,
			observation.Unit, observation.Accession,
		}, "|")
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}
		observations = append(observations, observation)
	}

	view.Observations = observations
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
