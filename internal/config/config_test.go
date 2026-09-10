package config

import "testing"

func TestConfigValidateAcceptsCurrencyInputs(t *testing.T) {
	cfg := Config{
		Currency:      "GBP",
		CurrencyDenom: "USD",
		StartPeriod:   "2026-02-12",
		EndPeriod:     "2026-03-12",
		Output:        "text",
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Config.Validate returned error: %v", err)
	}
}
