package news

import (
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestNewsSchemaRecordsCurrentVersion(t *testing.T) {
	t.Parallel()

	databasePath := filepath.Join(t.TempDir(), "feed.db")

	store, created, err := Open(t.Context(), databasePath)
	if err != nil {
		t.Fatal(err)
	}

	if !created {
		t.Fatal("fresh news database was not reported as created")
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

	if version != newsSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version,
			newsSchemaVersion)
	}
}

func TestNewsSchemaRejectsUnversionedDatabase(t *testing.T) {
	t.Parallel()

	databasePath := filepath.Join(t.TempDir(), "feed.db")

	database, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := database.ExecContext(t.Context(), `CREATE TABLE
		feed_items (stream_sequence INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}

	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	_, _, err = Open(t.Context(), databasePath)
	if err == nil {
		t.Fatal("Open() succeeded for an unversioned news database")
	}

	if !strings.Contains(err.Error(), "make rebuild-edgar") {
		t.Fatalf("error %q does not explain how to rebuild", err)
	}
}

func TestConcurrentNewsStoreInitialization(t *testing.T) {
	const workers = 12

	databasePath := filepath.Join(t.TempDir(), "feed.db")
	errorsCh := make(chan error, workers)

	var (
		createdCount atomic.Int64
		group        sync.WaitGroup
	)

	for range workers {
		group.Go(func() {
			store, created, err := Open(t.Context(), databasePath)
			if created {
				createdCount.Add(1)
			}

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

	if createdCount.Load() != 1 {
		t.Fatalf("created count = %d, want 1", createdCount.Load())
	}
}
