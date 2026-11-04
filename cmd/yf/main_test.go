package main

import (
	"io"
	"strings"
	"testing"
	"time"

	marketyahoo "wingman.com/chopstick/internal/market/yahoo"
)

func TestParseConfigBoundsFetchWork(t *testing.T) {
	t.Setenv("CHOPSTICK_DATA_DIR", "/tmp/chopstick-test-data")

	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

	cfg, err := parseConfig([]string{
		"fetch",
		"--ticker", "msft",
		"--max-symbols", "7",
		"--max-duration", "2h",
	}, io.Discard, now)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	if cfg.Command != "fetch" || cfg.MaxSymbols != 7 ||
		cfg.MaxDuration != 2*time.Hour {
		t.Fatalf("unexpected fetch config: %+v", cfg)
	}

	if cfg.Through.Format(marketyahoo.DateLayout) != "2026-10-25" {
		t.Fatalf("default target = %s, want previous day", cfg.Through)
	}

	if cfg.DatabasePath != "/tmp/chopstick-test-data/market.db" {
		t.Fatalf("database path = %q", cfg.DatabasePath)
	}

	if cfg.BatchDir != "/tmp/chopstick-test-data/market/yahoo/batches" {
		t.Fatalf("batch directory = %q", cfg.BatchDir)
	}

	if len(cfg.Tickers) != 1 || cfg.Tickers[0] != "MSFT" {
		t.Fatalf("tickers = %#v", cfg.Tickers)
	}
}

func TestRepairRequiresExplicitRangeAndTicker(t *testing.T) {
	_, err := parseConfig([]string{"repair"}, io.Discard, time.Now())
	if err == nil || !strings.Contains(err.Error(), "repair requires") {
		t.Fatalf("repair error = %v", err)
	}
}

func TestServeRejectsFixedTargetDate(t *testing.T) {
	_, err := parseConfig(
		[]string{"serve", "--to", "2026-10-25"},
		io.Discard,
		time.Now(),
	)
	if err == nil || !strings.Contains(err.Error(), "derives --to") {
		t.Fatalf("serve target error = %v", err)
	}
}
