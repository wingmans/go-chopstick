package edgar

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteChecksumCreatesJSONSidecar(t *testing.T) {
	directory := t.TempDir()

	indexPath := filepath.Join(directory, "2010-QTR1.tsv")
	if err := os.WriteFile(indexPath, []byte("header\nrecord\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	checksumPath, err := writeChecksum(indexPath, "network", "https://example.test/index.zip")
	if err != nil {
		t.Fatalf("writeChecksum returned error: %v", err)
	}

	if got, want := checksumPath, filepath.Join(directory, "2010-QTR1-CHK.txt"); got != want {
		t.Fatalf("checksum path = %q, want %q", got, want)
	}

	data, err := os.ReadFile(checksumPath)
	if err != nil {
		t.Fatal(err)
	}

	var record ChecksumRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("checksum is not JSON: %v", err)
	}

	if record.Path != indexPath || record.SizeBytes != int64(len("header\nrecord\n")) ||
		record.Source != "network" || record.SourceURL != "https://example.test/index.zip" ||
		record.SHA256 == "" || record.CheckedAt.IsZero() {
		t.Fatalf("unexpected checksum record: %#v", record)
	}
}

func TestChecksumPath(t *testing.T) {
	if got, want := ChecksumPath("data/indexes/2010-QTR1.tsv"), "data/indexes/2010-QTR1-CHK.txt"; got != want {
		t.Fatalf("ChecksumPath = %q, want %q", got, want)
	}
}
