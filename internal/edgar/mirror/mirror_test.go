package mirror

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const testAccession = "0000000123-25-000001"

func testSubmission() []byte {
	return []byte(`<SEC-DOCUMENT>` + testAccession + `
<SEC-HEADER>
ACCESSION NUMBER: ` + testAccession + `
CONFORMED SUBMISSION TYPE: 10-Q
FILED AS OF DATE: 20250131
CENTRAL INDEX KEY: 0000000123
</SEC-HEADER>
</SEC-DOCUMENT>
`)
}

func testKey() string {
	return "edgar/data/123/" + testAccession + ".txt"
}

func TestFileStoreRoundTripAndWalk(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	payload := bytes.Repeat(testSubmission(), 100)

	info, err := store.Put(t.Context(), testKey(), bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}

	if info.LogicalBytes != int64(len(payload)) || info.SHA256 == "" {
		t.Fatalf("unexpected object info: %+v", info)
	}

	reader, opened, err := store.Open(t.Context(), testKey())
	if err != nil {
		t.Fatal(err)
	}

	actual, err := io.ReadAll(reader)
	_ = reader.Close()

	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(actual, payload) || opened.StoredBytes == 0 {
		t.Fatal("stored submission did not round trip")
	}

	var keys []string

	if err := store.Walk(t.Context(), func(value ObjectInfo) error {
		keys = append(keys, value.Key)

		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if len(keys) != 1 || keys[0] != testKey() {
		t.Fatalf("walk keys = %v", keys)
	}
}

func TestFileStoreRejectsTraversal(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.Put(t.Context(), "../submission.txt",
		strings.NewReader("bad"))
	if !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("traversal error = %v", err)
	}
}

func TestFileStoreCanceledWriteLeavesNoObject(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := store.Put(ctx, testKey(),
		bytes.NewReader(testSubmission())); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled put error = %v", err)
	}

	if _, err := store.Stat(t.Context(), testKey()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stat after cancellation = %v", err)
	}
}

func TestCatalogRebuildAfterDeletion(t *testing.T) {
	root := t.TempDir()

	store, err := NewFileStore(filepath.Join(root, "objects"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.Put(t.Context(), testKey(),
		bytes.NewReader(testSubmission())); err != nil {
		t.Fatal(err)
	}

	catalogPath := filepath.Join(root, "catalog.db")
	for range 2 {
		result, err := RebuildCatalog(t.Context(), catalogPath,
			filepath.Join(root, "tmp"), store)
		if err != nil {
			t.Fatal(err)
		}

		if result.Indexed != 1 || len(result.Problems) != 0 {
			t.Fatalf("rebuild result = %+v", result)
		}

		catalog, _, err := OpenCatalog(t.Context(), catalogPath)
		if err != nil {
			t.Fatal(err)
		}

		value, err := catalog.Get(t.Context(), testAccession)
		_ = catalog.Close()

		if err != nil {
			t.Fatal(err)
		}

		if value.CIK != "0000000123" || value.FormType != "10-Q" ||
			value.FilingYear != 2025 || value.SHA256 == "" {
			t.Fatalf("rebuilt submission = %+v", value)
		}

		if err := os.Remove(catalogPath); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCatalogRecordsAndGuardsSchemaVersion(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "catalog.db")

	catalog, created, err := OpenCatalog(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	var version int
	if err := catalog.db.QueryRowContext(t.Context(),
		"PRAGMA user_version",
	).Scan(&version); err != nil {
		t.Fatal(err)
	}

	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}

	if !created || version != mirrorSchemaVersion {
		t.Fatalf("created=%t version=%d", created, version)
	}

	legacyPath := filepath.Join(root, "legacy.db")

	db, err := sql.Open("sqlite3", legacyPath)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(t.Context(),
		"CREATE TABLE legacy (id INTEGER PRIMARY KEY)",
	); err != nil {
		t.Fatal(err)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if _, _, err := OpenCatalog(t.Context(), legacyPath); !errors.Is(
		err, ErrCatalogSchema,
	) {
		t.Fatalf("legacy catalog error = %v", err)
	}
}

func TestCatalogConcurrentInitialization(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")
	errorsCh := make(chan error, 8)

	var group sync.WaitGroup

	for range 8 {
		group.Go(func() {
			catalog, _, err := OpenCatalog(context.Background(), path)
			if err == nil {
				err = catalog.Close()
			}

			errorsCh <- err
		})
	}

	group.Wait()
	close(errorsCh)

	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestCatalogSearchPaginationAndBatchAtomicity(t *testing.T) {
	catalog, _, err := OpenCatalog(t.Context(),
		filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = catalog.Close() }()

	values := []Submission{
		{
			Accession: "0000000123-24-000001", CIK: "0000000123",
			FormType: "10-Q", FilingDate: "2024-01-01",
			FilingPath: "edgar/data/123/0000000123-24-000001.txt",
			FilingYear: 2024, LogicalBytes: 100, StoredBytes: 60,
			SHA256: "first",
		},
		{
			Accession: "0000000123-24-000002", CIK: "0000000123",
			FormType: "10-Q", FilingDate: "2024-04-01",
			FilingPath: "edgar/data/123/0000000123-24-000002.txt",
			FilingYear: 2024, LogicalBytes: 200, StoredBytes: 120,
			SHA256: "second",
		},
		{
			Accession: "0000000456-24-000001", CIK: "0000000456",
			FormType: "10-K", FilingDate: "2024-02-01",
			FilingPath: "edgar/data/456/0000000456-24-000001.txt",
			FilingYear: 2024, LogicalBytes: 300, StoredBytes: 180,
			SHA256: "third",
		},
	}
	if err := catalog.UpsertBatch(t.Context(), values); err != nil {
		t.Fatal(err)
	}

	filter := SearchFilter{
		CIK: "0000000123", FormType: "10-q", FilingYear: 2024, Limit: 1,
	}

	first, err := catalog.Search(t.Context(), filter)
	if err != nil {
		t.Fatal(err)
	}

	if len(first.Items) != 1 || first.Items[0].SHA256 != "first" ||
		first.NextCursor != first.Items[0].Accession {
		t.Fatalf("first page = %+v", first)
	}

	filter.Cursor = first.NextCursor

	second, err := catalog.Search(t.Context(), filter)
	if err != nil {
		t.Fatal(err)
	}

	if len(second.Items) != 1 || second.Items[0].SHA256 != "second" ||
		second.NextCursor != "" {
		t.Fatalf("second page = %+v", second)
	}

	duplicate := []Submission{
		{
			Accession: "duplicate", CIK: "0000000789", FormType: "8-K",
			FilingDate: "2024-03-01", FilingYear: 2024,
			FilingPath: "edgar/data/789/first.txt",
		},
		{
			Accession: "duplicate", CIK: "0000000789", FormType: "8-K",
			FilingDate: "2024-03-01", FilingYear: 2024,
			FilingPath: "edgar/data/789/second.txt",
		},
	}
	if err := catalog.UpsertBatch(t.Context(), duplicate); err == nil {
		t.Fatal("duplicate accession batch succeeded")
	}

	if _, err := catalog.Get(t.Context(), "duplicate"); !errors.Is(
		err, ErrNotFound,
	) {
		t.Fatalf("rolled-back batch lookup = %v", err)
	}
}

func TestImportIsIdempotentAndLeavesSourceUnchanged(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	sourcePath := filepath.Join(source, filepath.FromSlash(testKey()))

	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o750); err != nil {
		t.Fatal(err)
	}

	payload := testSubmission()
	if err := os.WriteFile(sourcePath, payload, 0o600); err != nil {
		t.Fatal(err)
	}

	store, _ := NewFileStore(filepath.Join(root, "objects"))

	catalog, _, err := OpenCatalog(t.Context(),
		filepath.Join(root, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = catalog.Close() }()

	service, _ := NewService(store, catalog, nil, filepath.Join(root, "tmp"))

	first, err := service.Import(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}

	second, err := service.Import(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}

	after, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}

	stats, err := service.Stats(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if first != 1 || second != 0 || stats.Objects != 1 {
		t.Fatalf("first=%d second=%d objects=%d", first, second, stats.Objects)
	}

	if !bytes.Equal(after, payload) {
		t.Fatal("import changed the source submission")
	}
}

func TestVerifyReportsCorruption(t *testing.T) {
	root := t.TempDir()

	store, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := store.Put(t.Context(), testKey(),
		bytes.NewReader(testSubmission())); err != nil {
		t.Fatal(err)
	}

	path, err := store.objectPath(testKey())
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}

	valid, problems, err := Verify(t.Context(), store,
		filepath.Join(root, "tmp"))
	if err != nil {
		t.Fatal(err)
	}

	if valid != 0 || len(problems) != 1 {
		t.Fatalf("valid=%d problems=%v", valid, problems)
	}
}

func TestRepairReplacesCorruptObject(t *testing.T) {
	root := t.TempDir()

	store, _ := NewFileStore(filepath.Join(root, "objects"))
	if _, err := store.Put(t.Context(), testKey(),
		bytes.NewReader(testSubmission())); err != nil {
		t.Fatal(err)
	}

	path, _ := store.objectPath(testKey())
	if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}

	catalog, _, err := OpenCatalog(t.Context(),
		filepath.Join(root, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = catalog.Close() }()

	origin := &fakeOrigin{payload: testSubmission()}

	repaired, problems, err := Repair(t.Context(), store, catalog, origin,
		filepath.Join(root, "tmp"))
	if err != nil {
		t.Fatal(err)
	}

	if repaired != 1 || len(problems) != 0 || origin.Calls() != 1 {
		t.Fatalf("repaired=%d problems=%v calls=%d", repaired, problems,
			origin.Calls())
	}

	if _, err := catalog.Get(t.Context(), testAccession); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureRetrievesOnceAndThenUsesStore(t *testing.T) {
	root := t.TempDir()

	store, err := NewFileStore(filepath.Join(root, "objects"))
	if err != nil {
		t.Fatal(err)
	}

	catalog, _, err := OpenCatalog(t.Context(),
		filepath.Join(root, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = catalog.Close() }()

	origin := &fakeOrigin{payload: testSubmission()}

	service, err := NewService(store, catalog, origin,
		filepath.Join(root, "tmp"))
	if err != nil {
		t.Fatal(err)
	}

	ref := SubmissionRef{
		Accession: testAccession, CIK: "0000000123", FormType: "10-Q",
		FilingDate: "2025-01-31", FilingPath: testKey(),
	}
	for range 2 {
		value, err := service.Ensure(t.Context(), ref)
		if err != nil {
			t.Fatal(err)
		}

		if value.Accession != testAccession || value.CIK != "0000000123" {
			t.Fatalf("ensured submission = %+v", value)
		}
	}

	if origin.Calls() != 1 {
		t.Fatalf("origin calls = %d", origin.Calls())
	}
}

func TestEnsureRejectsDiscoveryMismatch(t *testing.T) {
	root := t.TempDir()
	store, _ := NewFileStore(filepath.Join(root, "objects"))

	catalog, _, err := OpenCatalog(t.Context(),
		filepath.Join(root, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = catalog.Close() }()

	origin := &fakeOrigin{payload: testSubmission()}
	service, _ := NewService(store, catalog, origin, filepath.Join(root, "tmp"))
	ref := SubmissionRef{
		Accession: testAccession, CIK: "123", FormType: "10-K",
		FilingDate: "20250131", FilingPath: testKey(),
	}

	if _, err := service.Ensure(t.Context(), ref); !errors.Is(
		err, ErrCorruptObject,
	) {
		t.Fatalf("mismatch error = %v", err)
	}
}

func TestConcurrentEnsureSharesRetrieval(t *testing.T) {
	root := t.TempDir()
	store, _ := NewFileStore(filepath.Join(root, "objects"))

	catalog, _, err := OpenCatalog(t.Context(),
		filepath.Join(root, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = catalog.Close() }()

	origin := &fakeOrigin{payload: testSubmission()}
	service, _ := NewService(store, catalog, origin, filepath.Join(root, "tmp"))
	ref := SubmissionRef{
		Accession: testAccession, CIK: "123", FormType: "10-Q",
		FilingDate: "20250131", FilingPath: testKey(),
	}

	var group sync.WaitGroup

	errorsCh := make(chan error, 4)

	for range 4 {
		group.Go(func() {
			_, err := service.Ensure(context.Background(), ref)
			errorsCh <- err
		})
	}

	group.Wait()
	close(errorsCh)

	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	if origin.Calls() != 1 {
		t.Fatalf("origin calls = %d", origin.Calls())
	}
}

type fakeOrigin struct {
	payload []byte
	mu      sync.Mutex
	calls   int
}

func (o *fakeOrigin) Fetch(_ context.Context, _ SubmissionRef) (
	io.ReadCloser, error,
) {
	o.mu.Lock()
	o.calls++
	o.mu.Unlock()

	return io.NopCloser(bytes.NewReader(o.payload)), nil
}

func (o *fakeOrigin) Calls() int {
	o.mu.Lock()
	defer o.mu.Unlock()

	return o.calls
}
