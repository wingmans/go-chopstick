package config

import (
	"testing"
	"time"
)

func TestParseConfigUsesCurrencyInputsAndCurrentEndDate(t *testing.T) {
	now := time.Date(2026, time.March, 12, 15, 30, 0, 0, time.FixedZone("CET", 3600))

	cfg, err := ParseConfig([]string{
		"-currency", "gbp",
		"-currency-denom", "usd",
	}, now)
	if err != nil {
		t.Fatalf("ParseConfig returned error: %v", err)
	}

	if cfg.Currency != "GBP" || cfg.CurrencyDenom != "USD" {
		t.Fatalf("unexpected currencies: %s/%s", cfg.Currency, cfg.CurrencyDenom)
	}

	if cfg.EndPeriod != "2026-03-12" {
		t.Fatalf("expected current end date, got %q", cfg.EndPeriod)
	}
}
