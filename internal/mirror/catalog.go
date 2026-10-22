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

	_ "github.com/mattn/go-sqlite3"
)

// Catalog is the disposable query index for mirrored submissions.
type Catalog struct {
	db *sql.DB
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

	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, false, fmt.Errorf("open mirror catalog: %w", err)
	}

	catalog := &Catalog{db: db}
	if err := catalog.createSchema(ctx); err != nil {
		_ = db.Close()

		return nil, false, err
	}

	return catalog, created, nil
}

func (c *Catalog) createSchema(ctx context.Context) error { //nolint:funcorder
	_, err := c.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS submissions (
			object_key TEXT PRIMARY KEY,
			accession TEXT NOT NULL UNIQUE,
			cik TEXT NOT NULL,
			form_type TEXT NOT NULL,
			filing_date TEXT NOT NULL,
			filing_year INTEGER NOT NULL,
			logical_bytes INTEGER NOT NULL,
			stored_bytes INTEGER NOT NULL,
			sha256 TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS submissions_cik_date
			ON submissions(cik, filing_date);
		CREATE INDEX IF NOT EXISTS submissions_form_date
			ON submissions(form_type, filing_date);
		CREATE INDEX IF NOT EXISTS submissions_year_form
			ON submissions(filing_year, form_type);
	`)
	if err != nil {
		return fmt.Errorf("create mirror catalog schema: %w", err)
	}

	return nil
}

// Close closes the catalog.
func (c *Catalog) Close() error { return c.db.Close() }

// Upsert records metadata derived from an authoritative raw object.
func (c *Catalog) Upsert(ctx context.Context, value Submission) error {
	_, err := c.db.ExecContext(ctx, `
		INSERT INTO submissions (
			object_key, accession, cik, form_type, filing_date, filing_year,
			logical_bytes, stored_bytes, sha256
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(object_key) DO UPDATE SET
			accession = excluded.accession,
			cik = excluded.cik,
			form_type = excluded.form_type,
			filing_date = excluded.filing_date,
			filing_year = excluded.filing_year,
			logical_bytes = excluded.logical_bytes,
			stored_bytes = excluded.stored_bytes,
			sha256 = excluded.sha256`, value.FilingPath, value.Accession,
		value.CIK, value.FormType, value.FilingDate, value.FilingYear,
		value.LogicalBytes, value.StoredBytes, value.SHA256)
	if err != nil {
		return fmt.Errorf("upsert mirror catalog: %w", err)
	}

	return nil
}

// Get returns a submission by accession.
func (c *Catalog) Get(ctx context.Context, accession string) (
	Submission, error,
) {
	row := c.db.QueryRowContext(ctx, `
		SELECT object_key, accession, cik, form_type, filing_date,
			filing_year, logical_bytes, stored_bytes, sha256
		FROM submissions WHERE accession = ?`, strings.TrimSpace(accession))

	value, err := scanSubmission(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Submission{}, ErrNotFound
	}

	if err != nil {
		return Submission{}, fmt.Errorf("read mirror catalog: %w", err)
	}

	return value, nil
}

// Search returns one stable accession-ordered page.
func (c *Catalog) Search(ctx context.Context, filter SearchFilter) (
	SubmissionPage, error,
) {
	clauses := []string{"1 = 1"}
	args := make([]any, 0, 6)

	if strings.TrimSpace(filter.CIK) != "" {
		clauses = append(clauses, "cik = ?")
		args = append(args, strings.TrimSpace(filter.CIK))
	}

	if strings.TrimSpace(filter.FormType) != "" {
		clauses = append(clauses, "form_type = ?")
		args = append(args, strings.ToUpper(strings.TrimSpace(filter.FormType)))
	}

	if filter.FilingYear != 0 {
		clauses = append(clauses, "filing_year = ?")
		args = append(args, filter.FilingYear)
	}

	if strings.TrimSpace(filter.Cursor) != "" {
		clauses = append(clauses, "accession > ?")
		args = append(args, strings.TrimSpace(filter.Cursor))
	}

	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	args = append(args, limit+1)

	// #nosec G202 -- Every clause is a fixed string selected above.
	query := `SELECT object_key, accession, cik, form_type, filing_date,
		filing_year, logical_bytes, stored_bytes, sha256
		FROM submissions WHERE ` + strings.Join(clauses, " AND ") +
		" ORDER BY accession LIMIT ?"

	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return SubmissionPage{}, fmt.Errorf("search mirror catalog: %w", err)
	}

	defer func() { _ = rows.Close() }()

	items := make([]Submission, 0, limit+1)

	for rows.Next() {
		value, scanErr := scanSubmission(rows)
		if scanErr != nil {
			return SubmissionPage{}, fmt.Errorf("scan mirror search: %w", scanErr)
		}

		items = append(items, value)
	}

	if err := rows.Err(); err != nil {
		return SubmissionPage{}, err
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
	var result Stats

	err := c.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(logical_bytes), 0),
			COALESCE(SUM(stored_bytes), 0)
		FROM submissions`).Scan(&result.Objects, &result.LogicalBytes,
		&result.StoredBytes)
	if err != nil {
		return Stats{}, fmt.Errorf("read mirror statistics: %w", err)
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
	_, err := c.db.ExecContext(ctx,
		`DELETE FROM submissions WHERE object_key = ?`, key)
	if err != nil {
		return fmt.Errorf("delete mirror catalog row: %w", err)
	}

	return nil
}

// IntegrityCheck checks SQLite's own structural integrity.
func (c *Catalog) IntegrityCheck(ctx context.Context) error {
	var result string
	if err := c.db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return fmt.Errorf("check mirror catalog integrity: %w", err)
	}

	if result != "ok" {
		return fmt.Errorf("mirror catalog integrity check: %s", result)
	}

	return nil
}

// Checkpoint flushes the derived WAL before an atomic catalog replacement.
func (c *Catalog) Checkpoint(ctx context.Context) error {
	if _, err := c.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("checkpoint mirror catalog: %w", err)
	}

	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSubmission(row rowScanner) (Submission, error) {
	var value Submission

	err := row.Scan(&value.FilingPath, &value.Accession, &value.CIK,
		&value.FormType, &value.FilingDate, &value.FilingYear,
		&value.LogicalBytes, &value.StoredBytes, &value.SHA256)

	return value, err
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
