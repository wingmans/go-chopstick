package indeximport

import (
	"os"
	"path/filepath"
	"testing"
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

func TestAccessionFromPath(t *testing.T) {
	got := accessionFromPath("edgar/data/1/000012345625000001.txt")
	if got != "0000123456-25-000001" {
		t.Fatalf("accession = %q", got)
	}
}
