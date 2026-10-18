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
	SourceFeed      string
}

// Filter limits the disposable news read model.
type Filter struct {
	CIK      string
	FormType string
	From     string
	To       string
	Limit    int
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
		source_feed TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS feed_items_form
		ON feed_items(form_type);
	CREATE INDEX IF NOT EXISTS feed_items_cik
		ON feed_items(cik);`
	if _, err := db.ExecContext(ctx, query); err != nil {
		_ = db.Close()

		return nil, false, fmt.Errorf("create news schema: %w", err)
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
			company_name, form_type, filing_date, filing_url, source_feed
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING`, sequence, eventID, event.AccessionNumber,
		event.CIK, event.CompanyName, event.FormType, event.FilingDate,
		event.FilingURL, event.SourceFeed)
	if err != nil {
		return fmt.Errorf("store news item: %w", err)
	}

	return nil
}

// List returns newest discoveries first. Dates are filing dates, not observed
// or processing timestamps.
func (s *Store) List(ctx context.Context, filter Filter) ([]Item, error) {
	clauses := []string{"1 = 1"}
	args := make([]any, 0, 5)

	if filter.CIK != "" {
		clauses = append(clauses, "cik = ?")
		args = append(args, filter.CIK)
	}

	if filter.FormType != "" {
		clauses = append(clauses, "form_type = ?")
		args = append(args, filter.FormType)
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
		form_type, filing_date, filing_url, source_feed
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
			&item.FilingURL, &item.SourceFeed); err != nil {
			return nil, fmt.Errorf("scan news item: %w", err)
		}

		items = append(items, item)
	}

	return items, rows.Err()
}

// Close closes the disposable read model.
func (s *Store) Close() error {
	return s.db.Close()
}
