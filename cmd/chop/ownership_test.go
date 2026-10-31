package main

import (
	"errors"
	"flag"
	"testing"
)

func TestParseOwnershipConfig(t *testing.T) {
	t.Parallel()

	cfg, err := parseConfig([]string{
		"ownership", "--from-year", "2020", "--to-year", "2022",
		"-f", "schedule 13g/a", "-f", "4", "--noop",
	})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.command != "ownership" || cfg.ownership.operation != "import" ||
		cfg.ownership.fromYear != 2020 || cfg.ownership.toYear != 2022 ||
		!cfg.ownership.noop || len(cfg.ownership.formTypes) != 2 ||
		cfg.ownership.formTypes[0] != "SCHEDULE 13G/A" {
		t.Fatalf("unexpected ownership config: %+v", cfg.ownership)
	}

	cfg, err = parseConfig([]string{
		"ownership", "status", "--database", "test.db",
	})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.ownership.operation != "status" ||
		cfg.ownership.databasePath != "test.db" {
		t.Fatalf("unexpected ownership status config: %+v", cfg.ownership)
	}
}

func TestParseOwnershipConfigRejectsInvalidSelection(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"ownership", "--from-year", "2015"},
		{"ownership", "--from-year", "2022", "--to-year", "2020"},
		{"ownership", "--form-type", "10-K"},
		{"ownership", "unknown"},
	} {
		if _, err := parseConfig(args); !errors.Is(err, flag.ErrHelp) {
			t.Fatalf("parseConfig(%v) error = %v, want help", args, err)
		}
	}
}
