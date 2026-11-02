package choppie

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"wingman.com/chopstick/internal/appdata"
	"wingman.com/chopstick/internal/transport"
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

func TestSubmitIndexImportSendsTypedRequest(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(),
		"tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()

	received := make(chan transport.Request, 1)
	serverErr := make(chan error, 1)

	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErr <- acceptErr

			return
		}
		defer func() { _ = connection.Close() }()

		payload, readErr := io.ReadAll(connection)
		if readErr != nil {
			serverErr <- readErr

			return
		}

		var request transport.Request
		if decodeErr := json.Unmarshal(payload, &request); decodeErr != nil {
			serverErr <- decodeErr

			return
		}

		received <- request

		_, serverErrValue := connection.Write(
			[]byte(`{"index_import":{"accepted":true}}`))
		serverErr <- serverErrValue
	}()

	err = SubmitIndexImport(t.Context(), IndexImportConfig{
		Address: listener.Addr().String(), File: "/tmp/2025-QTR1.tsv",
		Latest: false, Year: 2025, FromYear: 0, ToYear: 0, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	request := <-received
	if request.Command != transport.CommandImportIndex ||
		request.IndexImport == nil || request.IndexImport.Year != 2025 ||
		!request.IndexImport.DryRun {
		t.Fatalf("request = %+v", request)
	}

	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}
