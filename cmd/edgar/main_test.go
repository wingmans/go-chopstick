package main

import (
	"errors"
	"flag"
	"testing"
)

func TestParseDownloadIndexConfigDefaults(t *testing.T) {
	cfg, err := parseConfig([]string{"download-index"})
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}

	if cfg.index.Directory != "./data/indexes/quarterly" {
		t.Fatalf("unexpected index directory %q", cfg.index.Directory)
	}

	if cfg.index.ZipDirectory != "./data/raw/index-zips" {
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
}

func TestParseDownloadFilingsConfigSupportsRepeatedFormTypes(t *testing.T) {
	cfg, err := parseConfig([]string{
		"download-filings",
		"--form-type", "10-K",
		"--form-type", "10-Q",
		"--cik", "0000123456",
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
