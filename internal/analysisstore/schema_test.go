package analysisstore

import (
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestAnalysisSchemaRecordsCurrentVersion(t *testing.T) {
	t.Parallel()

	databasePath := filepath.Join(t.TempDir(), "analysis.db")

	store, err := Open(t.Context(), databasePath)
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

	if version != analysisSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version,
			analysisSchemaVersion)
	}
}

func TestAnalysisSchemaRejectsUnversionedDatabase(t *testing.T) {
	t.Parallel()

	databasePath := filepath.Join(t.TempDir(), "analysis.db")

	database, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := database.ExecContext(t.Context(), `CREATE TABLE
		filing_views (cik TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = Open(t.Context(), databasePath)
	if err == nil {
		t.Fatal("Open() succeeded for an unversioned analysis database")
	}

	if !strings.Contains(err.Error(), "make rebuild-edgar") {
		t.Fatalf("error %q does not explain how to rebuild", err)
	}
}

func TestConcurrentAnalysisStoreInitialization(t *testing.T) {
	const workers = 12

	databasePath := filepath.Join(t.TempDir(), "analysis.db")
	errorsCh := make(chan error, workers)

	var group sync.WaitGroup

	for range workers {
		group.Go(func() {
			store, err := Open(t.Context(), databasePath)
			if err == nil {
				err = store.Close()
			}

			errorsCh <- err
		})
	}

	group.Wait()
	close(errorsCh)

	for err := range errorsCh {
		if err != nil {
			t.Fatalf("concurrent initialization failed: %v", err)
		}
	}
}
