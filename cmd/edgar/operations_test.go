package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nats-io/nats.go"

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

func TestConsumerActivity(t *testing.T) {
	tests := []struct {
		name string
		info nats.ConsumerInfo
		want string
	}{
		{
			name: "working",
			info: nats.ConsumerInfo{NumAckPending: 1},
			want: "working",
		},
		{
			name: "waiting",
			info: nats.ConsumerInfo{NumWaiting: 1},
			want: "waiting",
		},
		{
			name: "not observed",
			info: nats.ConsumerInfo{},
			want: "idle-or-stopped",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := consumerActivity(&test.info); got != test.want {
				t.Fatalf("consumerActivity() = %q, want %q", got, test.want)
			}
		})
	}
}
