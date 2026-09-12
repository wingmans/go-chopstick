package main

import (
	"errors"
	"flag"
	"testing"
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
