// Package filingview builds the compact read model used by the local dashboard.
package filingview

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"wingman.com/fetch-ecb/internal/edgar"
)

const SchemaVersion = 1

type View struct {
	SchemaVersion   int                        `json:"schema_version"`
	ParserVersion   string                     `json:"parser_version"`
	TaxonomyVersion string                     `json:"taxonomy_version"`
	SourcePath      string                     `json:"source_path"`
	SourceSHA256    string                     `json:"source_sha256"`
	Metadata        Metadata                   `json:"metadata"`
	Documents       []edgar.SubmissionDocument `json:"documents"`
	Summary         []SummaryGroup             `json:"summary"`
	Statements      Statements                 `json:"statements"`
	Ratios          []RatioSeries              `json:"ratios"`
	Quality         Quality                    `json:"quality"`
	Counts          Counts                     `json:"counts"`
	Diagnostics     []edgar.ParseDiagnostic    `json:"diagnostics"`
}

type Metadata struct {
	Accession  string `json:"accession"`
	CIK        string `json:"cik"`
	FormType   string `json:"form_type"`
	FilingDate string `json:"filing_date"`
	ReportDate string `json:"report_date"`
	Company    string `json:"company"`
}

type Counts struct {
	Documents                int    `json:"documents"`
	Instances                int    `json:"instances"`
	Facts                    int    `json:"facts"`
	Contexts                 int    `json:"contexts"`
	DimensionalFactsExcluded int    `json:"dimensional_facts_excluded"`
	Status                   string `json:"status"`
}

type Quality struct {
	IdentityChecks []IdentityCheck `json:"identity_checks"`
}

type IdentityCheck struct {
	Name      string             `json:"name"`
	Period    string             `json:"period,omitempty"`
	Unit      string             `json:"unit,omitempty"`
	Status    string             `json:"status"`
	Expected  string             `json:"expected,omitempty"`
	Actual    string             `json:"actual,omitempty"`
	Tolerance string             `json:"tolerance,omitempty"`
	Metrics   []string           `json:"metrics,omitempty"`
	Evidence  []IdentityEvidence `json:"evidence,omitempty"`
	Message   string             `json:"message,omitempty"`
}

type IdentityEvidence struct {
	Metric     string `json:"metric"`
	Role       string `json:"role"`
	Sign       int64  `json:"sign,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	Concept    string `json:"concept,omitempty"`
	Value      string `json:"value,omitempty"`
	ContextRef string `json:"context_ref,omitempty"`
	Decimals   string `json:"decimals,omitempty"`
	UnitRef    string `json:"unit_ref,omitempty"`
}

type SummaryGroup struct {
	Title   string       `json:"title"`
	Periods []string     `json:"periods"`
	Rows    []FactSeries `json:"rows"`
}

type FactSeries struct {
	Key       string               `json:"key,omitempty"`
	Label     string               `json:"label"`
	Namespace string               `json:"namespace,omitempty"`
	Concept   string               `json:"concept"`
	Unit      string               `json:"unit"`
	Values    map[string]FactValue `json:"values"`
}

type FactValue struct {
	Value      string `json:"value"`
	Nil        bool   `json:"nil"`
	ContextRef string `json:"context_ref,omitempty"`
	Decimals   string `json:"decimals,omitempty"`
	UnitRef    string `json:"unit_ref,omitempty"`
	priority   int
	concept    edgar.QName
	id         string
	precision  string
}

type Statements struct {
	Income   StatementView `json:"income"`
	Balance  StatementView `json:"balance_sheet"`
	CashFlow StatementView `json:"cash_flow"`
}

type StatementView struct {
	Title  string         `json:"title"`
	Groups []SummaryGroup `json:"groups"`
}

type RatioSeries struct {
	Key     string            `json:"key"`
	Label   string            `json:"label"`
	Derived bool              `json:"derived"`
	Formula string            `json:"formula"`
	Values  map[string]string `json:"values"`
}

func Build(filing *edgar.ParsedFiling) (View, error) {
	if filing == nil {
		return View{}, errors.New("parsed filing is nil")
	}

	company := ""
	if len(filing.Metadata.Filers) > 0 {
		company = filing.Metadata.Filers[0].Name
	}

	facts := 0
	contexts := 0
	dimensionalFactsExcluded := 0

	for _, instance := range filing.Instances {
		facts += len(instance.Facts)
		contexts += len(instance.Contexts)
		dimensionalFactsExcluded += countDimensionalFacts(instance)
	}

	statements, ratios := buildStatements(filing)
	taxonomy := defaultTaxonomy()

	view := View{
		SchemaVersion:   SchemaVersion,
		ParserVersion:   filing.ParserVersion,
		TaxonomyVersion: taxonomy.TaxonomyVersion,
		SourcePath:      filing.SourcePath,
		SourceSHA256:    filing.SourceSHA256,
		Metadata: Metadata{
			Accession: filing.Metadata.Accession, CIK: filing.Metadata.CIK,
			FormType: filing.Metadata.FormType, FilingDate: filing.Metadata.FilingDate,
			ReportDate: filing.Metadata.ReportDate, Company: company,
		},
		Documents:  filing.Documents,
		Summary:    financialSummary(filing),
		Statements: statements,
		Ratios:     ratios,
		Counts: Counts{
			Documents: len(filing.Documents), Instances: len(filing.Instances),
			Facts: facts, Contexts: contexts,
			DimensionalFactsExcluded: dimensionalFactsExcluded,
			Status:                   filing.Status,
		},
		Diagnostics: filing.Diagnostics,
	}
	view.Quality = Quality{IdentityChecks: CheckAccountingIdentities(view)}

	return view, nil
}

func countDimensionalFacts(instance edgar.XBRLInstance) int {
	contexts := make(map[string]edgar.FactContext, len(instance.Contexts))
	for _, context := range instance.Contexts {
		contexts[context.ID] = context
	}

	count := 0

	for _, fact := range instance.Facts {
		context, ok := contexts[fact.ContextRef]
		if ok && len(context.Dimensions) > 0 {
			count++
		}
	}

	return count
}

func Path(directory string, filing *edgar.ParsedFiling) (string, error) {
	if filing == nil || directory == "" {
		return "", errors.New("filing view requires a filing and directory")
	}

	parsedPath, err := filing.ParsedPath(directory)
	if err != nil {
		return "", err
	}

	return filepath.Join(filepath.Dir(parsedPath), "filing-view.json"), nil
}

func Save(directory string, filing *edgar.ParsedFiling) (string, error) {
	view, err := Build(filing)
	if err != nil {
		return "", err
	}

	path, err := Path(directory, filing)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", fmt.Errorf("create filing view directory: %w", err)
	}

	temporary, err := os.CreateTemp(filepath.Dir(path), ".filing-view-*.part")
	if err != nil {
		return "", fmt.Errorf("create filing view temporary file: %w", err)
	}

	temporaryName := temporary.Name()
	defer func() { _ = temporary.Close(); _ = os.Remove(temporaryName) }()

	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(view); err != nil {
		return "", fmt.Errorf("encode filing view: %w", err)
	}

	if err := temporary.Sync(); err != nil {
		return "", fmt.Errorf("sync filing view: %w", err)
	}

	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("close filing view: %w", err)
	}

	if err := os.Rename(temporaryName, path); err != nil {
		return "", fmt.Errorf("store filing view: %w", err)
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
		return View{}, fmt.Errorf("decode filing view: %w", err)
	}

	if view.SchemaVersion != SchemaVersion {
		return View{}, fmt.Errorf("unsupported filing view schema %d", view.SchemaVersion)
	}

	return view, nil
}

func financialSummary(filing *edgar.ParsedFiling) []SummaryGroup {
	taxonomy := defaultTaxonomy()
	periodsByGroup := map[string]map[string]string{}
	seriesByGroup := map[string]map[string]*FactSeries{}

	for _, instance := range filing.Instances {
		units := unitMap(instance)

		contexts := make(map[string]edgar.FactContext, len(instance.Contexts))
		for _, context := range instance.Contexts {
			if len(context.Dimensions) == 0 {
				contexts[context.ID] = context
			}
		}

		for _, fact := range instance.Facts {
			definition, reference, ok := taxonomy.metricReferenceForConcept(fact.Concept.Namespace, fact.Concept.Local)
			if !ok {
				continue
			}

			context, ok := contexts[fact.ContextRef]
			if !ok {
				continue
			}

			group, periodKey, periodLabel := classifyPeriod(context, filing.Metadata.ReportDate)
			if group == "" {
				continue
			}

			unit := normalizedUnit(fact, units)
			key := definition.Key + "\x00" + unit

			if periodsByGroup[group] == nil {
				periodsByGroup[group] = map[string]string{}
				seriesByGroup[group] = map[string]*FactSeries{}
			}

			row := seriesByGroup[group][key]
			if row == nil {
				row = &FactSeries{
					Key: definition.Key, Label: definition.Label,
					Namespace: fact.Concept.Namespace, Concept: fact.Concept.Local,
					Unit: unit, Values: map[string]FactValue{},
				}
				seriesByGroup[group][key] = row
			}

			periodsByGroup[group][periodKey] = periodLabel
			selectedValue, selectedConcept := selectFact(row.Values[periodKey],
				edgar.QName{Namespace: row.Namespace, Local: row.Concept},
				selectedFact{Fact: fact, Priority: reference.Priority})
			row.Values[periodKey] = selectedValue
			row.Namespace = selectedConcept.Namespace
			row.Concept = selectedConcept.Local
		}
	}

	groups := make([]SummaryGroup, 0, len(periodsByGroup))
	for _, title := range []string{"Fiscal year", "Quarterly", "Year to date", "Instant"} {
		periods, ok := periodsByGroup[title]
		if !ok {
			continue
		}

		periodKeys := make([]string, 0, len(periods))
		for key := range periods {
			periodKeys = append(periodKeys, key)
		}

		sort.Sort(sort.Reverse(sort.StringSlice(periodKeys)))

		rows := make([]FactSeries, 0, len(seriesByGroup[title]))
		for _, row := range seriesByGroup[title] {
			rows = append(rows, *row)
		}

		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Label != rows[j].Label {
				return rows[i].Label < rows[j].Label
			}

			return rows[i].Unit < rows[j].Unit
		})
		groups = append(groups, SummaryGroup{Title: title, Periods: periodKeys, Rows: rows})
	}

	return groups
}

func classifyPeriod(context edgar.FactContext, reportDate string) (string, string, string) {
	if context.Instant != "" {
		return "Instant", displayDate(context.Instant), displayDate(context.Instant)
	}

	start, startErr := parseDate(context.StartDate)

	end, endErr := parseDate(context.EndDate)
	if startErr != nil || endErr != nil || start.After(end) {
		return "", "", ""
	}

	fiscalEnd, err := parseDate(reportDate)
	if err != nil {
		fiscalEnd = end
	}

	months := (end.Year()-start.Year())*12 + int(end.Month()) - int(start.Month()) + 1

	fiscalYear := end.Year()
	if months >= 10 {
		label := fmt.Sprintf("FY%d", fiscalYear)

		return "Fiscal year", label, label
	}

	if months == 3 {
		fiscalStartMonth := fiscalEnd.Month()%12 + 1
		label := fmt.Sprintf("Q%d FY%d", (int(start.Month())-int(fiscalStartMonth)+12)%12/3+1, fiscalYear)

		return "Quarterly", label, label
	}

	if months > 3 {
		label := fmt.Sprintf("YTD FY%d", fiscalYear)

		return "Year to date", label, label
	}

	return "", "", ""
}

func displayDate(value string) string {
	for _, layout := range []string{"2006-01-02", "20060102"} {
		if date, err := time.Parse(layout, value); err == nil {
			return date.Format("2006-01-02")
		}
	}

	return value
}

func parseDate(value string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02", "20060102"} {
		if date, err := time.Parse(layout, value); err == nil {
			return date, nil
		}
	}

	return time.Time{}, fmt.Errorf("invalid date %q", value)
}
