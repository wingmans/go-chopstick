package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"wingman.com/chopstick/internal/appdata"
)

func TestPathSizeAndFormatBytes(t *testing.T) {
	directory := t.TempDir()

	path := filepath.Join(directory, "payload")
	if err := os.WriteFile(path, make([]byte, 1536), 0o600); err != nil {
		t.Fatal(err)
	}

	bytes, err := pathSize(directory)
	if err != nil {
		t.Fatalf("pathSize returned error: %v", err)
	}

	if bytes != 1536 {
		t.Fatalf("pathSize = %d, want 1536", bytes)
	}

	if got := formatBytes(bytes); got != "1.5 KiB" {
		t.Fatalf("formatBytes = %q, want %q", got, "1.5 KiB")
	}
}

func TestCheckConfiguredSetReportsMissingFile(t *testing.T) {
	t.Setenv(appdata.EnvName, t.TempDir())
	t.Setenv(edgarSetEnv, "missing")

	report := doctorReport{}
	checkConfiguredSet(&report)

	if report.failures != 1 {
		t.Fatalf("failures = %d, want 1", report.failures)
	}
}

func TestOpenSQLiteReadOnlyDoesNotCreateDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")

	if _, err := openSQLiteReadOnly(t.Context(), path); !errors.Is(err,
		os.ErrNotExist) {
		t.Fatalf("openSQLiteReadOnly() error = %v, want os.ErrNotExist", err)
	}

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only inspection created database: %v", err)
	}
}
