package main

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

func TestParseSubmissionFlags(t *testing.T) {
	const firstFile = "one.txt"

	cfg, err := parseConfig([]string{parseCommand, "-f", firstFile, "--file", "two.txt", "-n"})
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.parse.files) != 2 || !cfg.parse.noop || cfg.command != parseCommand {
		t.Fatalf("unexpected parse config: %+v", cfg.parse)
	}

	cfg, err = parseConfig([]string{parseCommand, "--file", firstFile})
	if err != nil || cfg.parse.noop {
		t.Fatal("noop should default to false")
	}

	for _, args := range [][]string{
		{parseCommand},
		{parseCommand, "--unknown"},
		{parseCommand, "-f"},
		{parseCommand, "-f", "one.xml"},
		{parseCommand, "-f", firstFile, "unexpected", "--unknown"},
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

	if err := run(t.Context(), []string{parseCommand, "-f", source, "--noop"}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat("data"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("noop wrote data: %v", err)
	}

	if err := run(t.Context(), []string{parseCommand, "-f", source}); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join("data", "parsed", "0000789019", "0001193125-26-191457", "filing.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}
