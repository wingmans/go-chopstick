package mirror

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"wingman.com/fetch-ecb/internal/mirror/dbgen"

	_ "github.com/mattn/go-sqlite3"
)

const catalogBatchSize = 250

// Catalog is the disposable query index for mirrored submissions.
type Catalog struct {
	db      *sql.DB
	queries *dbgen.Queries
}

// OpenCatalog opens a catalog and reports whether its file was absent.
func OpenCatalog(ctx context.Context, path string) (*Catalog, bool, error) {
	_, statErr := os.Stat(path)

	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return nil, false, fmt.Errorf("inspect mirror catalog: %w", statErr)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, false, fmt.Errorf("create mirror catalog directory: %w", err)
	}

	db, err := sql.Open(
		"sqlite3",
		path+"?_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate",
	)
	if err != nil {
		return nil, false, fmt.Errorf("open mirror catalog: %w", err)
	}

	db.SetMaxOpenConns(1)

	if err := createCatalogSchema(ctx, db); err != nil {
		_ = db.Close()

		return nil, false, err
	}

	queries, err := dbgen.Prepare(ctx, db)
	if err != nil {
		_ = db.Close()

		return nil, false, fmt.Errorf("prepare mirror catalog queries: %w", err)
	}

	return &Catalog{db: db, queries: queries}, created, nil
}

// Close closes the catalog.
func (c *Catalog) Close() error {
	return errors.Join(c.queries.Close(), c.db.Close())
}

// Upsert records metadata derived from an authoritative raw object.
func (c *Catalog) Upsert(ctx context.Context, value Submission) error {
	if err := upsertSubmission(ctx, c.queries, value); err != nil {
		return fmt.Errorf("upsert mirror catalog: %w", err)
	}

	return nil
}

// UpsertBatch records a bounded group in one transaction.
func (c *Catalog) UpsertBatch(ctx context.Context,
	values []Submission,
) error {
	if len(values) == 0 {
		return nil
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin mirror catalog batch: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	queries := c.queries.WithTx(tx)
	for _, value := range values {
		if err := upsertSubmission(ctx, queries, value); err != nil {
			return fmt.Errorf("upsert mirror catalog batch: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit mirror catalog batch: %w", err)
	}

	return nil
}

// Get returns a submission by accession.
func (c *Catalog) Get(ctx context.Context, accession string) (
	Submission, error,
) {
	row, err := c.queries.SubmissionByAccession(ctx,
		strings.TrimSpace(accession))
	if errors.Is(err, sql.ErrNoRows) {
		return Submission{}, ErrNotFound
	}

	if err != nil {
		return Submission{}, fmt.Errorf("read mirror catalog: %w", err)
	}

	return submissionFromRow(row), nil
}

// Search returns one stable accession-ordered page.
func (c *Catalog) Search(ctx context.Context, filter SearchFilter) (
	SubmissionPage, error,
) {
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	rows, err := c.queries.SearchSubmissions(ctx,
		dbgen.SearchSubmissionsParams{
			CIK:        strings.TrimSpace(filter.CIK),
			FormType:   strings.ToUpper(strings.TrimSpace(filter.FormType)),
			FilingYear: int64(filter.FilingYear),
			Cursor:     strings.TrimSpace(filter.Cursor),
			RowLimit:   int64(limit + 1),
		})
	if err != nil {
		return SubmissionPage{}, fmt.Errorf("search mirror catalog: %w", err)
	}

	items := make([]Submission, 0, len(rows))
	for _, row := range rows {
		items = append(items, submissionFromRow(row))
	}

	page := SubmissionPage{Items: items}
	if len(page.Items) > limit {
		page.NextCursor = page.Items[limit-1].Accession
		page.Items = page.Items[:limit]
	}

	return page, nil
}

// Stats returns aggregate compression statistics from the catalog.
func (c *Catalog) Stats(ctx context.Context) (Stats, error) {
	row, err := c.queries.CatalogStats(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("read mirror statistics: %w", err)
	}

	result := Stats{
		Objects: row.ObjectCount, LogicalBytes: row.LogicalBytes,
		StoredBytes: row.StoredBytes,
	}

	result.SavedBytes = result.LogicalBytes - result.StoredBytes
	if result.LogicalBytes > 0 {
		result.Ratio = float64(result.StoredBytes) /
			float64(result.LogicalBytes)
	}

	return result, nil
}

// Delete removes one stale catalog row.
func (c *Catalog) Delete(ctx context.Context, key string) error {
	if err := c.queries.DeleteSubmission(ctx, key); err != nil {
		return fmt.Errorf("delete mirror catalog row: %w", err)
	}

	return nil
}

// IntegrityCheck checks SQLite's own structural integrity.
func (c *Catalog) IntegrityCheck(ctx context.Context) error {
	var result string
	if err := c.db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(
		&result,
	); err != nil {
		return fmt.Errorf("check mirror catalog integrity: %w", err)
	}

	if result != "ok" {
		return fmt.Errorf("mirror catalog integrity check: %s", result)
	}

	return nil
}

// Checkpoint flushes the derived WAL before an atomic catalog replacement.
func (c *Catalog) Checkpoint(ctx context.Context) error {
	if _, err := c.db.ExecContext(ctx,
		`PRAGMA wal_checkpoint(TRUNCATE)`,
	); err != nil {
		return fmt.Errorf("checkpoint mirror catalog: %w", err)
	}

	return nil
}

func upsertSubmission(ctx context.Context, queries *dbgen.Queries,
	value Submission,
) error {
	return queries.UpsertSubmission(ctx, dbgen.UpsertSubmissionParams{
		ObjectKey: value.FilingPath, Accession: value.Accession,
		CIK: value.CIK, FormType: value.FormType,
		FilingDate: value.FilingDate, FilingYear: int64(value.FilingYear),
		LogicalBytes: value.LogicalBytes, StoredBytes: value.StoredBytes,
		Sha256: value.SHA256,
	})
}

func submissionFromRow(row dbgen.Submission) Submission {
	return Submission{
		Accession: row.Accession, CIK: row.CIK,
		FormType: row.FormType, FilingDate: row.FilingDate,
		FilingPath: row.ObjectKey,
		FilingYear: int(row.FilingYear), LogicalBytes: row.LogicalBytes,
		StoredBytes: row.StoredBytes, SHA256: row.Sha256,
	}
}

func filingYear(date string) (int, error) {
	date = strings.ReplaceAll(strings.TrimSpace(date), "-", "")
	if len(date) != 8 {
		return 0, fmt.Errorf("invalid SEC filing date %q", date)
	}

	year, err := strconv.Atoi(date[:4])
	if err != nil {
		return 0, fmt.Errorf("invalid SEC filing date %q: %w", date, err)
	}

	return year, nil
}

func normalizedFilingDate(date string) (string, error) {
	date = strings.ReplaceAll(strings.TrimSpace(date), "-", "")
	if _, err := time.Parse("20060102", date); err != nil {
		return "", err
	}

	return date[:4] + "-" + date[4:6] + "-" + date[6:], nil
}
