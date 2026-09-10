package main

import (
	"testing"
	"time"
)

func TestParseConfigUsesFlagsAndCurrentEndDate(t *testing.T) {
	t.Setenv("ECB_CURRENCY", "cad")
	t.Setenv("ECB_CURRENCY_DENOM", "eur")

	now := time.Date(2026, time.March, 12, 15, 30, 0, 0, time.FixedZone("CET", 3600))
	cfg, err := parseConfig([]string{
		"-currency", "gbp",
		"-currency-denom", "usd",
	}, now)
	if err != nil {
		t.Fatalf("parseConfig returned error: %v", err)
	}

	if cfg.Currency != "GBP" || cfg.CurrencyDenom != "USD" {
		t.Fatalf("unexpected currencies: %s/%s", cfg.Currency, cfg.CurrencyDenom)
	}

	if cfg.EndPeriod != "2026-03-12" {
		t.Fatalf("expected current end date, got %q", cfg.EndPeriod)
	}
}
