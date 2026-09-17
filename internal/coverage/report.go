// Package coverage reports expected local EDGAR processing coverage.
package coverage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"wingman.com/fetch-ecb/internal/constituents"
	"wingman.com/fetch-ecb/internal/edgar"
	"wingman.com/fetch-ecb/internal/filingview"
)

const SchemaVersion = 1

type Config struct {
	MasterPath  string
	IndexesDir  string
	FilingsDir  string
	ParsedDir   string
	Filter      edgar.IndexFilter
	SetName     string
	FromYear    int
	ToYear      int
	Taxonomy    filingview.Taxonomy
	GeneratedAt time.Time
}

type Report struct {
	SchemaVersion int                  `json:"schema_version"`
	GeneratedAt   string               `json:"generated_at"`
	Selection     Selection            `json:"selection"`
	Summary       Summary              `json:"summary"`
	Filings       []Filing             `json:"filings"`
	Acquisition   []AcquisitionRequest `json:"acquisition"`
}

type Selection struct {
	SetName   string   `json:"set"`
	CIK       string   `json:"cik,omitempty"`
	CIKs      []string `json:"ciks,omitempty"`
	FormTypes []string `json:"form_types,omitempty"`
	FromYear  int      `json:"from_year,omitempty"`
	ToYear    int      `json:"to_year,omitempty"`
}

type Summary struct {
	Expected             int            `json:"expected"`
	Downloaded           int            `json:"downloaded"`
	Parsed               int            `json:"parsed"`
	Missing              int            `json:"missing"`
	Unparsed             int            `json:"unparsed"`
	MissingMetrics       int            `json:"filings_with_missing_metrics"`
	MissingMetricsByTier map[string]int `json:"filings_with_missing_metrics_by_tier,omitempty"`
}

type Filing struct {
	CIK           string              `json:"cik"`
	Company       string              `json:"company"`
	Accession     string              `json:"accession"`
	FormType      string              `json:"form_type"`
	DateFiled     string              `json:"date_filed"`
	FilingPath    string              `json:"filing_path"`
	SourcePath    string              `json:"source_path"`
	ParsedPath    string              `json:"parsed_path"`
	Status        string              `json:"status"`
	MissingMetric []string            `json:"missing_metrics,omitempty"`
	MissingByTier map[string][]string `json:"missing_metrics_by_tier,omitempty"`
	Metrics       map[string]string   `json:"metrics,omitempty"`
}

type AcquisitionRequest struct {
	Kind        string   `json:"kind"`
	Year        int      `json:"year"`
	Quarters    []int    `json:"quarters,omitempty"`
	CIK         string   `json:"cik,omitempty"`
	FormTypes   []string `json:"form_types,omitempty"`
	Years       []int    `json:"years,omitempty"`
	Description string   `json:"description"`
}

// Build generates a coverage report based on the provided configuration.
func Build(ctx context.Context, config Config) (Report, error) {
	if config.MasterPath == "" {
		return Report{}, errors.New("master index is required")
	}

	if config.FilingsDir == "" || config.ParsedDir == "" {
		return Report{}, errors.New("filings and parsed directories are required")
	}

	if config.Taxonomy.TaxonomyVersion == "" {
		return Report{}, errors.New("taxonomy is required")
	}

	if config.FromYear != 0 && config.ToYear != 0 && config.FromYear > config.ToYear {
		return Report{}, errors.New("from year must not be after to year")
	}

	file, err := os.Open(config.MasterPath)
	if err != nil {
		return Report{}, fmt.Errorf("open master index: %w", err)
	}
	defer func() { _ = file.Close() }()

	selection := Selection{
		SetName: config.SetName, CIK: config.Filter.CIK,
		CIKs:      append([]string(nil), config.Filter.CIKs...),
		FormTypes: append([]string(nil), config.Filter.FormTypes...),
		FromYear:  config.FromYear, ToYear: config.ToYear,
	}

	report := Report{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   config.GeneratedAt.UTC().Format(time.RFC3339),
		Selection:     selection,
		Summary: Summary{
			Expected: 0, Downloaded: 0, Parsed: 0, Missing: 0,
			Unparsed: 0, MissingMetrics: 0, MissingMetricsByTier: nil,
		},
		Filings:     nil,
		Acquisition: nil,
	}
	if config.GeneratedAt.IsZero() {
		report.GeneratedAt = ""
	}

	reader := edgar.NewIndexReader(file, config.Filter)
	seen := make(map[string]struct{})

	for {
		if err := ctx.Err(); err != nil {
			return Report{}, err
		}

		record, readErr := reader.Next()
		if errors.Is(readErr, io.EOF) {
			break
		}

		if readErr != nil {
			return Report{}, fmt.Errorf("read master index: %w", readErr)
		}

		if !inYearRange(record.DateFiled.Year(), config.FromYear, config.ToYear) {
			continue
		}

		key := record.CIK + "\x00" + record.FilingPath
		if _, exists := seen[key]; exists {
			continue
		}

		seen[key] = struct{}{}

		filing, inspectErr := inspectFiling(ctx, config, record)
		if inspectErr != nil {
			return Report{}, inspectErr
		}

		report.Filings = append(report.Filings, filing)
		report.Summary.Expected++

		switch filing.Status {
		case "parsed":
			report.Summary.Downloaded++
			report.Summary.Parsed++
		case "downloaded":
			report.Summary.Downloaded++
			report.Summary.Unparsed++
		default:
			report.Summary.Missing++
		}

		if len(filing.MissingMetric) > 0 {
			report.Summary.MissingMetrics++
		}
		for tier := range filing.MissingByTier {
			if report.Summary.MissingMetricsByTier == nil {
				report.Summary.MissingMetricsByTier = make(map[string]int)
			}

			report.Summary.MissingMetricsByTier[tier]++
		}
	}

	addMissingIndexRequests(&report, config)
	addMissingFilingRequests(&report)
	sortReport(&report)

	return report, nil
}

// inspectFiling inspects a single filing and returns its status and metrics.
func inspectFiling(ctx context.Context, config Config, record edgar.EdgarIndex) (Filing, error) {
	if err := ctx.Err(); err != nil {
		return Filing{}, err
	}

	accession := accessionFromPath(record.FilingPath)
	sourcePath, sourceErr := edgar.LocalFilingPath(config.FilingsDir, record.FilingPath)
	parsedPath := parsedViewPath(config.ParsedDir, record.CIK, accession)

	result := Filing{
		CIK: record.CIK, Company: record.CompanyName, Accession: accession,
		FormType: record.FormType, DateFiled: record.DateFiled.Format("2006-01-02"),
		FilingPath: record.FilingPath, SourcePath: sourcePath,
		ParsedPath: parsedPath, Status: "missing", MissingMetric: nil,
		MissingByTier: nil, Metrics: nil,
	}

	if sourceErr != nil {
		result.Status = "invalid_source_path"

		return result, sourceErr
	}

	if _, err := os.Stat(sourcePath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			result.Status = "source_error"
		}

		return result, nil
	}

	result.Status = "downloaded"

	view, err := filingview.Load(parsedPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			result.Status = "parsed_error"
		}

		return result, nil
	}

	result.Status = "parsed"
	result.Metrics, result.MissingMetric, result.MissingByTier = metricPresence(view, config.Taxonomy)

	return result, nil
}

// metricPresence checks which metrics are present in the filing view and which are missing according to the taxonomy.
func metricPresence(view filingview.View, taxonomy filingview.Taxonomy) (map[string]string, []string, map[string][]string) {
	present := make(map[string]string)

	for _, group := range view.Summary {
		for _, row := range group.Rows {
			if row.Key != "" {
				present[row.Key] = "present"
			}
		}
	}

	for _, statement := range []filingview.StatementView{
		view.Statements.Income, view.Statements.Balance, view.Statements.CashFlow,
	} {
		for _, group := range statement.Groups {
			for _, row := range group.Rows {
				if row.Key != "" {
					present[row.Key] = "present"
				}
			}
		}
	}

	metrics := make(map[string]string, len(taxonomy.Metrics))
	missing := make([]string, 0)
	missingByTier := make(map[string][]string)

	for _, metric := range taxonomy.Metrics {
		if _, ok := present[metric.Key]; ok {
			metrics[metric.Key] = "present"

			continue
		}

		tier := metricCoverageTier(metric)
		if tier == "supplemental" {
			metrics[metric.Key] = "supplemental_missing"

			continue
		}

		metrics[metric.Key] = "missing"
		missing = append(missing, metric.Key)
		missingByTier[tier] = append(missingByTier[tier], metric.Key)
	}

	sort.Strings(missing)
	for tier := range missingByTier {
		sort.Strings(missingByTier[tier])
	}
	if len(missingByTier) == 0 {
		missingByTier = nil
	}

	return metrics, missing, missingByTier
}

func metricCoverageTier(metric filingview.MetricDefinition) string {
	tier := strings.TrimSpace(metric.CoverageTier)
	if tier == "" {
		return "core"
	}

	return tier
}

// addMissingIndexRequests adds acquisition requests for any missing quarterly index files within the specified range of years.
func addMissingIndexRequests(report *Report, config Config) {
	if config.IndexesDir == "" || config.FromYear == 0 || config.ToYear == 0 {
		return
	}

	for year := config.FromYear; year <= config.ToYear; year++ {
		missing := make([]int, 0, 4)

		for quarter := 1; quarter <= 4; quarter++ {
			path := filepath.Join(config.IndexesDir,
				fmt.Sprintf("%d-QTR%d.tsv", year, quarter))
			if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
				missing = append(missing, quarter)
			}
		}

		if len(missing) == 0 {
			continue
		}

		report.Acquisition = append(report.Acquisition, AcquisitionRequest{
			Kind: "index", Year: year, Quarters: missing,
			CIK: "", FormTypes: nil, Years: nil,
			Description: "download missing quarterly index files before refreshing master.tsv",
		})
	}
}

// addMissingFilingRequests adds acquisition requests for any missing filings in the report.
func addMissingFilingRequests(report *Report) {
	grouped := make(map[string]*AcquisitionRequest)

	for _, filing := range report.Filings {
		if filing.Status != "missing" {
			continue
		}

		key := filing.CIK + "\x00" + filing.FormType + "\x00" + filing.DateFiled[:4]
		request := grouped[key]

		if request == nil {
			year := yearFromDate(filing.DateFiled)
			request = &AcquisitionRequest{
				Kind: "filings", CIK: filing.CIK,
				FormTypes: []string{filing.FormType}, Years: []int{year},
				Year: 0, Quarters: nil,
				Description: "download missing filing from the local master record",
			}
			grouped[key] = request
		}
	}

	for _, request := range grouped {
		report.Acquisition = append(report.Acquisition, *request)
	}
}

func sortReport(report *Report) {
	sort.Slice(report.Filings, func(i, j int) bool {
		left, right := report.Filings[i], report.Filings[j]
		if left.CIK != right.CIK {
			return left.CIK < right.CIK
		}

		if left.DateFiled != right.DateFiled {
			return left.DateFiled < right.DateFiled
		}

		return left.Accession < right.Accession
	})
	sort.Slice(report.Acquisition, func(i, j int) bool {
		left, right := report.Acquisition[i], report.Acquisition[j]
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}

		if left.Year != right.Year {
			return left.Year < right.Year
		}

		return left.CIK < right.CIK
	})
}

func WriteJSON(path string, report Report) error {
	if path == "" {
		return errors.New("coverage report path is required")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create coverage report directory: %w", err)
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create coverage report: %w", err)
	}

	defer func() { _ = file.Close() }()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("write coverage report: %w", err)
	}

	return nil
}

func inYearRange(year, from, to int) bool {
	return (from == 0 || year >= from) && (to == 0 || year <= to)
}

func accessionFromPath(path string) string {
	name := filepath.Base(filepath.FromSlash(path))

	return strings.TrimSuffix(name, filepath.Ext(name))
}

// parsedViewPath constructs the path to the parsed filing view JSON file based on the directory, CIK, and accession.
func parsedViewPath(directory, cik, accession string) string {
	normalized, err := constituents.NormalizeCIK(cik)
	if err != nil {
		normalized = cik
	}

	return filepath.Join(directory, normalized, accession, "filing-view.json")
}

// yearFromDate extracts the year from a date string in the format "YYYY-MM-DD".
func yearFromDate(date string) int {
	if len(date) < 4 {
		return 0
	}

	year := 0

	_, _ = fmt.Sscanf(date[:4], "%d", &year)

	return year
}
