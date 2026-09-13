package main

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

func TestParseSubmissionFlags(t *testing.T) {
	cfg, err := parseConfig([]string{parseCommand, "--file", "one.txt", "--file", "two.txt", "-n"})
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.parse.files) != 2 || !cfg.parse.noop || cfg.command != parseCommand {
		t.Fatalf("unexpected parse config: %+v", cfg.parse)
	}

	cfg, err = parseConfig([]string{parseCommand, "--cik", "123", "--form-type", "10-Q", "--year", "2024"})
	if err != nil || cfg.parse.noop || cfg.parse.filter.CIK != "123" || cfg.parse.filter.Year != 2024 || len(cfg.parse.filter.FormTypes) != 1 {
		t.Fatalf("unexpected parse filters: %+v", cfg.parse)
	}

	cfg, err = parseConfig([]string{parseCommand})
	if err != nil || cfg.parse.noop || cfg.parse.filter.Year != 0 {
		t.Fatal("unexpected parse defaults")
	}

	for _, args := range [][]string{
		{parseCommand, "--unknown"},
		{parseCommand, "-f"},
		{parseCommand, "--file", "one.xml"},
		{parseCommand, "--file", "one.txt", "--cik", "123"},
		{parseCommand, "unexpected", "--unknown"},
		{parseCommand, "--help"},
	} {
		if _, err := parseConfig(args); !errors.Is(err, flag.ErrHelp) {
			t.Fatalf("%v: got %v, want help", args, err)
		}
	}
}

func TestParseCommandNoopAndPersistence(t *testing.T) {
	source, err := filepath.Abs(filepath.Join("..", "..", "golden", "sample_8-K.txt"))
	if err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()
	t.Chdir(directory)

	if err := run(t.Context(), []string{parseCommand, "--file", source, "--noop"}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat("data"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("noop wrote data: %v", err)
	}

	if err := run(t.Context(), []string{parseCommand, "--file", source}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join("data", "parsed", "0000789019", "0001193125-26-191457", "filing.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
