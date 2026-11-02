package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testDefaults() commandDefaults {
	return commandDefaults{
		FromYear: 2020, Set: "test-set", NATSURL: "nats://test:4222",
		UserAgent: "test-agent", CommandAddress: "127.0.0.1:8084",
		HTTPAddress: "127.0.0.1:8083",
	}
}

func TestKongParsesCommandHierarchy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		command string
		check   func(*testing.T, cli)
	}{
		{
			name:    "backfill",
			args:    []string{"backfill"},
			command: "backfill",
		},
		{
			name:    "repeated filing forms",
			args:    []string{"filings", "-f", "10-K", "-f", "10-Q", "-r"},
			command: "filings",
			check: func(t *testing.T, model cli) {
				t.Helper()

				if len(model.Filings.FormTypes) != 2 ||
					!model.Filings.Reprocess {
					t.Fatalf("unexpected filings: %+v", model.Filings)
				}
			},
		},
		{
			name: "repeated parse files",
			args: []string{
				"parse", "--file", "one.txt", "--file",
				"two.txt", "-n",
			},
			command: "parse",
			check: func(t *testing.T, model cli) {
				t.Helper()

				if len(model.Parse.Files) != 2 || !model.Parse.Noop {
					t.Fatalf("unexpected parse: %+v", model.Parse)
				}
			},
		},
		{
			name:    "coverage aliases",
			args:    []string{"coverage", "-s", "golden", "-c", "1", "-f", "10-K", "-m", "master.tsv", "-d", "parsed", "-o", "report.json"},
			command: "coverage",
		},
		{
			name:    "default index download",
			args:    []string{"index", "-y", "2021", "-r"},
			command: "index download",
			check: func(t *testing.T, model cli) {
				t.Helper()

				if model.Index.Download.FromYear != 2021 ||
					!model.Index.Download.RefreshLatest {
					t.Fatalf("unexpected index: %+v", model.Index.Download)
				}
			},
		},
		{
			name:    "explicit index download",
			args:    []string{"index", "download"},
			command: "index download",
		},
		{
			name:    "index import",
			args:    []string{"index", "import", "--latest"},
			command: "index import",
		},
		{
			name:    "index status",
			args:    []string{"index", "status"},
			command: "index status",
		},
		{
			name:    "default ownership import",
			args:    []string{"ownership", "-f", "4", "--noop"},
			command: "ownership import",
			check: func(t *testing.T, model cli) {
				t.Helper()

				if len(model.Ownership.Import.FormTypes) != 1 ||
					!model.Ownership.Import.Noop {
					t.Fatalf("unexpected ownership: %+v",
						model.Ownership.Import)
				}
			},
		},
		{
			name:    "ownership status",
			args:    []string{"ownership", "status"},
			command: "ownership status",
		},
		{
			name:    "artifacts list",
			args:    []string{"artifacts", "list", "--cik", "1", "--accession", "a"},
			command: "artifacts list",
		},
		{
			name: "artifacts extract repeated options",
			args: []string{
				"artifacts", "extract", "--cik", "1", "--accession", "a",
				"--type", "instance", "--type", "schema",
				"--document", "one.xml", "--document", "two.xsd",
			},
			command: "artifacts extract",
			check: func(t *testing.T, model cli) {
				t.Helper()

				if len(model.Artifacts.Extract.Types) != 2 ||
					len(model.Artifacts.Extract.Documents) != 2 {
					t.Fatalf("unexpected extract: %+v", model.Artifacts.Extract)
				}
			},
		},
		{
			name:    "artifacts serve",
			args:    []string{"artifacts", "serve", "--cik", "1", "--accession", "a"},
			command: "artifacts serve",
		},
		{
			name:    "artifacts clean",
			args:    []string{"artifacts", "clean", "--cik", "1", "--accession", "a"},
			command: "artifacts clean",
		},
		{
			name:    "taxonomy lint",
			args:    []string{"taxonomy", "lint"},
			command: "taxonomy lint",
		},
		{
			name:    "taxonomy coverage",
			args:    []string{"taxonomy", "coverage", "--format", "json"},
			command: "taxonomy coverage",
		},
		{
			name:    "doctor",
			args:    []string{"doctor"},
			command: "doctor",
		},
		{
			name:    "status",
			args:    []string{"status"},
			command: "status",
		},
		{
			name:    "rebuild edgar",
			args:    []string{"rebuild", "edgar", "--confirm"},
			command: "rebuild edgar",
		},
		{
			name:    "default failures list",
			args:    []string{"failures", "--limit", "12"},
			command: "failures list",
			check: func(t *testing.T, model cli) {
				t.Helper()

				if model.Failures.List.Limit != 12 {
					t.Fatalf("unexpected failures: %+v", model.Failures.List)
				}
			},
		},
		{
			name:    "explicit failures list",
			args:    []string{"failures", "list"},
			command: "failures list",
		},
		{
			name:    "failures retry",
			args:    []string{"failures", "retry", "--stage", "download"},
			command: "failures retry",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var model cli

			parser, err := newParser(&model, testDefaults())
			if err != nil {
				t.Fatal(err)
			}

			parsed, err := parser.Parse(test.args)
			if err != nil {
				t.Fatal(err)
			}

			if parsed.Command() != test.command {
				t.Fatalf("command = %q, want %q", parsed.Command(),
					test.command)
			}

			if test.check != nil {
				test.check(t, model)
			}
		})
	}
}

func TestKongRejectsInvalidArguments(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"unknown"},
		{"artifacts", "extract", "--cik", "1"},
		{"failures", "retry", "--stage", "unknown"},
		{"taxonomy", "lint", "--format", "yaml"},
	} {
		var model cli

		parser, err := newParser(&model, testDefaults())
		if err != nil {
			t.Fatal(err)
		}

		if _, err := parser.Parse(args); err == nil {
			t.Fatalf("Parse(%v) succeeded", args)
		}
	}
}

func TestTypedCommandsRejectInvalidCombinations(t *testing.T) {
	t.Parallel()

	tests := [][]string{
		{"parse", "--file", "one.txt", "--cik", "123"},
		{"parse", "--file", "one.xml"},
		{"rebuild", "edgar", "--set", "golden"},
		{"ownership", "--form-type", "10-K", "--noop"},
		{"failures", "--limit", "0"},
	}

	for _, args := range tests {
		var model cli

		parser, err := newParser(&model, testDefaults())
		if err != nil {
			t.Fatal(err)
		}

		parsed, err := parser.Parse(args)
		if err != nil {
			continue
		}

		runtime := &commandRuntime{
			Client: http.DefaultClient,
			Logger: slog.New(slog.DiscardHandler),
		}

		err = runCommand(parsed, context.Background(), runtime)
		if err == nil {
			t.Errorf("Run(%v) succeeded", args)
		} else if strings.Contains(err.Error(), "binding") {
			t.Errorf("Run(%v) failed to bind context: %v", args, err)
		}
	}
}

func TestNoArgumentsSelectHelp(t *testing.T) {
	t.Parallel()

	args := commandArgs(nil)
	if len(args) != 1 || args[0] != "--help" {
		t.Fatalf("commandArgs(nil) = %v", args)
	}
}

func TestEnvironmentDefaultsReachKong(t *testing.T) {
	t.Setenv("EDGAR_FROM_YEAR", "2024")
	t.Setenv("EDGAR_SET", "golden")
	t.Setenv("EDGAR_NATS_URL", "nats://example.test:4222")

	defaults, err := commandDefaultsFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}

	var model cli

	parser, err := newParser(&model, defaults)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := parser.Parse([]string{"backfill"}); err != nil {
		t.Fatal(err)
	}

	if model.Backfill.FromYear != 2024 || model.Backfill.Set != "golden" ||
		model.Backfill.NATSURL != "nats://example.test:4222" {
		t.Fatalf("environment defaults not applied: %+v", model.Backfill)
	}
}

func TestChoppieContainsNoCLIParser(t *testing.T) {
	t.Parallel()

	directory := filepath.Join("..", "..", "internal", "choppie")

	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}

		source := string(data)
		if strings.Contains(source, `"flag"`) ||
			strings.Contains(source, `github.com/alecthomas/kong`) {
			t.Errorf("%s contains CLI parsing", entry.Name())
		}
	}
}
