package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"wingman.com/fetch-ecb/internal/edgar"
	"wingman.com/fetch-ecb/internal/filingview"
	"wingman.com/fetch-ecb/internal/xerr"
)

func TestParseDownloadIndexConfigDefaults(t *testing.T) {
	t.Setenv(edgarUserAgentEnv, "")
	t.Setenv(edgarBaseURLEnv, "")

	cfg, err := parseConfig([]string{"index"})
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}

	if cfg.index.Directory != "./data/indexes/quarterly" {
		t.Fatalf("unexpected index directory %q", cfg.index.Directory)
	}

	if cfg.index.ZipDirectory != "./data/cache/index-zips" {
		t.Fatalf("unexpected ZIP directory %q", cfg.index.ZipDirectory)
	}

	if cfg.index.MasterPath != "./data/indexes/master.tsv" {
		t.Fatalf("unexpected master path %q", cfg.index.MasterPath)
	}

	if !cfg.index.Stitch {
		t.Fatal("expected stitching to be enabled by default")
	}

	if cfg.index.UserAgent != defaultUserAgent {
		t.Fatalf("unexpected user agent %q", cfg.index.UserAgent)
	}

	if cfg.index.Noop {
		t.Fatal("expected noop to be disabled by default")
	}
}

func TestParseConfigUsesEDGAREnvironment(t *testing.T) {
	t.Setenv(edgarUserAgentEnv, "Test Agent test@example.com")
	t.Setenv(edgarBaseURLEnv, "https://example.test/Archives")

	cfg, err := parseConfig([]string{"index"})
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}

	if cfg.index.UserAgent != "Test Agent test@example.com" {
		t.Fatalf("unexpected user agent %q", cfg.index.UserAgent)
	}

	if cfg.index.BaseURL != "https://example.test/Archives" {
		t.Fatalf("unexpected base URL %q", cfg.index.BaseURL)
	}
}

func TestParseDownloadFilingsConfigSupportsRepeatedFormTypes(t *testing.T) {
	cfg, err := parseConfig([]string{
		"filings",
		"--form-type", "10-K",
		"--form-type", "10-Q",
		"--cik", "0000123456",
		"--year", "2024",
		"--noop",
	})
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}

	if cfg.filings.masterPath != "./data/indexes/master.tsv" {
		t.Fatalf("unexpected master path %q", cfg.filings.masterPath)
	}

	if cfg.filings.config.Directory != "./data/filings" {
		t.Fatalf("unexpected filing directory %q", cfg.filings.config.Directory)
	}

	if cfg.filings.filter.CIK != "0000123456" {
		t.Fatalf("unexpected CIK %q", cfg.filings.filter.CIK)
	}

	if cfg.filings.filter.Year != 2024 {
		t.Fatalf("unexpected year %d", cfg.filings.filter.Year)
	}

	if !cfg.filings.config.Noop {
		t.Fatal("expected noop to be enabled")
	}

	if got, want := len(cfg.filings.filter.FormTypes), 2; got != want {
		t.Fatalf("got %d form types, want %d", got, want)
	}
}

func TestParseConfigWithoutCommandShowsHelp(t *testing.T) {
	_, err := parseConfig(nil)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("got %v, want flag.ErrHelp", err)
	}
}

func TestParseServeConfigDefaults(t *testing.T) {
	cfg, err := parseConfig([]string{"serve"})
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}

	if cfg.serve.address != "127.0.0.1:8080" {
		t.Fatalf("unexpected serve address %q", cfg.serve.address)
	}

	if cfg.serve.parsedDir != "./data/parsed" {
		t.Fatalf("unexpected parsed directory %q", cfg.serve.parsedDir)
	}

	cfg, err = parseConfig([]string{"serve", "--set", "golden"})
	if err != nil {
		t.Fatalf("parseConfig with set returned error: %v", err)
	}

	if cfg.serve.setName != "golden" {
		t.Fatalf("unexpected serve set %q", cfg.serve.setName)
	}
}

func TestParseTaxonomyConfigFilters(t *testing.T) {
	cfg, err := parseConfig([]string{
		"taxonomy", "lint", "--cik", "789019", "--form-type", "10-K",
		"--year", "2024", "--parsed-dir", "./parsed", "--format", "json",
		"--out", "lint.json",
	})
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}

	if cfg.taxonomy.filter.CIK != "789019" || cfg.taxonomy.filter.Year != 2024 {
		t.Fatalf("unexpected taxonomy filters: %+v", cfg.taxonomy.filter)
	}

	if len(cfg.taxonomy.filter.FormTypes) != 1 || cfg.taxonomy.filter.FormTypes[0] != "10-K" {
		t.Fatalf("unexpected form types: %v", cfg.taxonomy.filter.FormTypes)
	}

	if cfg.taxonomy.parsedDir != "./parsed" {
		t.Fatalf("unexpected parsed directory %q", cfg.taxonomy.parsedDir)
	}

	if cfg.taxonomy.format != "json" || cfg.taxonomy.out != "lint.json" {
		t.Fatalf("unexpected taxonomy output config: format=%q out=%q",
			cfg.taxonomy.format, cfg.taxonomy.out)
	}
}

func TestParseTaxonomyConfigYearRange(t *testing.T) {
	cfg, err := parseConfig([]string{
		"taxonomy", "lint", "--cik", "789019", "--form-type", "10-K",
		"--from-year", "2010", "--to-year", "2025",
	})
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}

	if cfg.taxonomy.fromYear != 2010 || cfg.taxonomy.toYear != 2025 {
		t.Fatalf("unexpected taxonomy year range: %d-%d",
			cfg.taxonomy.fromYear, cfg.taxonomy.toYear)
	}

	if cfg.taxonomy.format != "text" {
		t.Fatalf("unexpected default taxonomy format %q", cfg.taxonomy.format)
	}
}

func TestParseTaxonomyConfigFileCannotUseFilters(t *testing.T) {
	_, err := parseConfig([]string{
		"taxonomy", "lint", "--file", "filing-view.json", "--from-year", "2010",
	})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected help for conflicting options, got %v", err)
	}
}

func TestParseTaxonomyConfigYearCannotUseRange(t *testing.T) {
	_, err := parseConfig([]string{
		"taxonomy", "lint", "--year", "2024", "--from-year", "2010",
	})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected help for conflicting year options, got %v", err)
	}
}

func TestParseTaxonomyConfigRejectsUnknownFormat(t *testing.T) {
	_, err := parseConfig([]string{
		"taxonomy", "lint", "--format", "yaml",
	})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected help for unknown format, got %v", err)
	}
}

func TestRunTaxonomyLintWritesJSONReport(t *testing.T) {
	root := t.TempDir()
	parsedDir := filepath.Join(root, "parsed")
	viewPath := filepath.Join(parsedDir, "0000789019", "0000000001", "filing-view.json")
	if err := os.MkdirAll(filepath.Dir(viewPath), 0o750); err != nil {
		t.Fatalf("create view directory: %v", err)
	}

	view := filingview.View{
		SchemaVersion:   filingview.SchemaVersion,
		TaxonomyVersion: "test-taxonomy",
		Metadata: filingview.Metadata{
			Accession: "0000000001", CIK: "0000789019", FormType: "10-K",
			FilingDate: "2024-01-02", Company: "Example Corp.",
		},
		Summary: []filingview.SummaryGroup{
			{
				Title:   "Summary",
				Periods: []string{"FY2023"},
				Rows: []filingview.FactSeries{
					{
						Label:     "Example",
						Namespace: "https://example.test/taxonomy",
						Concept:   "ExampleMetric",
						Unit:      "USD",
						Values: map[string]filingview.FactValue{
							"FY2023": {Value: "1"},
						},
					},
				},
			},
		},
	}
	viewData, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("encode view: %v", err)
	}
	if err := os.WriteFile(viewPath, viewData, 0o640); err != nil {
		t.Fatalf("write view: %v", err)
	}

	out := filepath.Join(root, "reports", "lint.json")
	err = runTaxonomyCommand(t.Context(), "lint", "", "", parsedDir,
		edgar.IndexFilter{CIK: "789019", FormTypes: []string{"10-K"}},
		"", 2020, 2025, "json", out)
	if err != nil {
		t.Fatalf("runTaxonomyCommand returned error: %v", err)
	}

	reportData, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}

	var report taxonomyBatchReport
	if err := json.Unmarshal(reportData, &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}

	if report.Operation != "lint" || report.Summary.Selected != 1 ||
		len(report.Filings) != 1 || report.Filings[0].Lint == nil {
		t.Fatalf("unexpected taxonomy report: %+v", report)
	}

	if report.Selection.FromYear != 2020 || report.Selection.ToYear != 2025 {
		t.Fatalf("unexpected selection: %+v", report.Selection)
	}
}

func TestParseCoverageConfigFilters(t *testing.T) {
	cfg, err := parseConfig([]string{
		"coverage", "--cik", "789019", "--form-type", "10-K",
		"--from-year", "2010", "--to-year", "2025", "--out", "coverage.json",
	})
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}

	if cfg.coverage.filter.CIK != "789019" {
		t.Fatalf("unexpected CIK %q", cfg.coverage.filter.CIK)
	}

	if cfg.coverage.fromYear != 2010 || cfg.coverage.toYear != 2025 {
		t.Fatalf("unexpected year range: %d-%d", cfg.coverage.fromYear, cfg.coverage.toYear)
	}

	if cfg.coverage.out != "coverage.json" {
		t.Fatalf("unexpected output path %q", cfg.coverage.out)
	}
}

func TestClassifyCLIErrorPreservesHTTPDetails(t *testing.T) {
	original := &edgar.HTTPError{
		URL: "https://example.test/filing.txt", StatusCode: 429,
		Status: "429 Too Many Requests",
	}

	result := classifyCLIError(context.Background(), original)

	classified, ok := errors.AsType[*xerr.Error](result)
	if !ok {
		t.Fatalf("classified error is not structured: %v", result)
	}

	if classified.Details["status_code"] != 429 || classified.Details["url"] != original.URL {
		t.Fatalf("missing HTTP details: %+v", classified.Details)
	}

	if classified.Message != "EDGAR rejected the request" {
		t.Fatalf("unexpected user-facing message %q", classified.Message)
	}
}

func TestReprocessFlags(t *testing.T) {
	const command = "filings"
	for _, alias := range []string{"-r", "--reprocess"} {
		filings, err := parseConfig([]string{command, alias})
		if err != nil || !filings.filings.reprocess {
			t.Fatalf("filings %s: %v", alias, err)
		}

		local, err := parseConfig([]string{parseCommand, "-f", "test.txt", alias})
		if err != nil || !local.parse.reprocess {
			t.Fatalf("parse %s: %v", alias, err)
		}
	}

	filings, err := parseConfig([]string{command})
	if err != nil || filings.filings.reprocess || filings.filings.filter.Year != 0 {
		t.Fatal("unexpected filings defaults")
	}

	local, err := parseConfig([]string{parseCommand, "-f", "test.txt"})
	if err != nil || local.parse.reprocess {
		t.Fatal("unexpected parse defaults")
	}

	for _, args := range [][]string{
		{command, "unexpected", "--reprocess"},
		{command, "--reprocess=invalid"},
		{command, "--not-a-flag"},
	} {
		if _, err := parseConfig(args); !errors.Is(err, flag.ErrHelp) {
			t.Fatalf("bad flags should show help: %v", err)
		}
	}
}
