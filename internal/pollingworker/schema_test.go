package pollingworker

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestPollingSchemaRecordsCurrentVersion(t *testing.T) {
	t.Parallel()

	databasePath := filepath.Join(t.TempDir(), "polling.db")

	store, err := NewDedupeServiceContext(t.Context(), databasePath)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	database, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = database.Close() })

	var version int
	if err := database.QueryRowContext(
		t.Context(),
		"PRAGMA user_version",
	).Scan(&version); err != nil {
		t.Fatal(err)
	}

	if version != pollingSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version,
			pollingSchemaVersion)
	}
}

func TestPollingSchemaRejectsUnversionedDatabase(t *testing.T) {
	t.Parallel()

	databasePath := filepath.Join(t.TempDir(), "polling.db")

	database, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := database.ExecContext(t.Context(), `CREATE TABLE
		processed_accessions (accession_number TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}

	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = NewDedupeServiceContext(t.Context(), databasePath)
	if err == nil {
		t.Fatal("NewDedupeServiceContext() succeeded for old schema")
	}

	if !strings.Contains(err.Error(), "make rebuild-edgar") {
		t.Fatalf("error %q does not explain how to rebuild", err)
	}
}
