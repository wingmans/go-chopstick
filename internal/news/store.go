// Package news maintains the disposable read model used by the news page.
package news

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "github.com/mattn/go-sqlite3"

	"wingman.com/fetch-ecb/internal/pollingworker"
)

// Item is one filing in JetStream discovery order.
type Item struct {
	Sequence        uint64
	AccessionNumber string
	CIK             string
	CompanyName     string
	FormType        string
	FilingDate      string
	FilingURL       string
	FilingPath      string
	SourceFeed      string
	Selected        bool
	SelectionReason string
	SelectionMode   string
	SelectionSet    string
}

// Filter limits the disposable news read model.
type Filter struct {
	Query     string
	CIK       string
	FormType  string
	Selection string
	From      string
	To        string
	Limit     int
}

// Store owns the news SQLite read model.
type Store struct {
	db *sql.DB
}

// Open opens the read model. The boolean reports whether it must be replayed.
func Open(ctx context.Context, path string) (*Store, bool, error) {
	_, statErr := os.Stat(path)
	created := os.IsNotExist(statErr)

	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, false, fmt.Errorf("open news database: %w", err)
	}

	query := `
	CREATE TABLE IF NOT EXISTS feed_items (
		stream_sequence INTEGER PRIMARY KEY,
		event_id TEXT NOT NULL UNIQUE,
		accession_number TEXT NOT NULL UNIQUE,
		cik TEXT NOT NULL,
		company_name TEXT NOT NULL,
		form_type TEXT NOT NULL,
		filing_date TEXT NOT NULL,
		filing_url TEXT NOT NULL,
		filing_path TEXT NOT NULL DEFAULT '',
		source_feed TEXT NOT NULL,
		selected INTEGER NOT NULL DEFAULT 0,
		selection_reason TEXT NOT NULL DEFAULT '',
		selection_mode TEXT NOT NULL DEFAULT '',
		selection_set TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX IF NOT EXISTS feed_items_form
		ON feed_items(form_type);
	CREATE INDEX IF NOT EXISTS feed_items_cik
		ON feed_items(cik);`
	if _, err := db.ExecContext(ctx, query); err != nil {
		_ = db.Close()

		return nil, false, fmt.Errorf("create news schema: %w", err)
	}
	if err := migrateFeedItems(ctx, db); err != nil {
		_ = db.Close()

		return nil, false, err
	}

	return &Store{db: db}, created, nil
}

// Apply stores an event after it has been durably received from JetStream.
func (s *Store) Apply(ctx context.Context, sequence uint64, eventID string,
	event pollingworker.FilingEvent,
) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO feed_items (
			stream_sequence, event_id, accession_number, cik,
			company_name, form_type, filing_date, filing_url, filing_path,
			source_feed, selected, selection_reason, selection_mode,
			selection_set
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(accession_number) DO UPDATE SET
			stream_sequence = excluded.stream_sequence,
			event_id = excluded.event_id,
			selected = excluded.selected,
			selection_reason = excluded.selection_reason,
			selection_mode = excluded.selection_mode,
			selection_set = excluded.selection_set`, sequence, eventID,
		event.AccessionNumber,
		event.CIK, event.CompanyName, event.FormType, event.FilingDate,
		event.FilingURL, event.FilingPath, event.SourceFeed,
		event.Selected != nil && *event.Selected, event.SelectionReason,
		event.SelectionMode, event.SelectionSet)
	if err != nil {
		return fmt.Errorf("store news item: %w", err)
	}

	return nil
}

// List returns newest discoveries first. Dates are filing dates, not observed
// or processing timestamps.
func (s *Store) List(ctx context.Context, filter Filter) ([]Item, error) {
	clauses := []string{"1 = 1"}
	args := make([]any, 0, 8)

	if filter.Query != "" {
		clauses = append(clauses, `(company_name LIKE ? OR
			accession_number LIKE ? OR cik LIKE ?)`)
		query := "%" + filter.Query + "%"
		args = append(args, query, query, query)
	}

	if filter.CIK != "" {
		clauses = append(clauses, "cik = ?")
		args = append(args, filter.CIK)
	}

	if filter.FormType != "" {
		clauses = append(clauses, "form_type = ?")
		args = append(args, filter.FormType)
	}

	switch filter.Selection {
	case "selected":
		clauses = append(clauses, "selected = 1")
	case "skipped":
		clauses = append(clauses, "selected = 0")
	}

	if filter.From != "" {
		clauses = append(clauses, "filing_date >= ?")
		args = append(args, filter.From)
	}

	if filter.To != "" {
		clauses = append(clauses, "filing_date <= ?")
		args = append(args, filter.To)
	}

	if filter.Limit <= 0 || filter.Limit > 500 {
		filter.Limit = 100
	}

	args = append(args, filter.Limit)

	// Clauses contain only the fixed strings selected above; values remain
	// parameters.
	//nolint:gosec // No caller-controlled text is concatenated into the SQL.
	query := `SELECT stream_sequence, accession_number, cik, company_name,
		form_type, filing_date, filing_url, filing_path, source_feed,
		selected, selection_reason, selection_mode, selection_set
		FROM feed_items WHERE ` + strings.Join(clauses, " AND ") +
		" ORDER BY stream_sequence DESC LIMIT ?"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query news items: %w", err)
	}

	defer func() { _ = rows.Close() }()

	items := make([]Item, 0)

	for rows.Next() {
		var item Item
		if err := rows.Scan(&item.Sequence, &item.AccessionNumber, &item.CIK,
			&item.CompanyName, &item.FormType, &item.FilingDate,
			&item.FilingURL, &item.FilingPath, &item.SourceFeed,
			&item.Selected, &item.SelectionReason, &item.SelectionMode,
			&item.SelectionSet); err != nil {
			return nil, fmt.Errorf("scan news item: %w", err)
		}

		items = append(items, item)
	}

	return items, rows.Err()
}

// Get returns one observed filing for an explicit on-demand request.
func (s *Store) Get(ctx context.Context, accession string) (Item, error) {
	var item Item
	err := s.db.QueryRowContext(ctx, `
		SELECT stream_sequence, accession_number, cik, company_name,
			form_type, filing_date, filing_url, filing_path, source_feed,
			selected, selection_reason, selection_mode, selection_set
		FROM feed_items WHERE accession_number = ?`, accession).Scan(
		&item.Sequence, &item.AccessionNumber, &item.CIK, &item.CompanyName,
		&item.FormType, &item.FilingDate, &item.FilingURL, &item.FilingPath,
		&item.SourceFeed, &item.Selected, &item.SelectionReason,
		&item.SelectionMode, &item.SelectionSet)
	if err != nil {
		return Item{}, fmt.Errorf("read news item: %w", err)
	}

	return item, nil
}

func migrateFeedItems(ctx context.Context, db *sql.DB) error {
	columns := []struct {
		name       string
		definition string
	}{
		{name: "filing_path", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "selected", definition: "INTEGER NOT NULL DEFAULT 0"},
		{name: "selection_reason", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "selection_mode", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "selection_set", definition: "TEXT NOT NULL DEFAULT ''"},
	}

	rows, err := db.QueryContext(ctx, `PRAGMA table_info(feed_items)`)
	if err != nil {
		return fmt.Errorf("inspect news schema: %w", err)
	}
	existing := make(map[string]struct{})
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull,
			&defaultValue, &primaryKey); err != nil {
			_ = rows.Close()

			return fmt.Errorf("read news schema: %w", err)
		}
		existing[name] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close news schema rows: %w", err)
	}

	for _, column := range columns {
		if _, exists := existing[column.name]; exists {
			continue
		}
		statement := "ALTER TABLE feed_items ADD COLUMN " + column.name +
			" " + column.definition
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("add news column %s: %w", column.name, err)
		}
	}

	return nil
}

// Close closes the disposable read model.
func (s *Store) Close() error {
	return s.db.Close()
}
