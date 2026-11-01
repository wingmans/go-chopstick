package choppie

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"wingman.com/chopstick/internal/edgar"
	filingview "wingman.com/chopstick/internal/filing"
	"wingman.com/chopstick/internal/parser"
)

type taxonomyBatchReport struct {
	SchemaVersion   int                    `json:"schema_version"`
	Operation       string                 `json:"operation"`
	TaxonomyVersion string                 `json:"taxonomy_version"`
	GeneratedAt     string                 `json:"generated_at"`
	Selection       taxonomySelection      `json:"selection"`
	Summary         taxonomyBatchSummary   `json:"summary"`
	Filings         []taxonomyFilingReport `json:"filings"`
}

type taxonomySelection struct {
	File      string   `json:"file,omitempty"`
	Set       string   `json:"set,omitempty"`
	CIK       string   `json:"cik,omitempty"`
	CIKs      []string `json:"ciks,omitempty"`
	FormTypes []string `json:"form_types,omitempty"`
	Year      int      `json:"year,omitempty"`
	FromYear  int      `json:"from_year,omitempty"`
	ToYear    int      `json:"to_year,omitempty"`
}

type taxonomyBatchSummary struct {
	Selected      int `json:"selected"`
	Failed        int `json:"failed"`
	Findings      int `json:"findings"`
	QualityIssues int `json:"quality_issues,omitempty"`
}

type taxonomyFilingReport struct {
	Path       string                     `json:"path"`
	CIK        string                     `json:"cik,omitempty"`
	Accession  string                     `json:"accession,omitempty"`
	FormType   string                     `json:"form_type,omitempty"`
	FilingDate string                     `json:"filing_date,omitempty"`
	Lint       *filingview.LintReport     `json:"lint,omitempty"`
	Coverage   *filingview.CoverageReport `json:"coverage,omitempty"`
	Error      string                     `json:"error,omitempty"`
}

func runTaxonomyCommand(ctx context.Context, operation, path, taxonomyPath,
	parsedDir string, filter edgar.IndexFilter, setName string, fromYear,
	toYear int, format, out string,
) error {
	taxonomy, err := filingview.LoadTaxonomy(taxonomyPath)
	if err != nil {
		return err
	}

	paths := []string{path}
	if path == "" {
		if operation == "coverage" {
			paths, err = parsedFilingPaths(ctx, parsedDir, filter, fromYear, toYear)
		} else {
			paths, err = filingViewPaths(ctx, parsedDir, filter, fromYear, toYear)
		}

		if err != nil {
			return err
		}
	}

	failed := 0
	findings := 0
	qualityIssues := 0
	batch := taxonomyBatchReport{
		SchemaVersion:   1,
		Operation:       operation,
		TaxonomyVersion: taxonomy.TaxonomyVersion,
		GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
		Selection: taxonomySelection{
			File: path, Set: setName, CIK: filter.CIK, CIKs: filter.CIKs,
			FormTypes: filter.FormTypes, Year: filter.Year,
			FromYear: fromYear, ToYear: toYear,
		},
		Summary: taxonomyBatchSummary{
			Selected: 0, Failed: 0, Findings: 0, QualityIssues: 0,
		},
		Filings: nil,
	}

	var text strings.Builder

	for _, filingPath := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}

		entry := taxonomyFilingReport{
			Path: filingPath, CIK: "", Accession: "", FormType: "",
			FilingDate: "", Lint: nil, Coverage: nil, Error: "",
		}

		if operation == "coverage" {
			filing, loadErr := parser.LoadParsedFiling(filingPath)
			if loadErr != nil {
				fmt.Fprintf(&text, "%s error=%v\n", filingPath, loadErr)
				entry.Error = loadErr.Error()
				batch.Filings = append(batch.Filings, entry)

				failed++

				continue
			}

			report := filingview.Coverage(filing, taxonomy)
			findings += len(report.Unmapped)
			entry.CIK = filing.Metadata.CIK
			entry.Accession = filing.Metadata.Accession
			entry.FormType = filing.Metadata.FormType
			entry.FilingDate = filing.Metadata.FilingDate
			entry.Coverage = &report
			batch.Filings = append(batch.Filings, entry)

			fmt.Fprintf(&text, "%s cik=%s form=%s %s\n", filingPath,
				filing.Metadata.CIK, filing.Metadata.FormType, report)

			continue
		}

		view, loadErr := filingview.Load(filingPath)
		if loadErr != nil {
			fmt.Fprintf(&text, "%s error=%v\n", filingPath, loadErr)
			entry.Error = loadErr.Error()
			batch.Filings = append(batch.Filings, entry)

			failed++

			continue
		}

		report := filingview.LintView(view, taxonomy)
		findings += len(report.Unmapped)
		qualityIssues += len(report.QualityIssues)
		entry.CIK = view.Metadata.CIK
		entry.Accession = view.Metadata.Accession
		entry.FormType = view.Metadata.FormType
		entry.FilingDate = view.Metadata.FilingDate
		entry.Lint = &report
		batch.Filings = append(batch.Filings, entry)

		fmt.Fprintf(&text, "%s cik=%s form=%s %s\n", filingPath,
			view.Metadata.CIK, view.Metadata.FormType, report)
	}

	// Lint findings are review evidence for taxonomy work, not operational
	// failures. Only unreadable or malformed inputs increment failed and
	// produce a non-zero exit.
	batch.Summary = taxonomyBatchSummary{
		Selected: len(paths), Failed: failed, Findings: findings,
		QualityIssues: qualityIssues,
	}

	fmt.Fprintf(&text, "taxonomy %s summary selected=%d findings=%d failed=%d\n",
		operation, len(paths), findings, failed)

	if format == "json" {
		if err := writeTaxonomyJSON(out, batch); err != nil {
			return err
		}
	} else if err := writeTaxonomyText(out, text.String()); err != nil {
		return err
	}

	if failed > 0 {
		return fmt.Errorf("%d filing view(s) failed linting", failed)
	}

	return nil
}

// filingViewPaths returns the paths to all filing-view.json files in the given directory that match the filter.
// The returned paths are sorted in lexicographical order.
func filingViewPaths(ctx context.Context, directory string, filter edgar.IndexFilter, fromYear, toYear int) ([]string, error) {
	paths := []string{}

	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		if walkErr != nil {
			return walkErr
		}

		if entry.IsDir() || entry.Name() != "filing-view.json" {
			return nil
		}

		view, err := filingview.Load(path)
		if err != nil {
			return fmt.Errorf("inspect %s: %w", path, err)
		}

		dateFiled, _ := parseFilingDate(view.Metadata.FilingDate)
		if !inYearRange(dateFiled.Year(), fromYear, toYear) {
			return nil
		}

		if !edgar.MatchesIndexFilter(edgar.EdgarIndex{
			CIK: view.Metadata.CIK, CompanyName: view.Metadata.Company,
			FormType: view.Metadata.FormType, DateFiled: dateFiled,
			FilingPath: "local", IndexPath: "",
		}, filter) {
			return nil
		}

		paths = append(paths, path)

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan parsed directory: %w", err)
	}

	sort.Strings(paths)

	return paths, nil
}

// parsedFilingPaths returns the paths to all filing.json files in the given directory that match the filter.
// The returned paths are sorted in lexicographical order.
func parsedFilingPaths(ctx context.Context, directory string, filter edgar.IndexFilter, fromYear, toYear int) ([]string, error) {
	paths := []string{}

	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		if walkErr != nil {
			return walkErr
		}

		if entry.IsDir() || entry.Name() != "filing.json" {
			return nil
		}

		filing, err := parser.LoadParsedFiling(path)
		if err != nil {
			return fmt.Errorf("inspect %s: %w", path, err)
		}

		dateFiled, _ := parseFilingDate(filing.Metadata.FilingDate)
		if !inYearRange(dateFiled.Year(), fromYear, toYear) {
			return nil
		}

		if !edgar.MatchesIndexFilter(edgar.EdgarIndex{
			CIK: filing.Metadata.CIK, CompanyName: "",
			FormType: filing.Metadata.FormType, DateFiled: dateFiled,
			FilingPath: "local", IndexPath: "",
		}, filter) {
			return nil
		}

		paths = append(paths, path)

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan parsed directory: %w", err)
	}

	sort.Strings(paths)

	return paths, nil
}

func writeTaxonomyJSON(path string, report taxonomyBatchReport) error {
	var (
		writer io.Writer = os.Stdout
		file   *os.File
	)

	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return fmt.Errorf("create taxonomy report directory: %w", err)
		}

		created, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("create taxonomy report: %w", err)
		}

		file = created
		writer = file
	}

	if file != nil {
		defer func() { _ = file.Close() }()
	}

	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("write taxonomy report: %w", err)
	}

	return nil
}

func writeTaxonomyText(path, text string) error {
	if path == "" {
		fmt.Fprint(os.Stdout, text)

		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create taxonomy report directory: %w", err)
	}

	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return fmt.Errorf("write taxonomy report: %w", err)
	}

	return nil
}

func inYearRange(year, fromYear, toYear int) bool {
	return (fromYear == 0 || year >= fromYear) && (toYear == 0 || year <= toYear)
}

func parseFilingDate(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{"2006-01-02", "20060102"} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed, nil
		}
	}

	return time.Time{}, fmt.Errorf("invalid filing date %q", value)
}
