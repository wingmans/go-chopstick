package main

import (
	"context"
	"errors"
	"flag"
	"testing"

	"wingman.com/fetch-ecb/internal/edgar"
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
}

func TestParseTaxonomyConfigFilters(t *testing.T) {
	cfg, err := parseConfig([]string{
		"taxonomy", "lint", "--cik", "789019", "--form-type", "10-K",
		"--year", "2024", "--parsed-dir", "./parsed",
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
}

func TestParseTaxonomyConfigFileCannotUseFilters(t *testing.T) {
	_, err := parseConfig([]string{
		"taxonomy", "lint", "--file", "filing-view.json", "--cik", "789019",
	})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected help for conflicting options, got %v", err)
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
