package ingest

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestSelectFilesUsesYearRange(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{
		"2024-QTR4.tsv", "2025-QTR1.tsv", "2025-QTR2.tsv", "ignore.txt",
	} {
		if err := os.WriteFile(filepath.Join(directory, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	files, err := selectFiles(Config{
		IndexesDirectory: directory,
		PollingDB:        "polling.db",
		NATSURL:          "nats://127.0.0.1:4222",
		Year:             2025,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(files) != 2 || filepath.Base(files[0]) != "2025-QTR1.tsv" ||
		filepath.Base(files[1]) != "2025-QTR2.tsv" {
		t.Fatalf("selected files = %v", files)
	}
}

func TestConfigRequiresOneSelection(t *testing.T) {
	config := Config{
		IndexesDirectory: t.TempDir(),
		PollingDB:        "polling.db",
		NATSURL:          "nats://127.0.0.1:4222",
	}

	if err := config.Validate(); err == nil {
		t.Fatal("Validate accepted an empty selection")
	}
}

func TestEnsureMasterIndexStitchesEveryLocalQuarter(t *testing.T) {
	directory := t.TempDir()
	masterPath := filepath.Join(t.TempDir(), "master.tsv")

	writeIndexFile(t, directory, "2024-QTR4.tsv", "older\n")
	writeIndexFile(t, directory, "2025-QTR1.tsv", "selected\n")

	config := Config{
		IndexesDirectory: directory,
		MasterPath:       masterPath,
		PollingDB:        "polling.db",
		NATSURL:          "nats://127.0.0.1:4222",
		Year:             2025,
	}
	if err := ensureMasterIndex(t.Context(), config,
		slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(masterPath)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := string(data), "older\nselected\n"; got != want {
		t.Fatalf("master = %q, want %q", got, want)
	}
}

func TestEnsureMasterIndexRefreshesStaleMaster(t *testing.T) {
	directory := t.TempDir()
	masterPath := filepath.Join(t.TempDir(), "master.tsv")
	quarterlyPath := writeIndexFile(t, directory, "2025-QTR1.tsv", "new\n")

	if err := os.WriteFile(masterPath, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	old := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)

	newer := old.Add(time.Hour)
	if err := os.Chtimes(masterPath, old, old); err != nil {
		t.Fatal(err)
	}

	if err := os.Chtimes(quarterlyPath, newer, newer); err != nil {
		t.Fatal(err)
	}

	config := Config{
		IndexesDirectory: directory,
		MasterPath:       masterPath,
		PollingDB:        "polling.db",
		NATSURL:          "nats://127.0.0.1:4222",
		Latest:           true,
	}
	if err := ensureMasterIndex(t.Context(), config,
		slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(masterPath)
	if err != nil {
		t.Fatal(err)
	}

	if string(data) != "new\n" {
		t.Fatalf("master = %q, want new content", data)
	}
}

func TestInspectReportsAvailableAndImportedPeriods(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "quarterly")
	masterPath := filepath.Join(root, "master.tsv")

	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}

	first := writeIndexFile(t, directory, "2024-QTR4.tsv", "first\n")

	second := writeIndexFile(t, directory, "2025-QTR2.tsv", "second\n")
	if err := os.WriteFile(masterPath, []byte("first\nsecond\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()

	older := now.Add(-time.Hour)
	for _, path := range []string{first, second} {
		if err := os.Chtimes(path, older, older); err != nil {
			t.Fatal(err)
		}
	}

	if err := recordImport(masterPath, []string{first}, ImportSummary{
		CommittedAt: now, Examined: 10, Queued: 8, Duplicates: 2,
	}); err != nil {
		t.Fatal(err)
	}

	if err := recordImport(masterPath, []string{second}, ImportSummary{
		CommittedAt: now.Add(time.Minute), Examined: 5,
		Queued: 4, Duplicates: 1,
	}); err != nil {
		t.Fatal(err)
	}

	inventory, err := Inspect(directory, masterPath)
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(inventory.AvailablePeriods,
		[]string{"2024-QTR4", "2025-QTR2"}) {
		t.Fatalf("available periods = %v", inventory.AvailablePeriods)
	}

	if !slices.Equal(inventory.MissingPeriods, []string{"2025-QTR1"}) {
		t.Fatalf("missing periods = %v", inventory.MissingPeriods)
	}

	if !slices.Equal(inventory.ImportedPeriods,
		[]string{"2024-QTR4", "2025-QTR2"}) {
		t.Fatalf("imported periods = %v", inventory.ImportedPeriods)
	}

	if inventory.MasterState != "current" || inventory.LastImport == nil ||
		inventory.LastImport.Queued != 4 ||
		!slices.Equal(inventory.LastImport.Periods,
			[]string{"2025-QTR2"}) {
		t.Fatalf("inventory = %+v", inventory)
	}
}

func TestInspectReportsMissingEmptyInventory(t *testing.T) {
	root := t.TempDir()

	inventory, err := Inspect(filepath.Join(root, "quarterly"),
		filepath.Join(root, "master.tsv"))
	if err != nil {
		t.Fatal(err)
	}

	if inventory.MasterState != "missing" ||
		len(inventory.AvailablePeriods) != 0 ||
		len(inventory.ImportedPeriods) != 0 {
		t.Fatalf("inventory = %+v", inventory)
	}
}

func TestRunDryRunDoesNotStitchOrConnect(t *testing.T) {
	directory := t.TempDir()
	masterPath := filepath.Join(t.TempDir(), "master.tsv")
	writeIndexFile(t, directory, "2025-QTR1.tsv",
		"1|ACME|10-K|2025-01-02|edgar/data/1/000000000125000001.txt\n")

	progress := make([]string, 0)

	config := Config{
		IndexesDirectory: directory,
		MasterPath:       masterPath,
		PollingDB:        "polling.db",
		NATSURL:          "nats://network-must-not-be-used:4222",
		Latest:           true,
		DryRun:           true,
		Progress: func(update Progress) {
			progress = append(progress, update.Phase)
		},
	}
	if err := RunIndex(context.Background(), config,
		slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(masterPath); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote master index: %v", err)
	}

	for _, phase := range []string{"selected", "stitching", "read", "dry-run"} {
		if !slices.Contains(progress, phase) {
			t.Errorf("progress %v is missing %q", progress, phase)
		}
	}
}

func TestRunExplicitFileDoesNotRequireQuarterlyCache(t *testing.T) {
	file := writeIndexFile(t, t.TempDir(), "external.tsv",
		"1|ACME|10-K|2025-01-02|edgar/data/1/000000000125000001.txt\n")
	root := t.TempDir()

	config := Config{
		IndexesDirectory: filepath.Join(root, "missing-quarterly-cache"),
		MasterPath:       filepath.Join(root, "master.tsv"),
		PollingDB:        filepath.Join(root, "polling.db"),
		NATSURL:          "nats://network-must-not-be-used:4222",
		File:             file,
		DryRun:           true,
	}
	if err := RunIndex(t.Context(), config,
		slog.New(slog.DiscardHandler)); err != nil {
		t.Fatal(err)
	}
}

func TestAccessionFromPath(t *testing.T) {
	got := accessionFromPath("edgar/data/1/000012345625000001.txt")
	if got != "0000123456-25-000001" {
		t.Fatalf("accession = %q", got)
	}
}

func writeIndexFile(t *testing.T, directory, name, contents string) string {
	t.Helper()

	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}
