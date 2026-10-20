package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wingman.com/fetch-ecb/internal/appdata"
	"wingman.com/fetch-ecb/internal/edgar"
	filingview "wingman.com/fetch-ecb/internal/filing"
	"wingman.com/fetch-ecb/internal/xerr"
)

func TestParseDownloadIndexConfigDefaults(t *testing.T) {
	t.Setenv(appdata.EnvName, "")
	t.Setenv(edgarUserAgentEnv, "")
	t.Setenv(edgarBaseURLEnv, "")
	t.Setenv(edgarFromYearEnv, "")

	cfg, err := parseConfig([]string{"index"})
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}

	if cfg.index.Directory != "data/cache/indexes/quarterly" {
		t.Fatalf("unexpected index directory %q", cfg.index.Directory)
	}

	if cfg.index.ZipDirectory != "data/cache/index-zips" {
		t.Fatalf("unexpected ZIP directory %q", cfg.index.ZipDirectory)
	}

	if cfg.index.MasterPath != "data/cache/indexes/master.tsv" {
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

	if cfg.index.SinceYear != edgar.EarliestPipelineYear {
		t.Fatalf("unexpected earliest year %d", cfg.index.SinceYear)
	}
}

func TestConfiguredDataRootControlsCommandDefaults(t *testing.T) {
	t.Setenv(appdata.EnvName, "/srv/chopstick")
	t.Setenv(edgarSetEnv, "")

	events, err := parseEventCommandConfig("run", nil)
	if err != nil {
		t.Fatalf("parse event config: %v", err)
	}

	index, err := parseDownloadIndexConfig(nil)
	if err != nil {
		t.Fatalf("parse index config: %v", err)
	}

	operations, err := parseOperationsConfig("doctor", nil)
	if err != nil {
		t.Fatalf("parse operations config: %v", err)
	}

	paths := map[string]string{
		"polling database":  events.events.pollingDB,
		"news database":     events.events.newsDB,
		"filings":           events.events.filingsDirectory,
		"parsed":            events.events.parsedDirectory,
		"analysis database": events.events.analysisDB,
		"master index":      index.index.MasterPath,
		"quarterly indexes": index.index.Directory,
		"index archives":    index.index.ZipDirectory,
		"doctor database":   operations.operations.pollingDB,
	}

	for name, path := range paths {
		if !strings.HasPrefix(path, "/srv/chopstick/") {
			t.Errorf("%s path %q is outside configured data root", name, path)
		}
	}
}

func TestParseDiscoverConfigAppliesPipelineBoundaryAndFilters(t *testing.T) {
	t.Chdir(filepath.Join("..", ".."))

	cfg, err := parseConfig([]string{
		"discover", "--set", "us-gaap", "--from-year", "2022",
		"--to-year", "2026", "--form-type", "10-K",
	})
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}

	if cfg.events.filter.FromYear != 2022 ||
		cfg.events.filter.ToYear != 2026 {
		t.Fatalf("unexpected year range: %+v", cfg.events.filter)
	}

	if len(cfg.events.filter.CIKs) == 0 {
		t.Fatal("constituent set did not populate CIKs")
	}

	if got := cfg.events.filter.FormTypes; len(got) != 1 || got[0] != "10-K" {
		t.Fatalf("unexpected form types: %v", got)
	}
}

func TestParseDiscoverConfigDefaultsToPipelineBoundary(t *testing.T) {
	t.Setenv(edgarFromYearEnv, "")
	t.Setenv(edgarSetEnv, "")

	cfg, err := parseConfig([]string{"discover"})
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}

	if cfg.events.filter.FromYear != edgar.EarliestPipelineYear {
		t.Fatalf("discover from year = %d, want %d",
			cfg.events.filter.FromYear, edgar.EarliestPipelineYear)
	}

	if _, err := parseConfig([]string{
		"discover", "--from-year", "2015",
	}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("pre-boundary discover returned %v, want help error", err)
	}
}

func TestParseEventWorkersSupportDrainMode(t *testing.T) {
	for _, command := range []string{"download", "process"} {
		cfg, err := parseConfig([]string{command, "--drain"})
		if err != nil {
			t.Fatalf("%s config returned error: %v", command, err)
		}

		if !cfg.events.drain {
			t.Fatalf("%s did not enable drain mode", command)
		}
	}
}

func TestParseDiscoverAlias(t *testing.T) {
	cfg, err := parseConfig([]string{"discover", "--from-year", "2022"})
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}

	if cfg.command != "discover" || cfg.events.filter.FromYear != 2022 {
		t.Fatalf("unexpected discover config: %+v", cfg)
	}
}

func TestExplicitYearOverridesEnvironmentFromYear(t *testing.T) {
	t.Setenv(edgarFromYearEnv, "2022")

	cfg, err := parseConfig([]string{"discover", "--year", "2024"})
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}

	if cfg.events.filter.Year != 2024 || cfg.events.filter.FromYear != 0 {
		t.Fatalf("unexpected year selection: %+v", cfg.events.filter)
	}
}

func TestParseBackfillUsesEnvironmentDefaults(t *testing.T) {
	t.Chdir(filepath.Join("..", ".."))
	t.Setenv(edgarSetEnv, "us-gaap")
	t.Setenv(edgarFromYearEnv, "2022")
	t.Setenv(edgarNATSURLEnv, "nats://example.test:4222")

	cfg, err := parseConfig([]string{"backfill"})
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}

	if cfg.backfill.setName != "us-gaap" || cfg.backfill.fromYear != 2022 ||
		cfg.backfill.natsURL != "nats://example.test:4222" {
		t.Fatalf("unexpected backfill config: %+v", cfg.backfill)
	}
}

func TestParseDoctorVerbose(t *testing.T) {
	cfg, err := parseConfig([]string{"doctor", "--verbose"})
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}

	if !cfg.operations.verbose {
		t.Fatal("doctor did not enable verbose output")
	}
}

func TestNewsAddressUsesEnvironmentDefault(t *testing.T) {
	t.Setenv(edgarNewsAddressEnv, "127.0.0.1:8082")

	for _, command := range []string{"run", "news", "status"} {
		cfg, err := parseConfig([]string{command})
		if err != nil {
			t.Fatalf("parseConfig(%q) returned error: %v", command, err)
		}

		address := cfg.events.address
		if command == "status" {
			address = cfg.operations.newsAddress
		}

		if address != "127.0.0.1:8082" {
			t.Fatalf("parseConfig(%q) address = %q", command, address)
		}
	}

	cfg, err := parseConfig([]string{
		"run", "--address", "127.0.0.1:8083",
	})
	if err != nil {
		t.Fatalf("parseConfig with address returned error: %v", err)
	}
	if cfg.events.address != "127.0.0.1:8083" {
		t.Fatalf("CLI address did not override environment: %q",
			cfg.events.address)
	}
}

func TestParseRetryRequiresValidStage(t *testing.T) {
	for _, args := range [][]string{
		{"retry"},
		{"retry", "--stage", "unknown"},
	} {
		if _, err := parseConfig(args); !errors.Is(err, flag.ErrHelp) {
			t.Fatalf("parseConfig(%v) returned %v, want help", args, err)
		}
	}

	cfg, err := parseConfig([]string{"retry", "--stage", "download"})
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}

	if cfg.operations.stage != "download" {
		t.Fatalf("unexpected retry stage %q", cfg.operations.stage)
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
	t.Setenv(appdata.EnvName, "")

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

	if cfg.filings.masterPath != "data/cache/indexes/master.tsv" {
		t.Fatalf("unexpected master path %q", cfg.filings.masterPath)
	}

	if cfg.filings.config.Directory != "data/cache/filings" {
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
	t.Setenv(appdata.EnvName, "")

	cfg, err := parseConfig([]string{"serve"})
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}

	if cfg.serve.address != "127.0.0.1:8080" {
		t.Fatalf("unexpected serve address %q", cfg.serve.address)
	}

	if cfg.serve.parsedDir != "data/parsed" {
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

func TestParseFilingsConfigYearRange(t *testing.T) {
	cfg, err := parseConfig([]string{
		"filings", "--cik", "789019", "--form-type", "10-K",
		"--from-year", "2015", "--to-year", "2026",
	})
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}

	if cfg.filings.filter.FromYear != 2015 || cfg.filings.filter.ToYear != 2026 {
		t.Fatalf("unexpected filings year range: %+v", cfg.filings.filter)
	}
}

func TestParseFilingsConfigYearCannotUseRange(t *testing.T) {
	_, err := parseConfig([]string{
		"filings", "--year", "2024", "--from-year", "2015",
	})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected help for conflicting year options, got %v", err)
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
		SchemaVersion: filingview.SchemaVersion, ParserVersion: "",
		TaxonomyVersion: "test-taxonomy", SourcePath: "", SourceSHA256: "",
		Metadata: filingview.Metadata{
			Accession: "0000000001", CIK: "0000789019", FormType: "10-K",
			FilingDate: "2024-01-02", ReportDate: "", Company: "Example Corp.",
		},
		Documents: nil,
		Summary: []filingview.SummaryGroup{
			{
				Title:   "Summary",
				Periods: []string{"FY2023"},
				Rows: []filingview.FactSeries{
					{
						Key:       "",
						Label:     "Example",
						Namespace: "https://example.test/taxonomy",
						Concept:   "ExampleMetric",
						Unit:      "USD",
						Values: map[string]filingview.FactValue{
							"FY2023": {
								Value: "1", Nil: false, ContextRef: "",
								Decimals: "", UnitRef: "",
							},
						},
					},
				},
			},
		},
		Statements: filingview.Statements{
			Income: filingview.StatementView{Title: "", Groups: nil},
			Balance: filingview.StatementView{
				Title: "", Groups: nil,
			},
			CashFlow: filingview.StatementView{Title: "", Groups: nil},
		},
		Ratios: nil,
		Quality: filingview.Quality{
			IdentityChecks: nil,
		},
		Counts: filingview.Counts{
			Documents: 0, Instances: 0, Facts: 0, Contexts: 0,
			DimensionalFactsExcluded: 0, Status: "",
		},
		Diagnostics: nil,
	}

	viewData, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("encode view: %v", err)
	}

	if err := os.WriteFile(viewPath, viewData, 0o600); err != nil {
		t.Fatalf("write view: %v", err)
	}

	out := filepath.Join(root, "reports", "lint.json")

	err = runTaxonomyCommand(t.Context(), "lint", "", "", parsedDir,
		edgar.IndexFilter{
			CIK: "789019", CIKs: nil, FormTypes: []string{"10-K"},
			Year: 0, FromYear: 0, ToYear: 0,
		},
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
