package pollingworker

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// FilingEvent represents the unified schema for both master.zip and RSS feeds.
type FilingEvent struct {
	AccessionNumber string    `json:"accession_number"`
	CIK             string    `json:"cik"`
	FormType        string    `json:"form_type"`
	FilingDate      string    `json:"filing_date"`
	SourceFeed      string    `json:"source_feed"` // "BATCH" or "RSS"
	Timestamp       time.Time `json:"timestamp"`
}

type DedupeService struct {
	db *sql.DB
	mu sync.Mutex
}

// NewDedupeService initializes SQLite with WAL mode for performance.
func NewDedupeService(dbPath string) (*DedupeService, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite db: %w", err)
	}

	// Create table with AccessionNumber as the PRIMARY KEY for O(1) lookups
	query := `
	CREATE TABLE IF NOT EXISTS processed_accessions (
		accession_number TEXT PRIMARY KEY,
		cik TEXT NOT NULL,
		form_type TEXT NOT NULL,
		source_feed TEXT NOT NULL,
		processed_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`

	if _, err := db.ExecContext(context.Background(), query); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("failed to create schema: %w", err)
	}

	return &DedupeService{db: db}, nil
}

// NewSQLiteStore is the name used by the polling command. Keep the storage
// constructor here so the command does not need to know the implementation.
func NewSQLiteStore(dbPath string) (*DedupeService, error) {
	return NewDedupeService(dbPath)
}

// IsDuplicate performs a fast check against the index.
func (s *DedupeService) IsDuplicate(accessionNumber string) (bool, error) {
	return s.IsDuplicateContext(context.Background(), accessionNumber)
}

func (s *DedupeService) IsDuplicateContext(ctx context.Context, accessionNumber string) (bool, error) {
	var exists bool

	query := `SELECT EXISTS(SELECT 1 FROM processed_accessions WHERE accession_number = ?);`

	err := s.db.QueryRowContext(ctx, query, accessionNumber).Scan(&exists)
	if err != nil {
		return false, err
	}

	return exists, nil
}

// SaveAccession records a newly emitted accession number atomically.
func (s *DedupeService) SaveAccession(event FilingEvent) error {
	return s.SaveAccessionContext(context.Background(), event)
}

func (s *DedupeService) SaveAccessionContext(ctx context.Context, event FilingEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `
	INSERT INTO processed_accessions (accession_number, cik, form_type, source_feed)
	VALUES (?, ?, ?, ?)
	ON CONFLICT(accession_number) DO NOTHING;`

	_, err := s.db.ExecContext(ctx, query, event.AccessionNumber, event.CIK, event.FormType, event.SourceFeed)

	return err
}

// SaveBatch performs bulk insertions inside a single transaction (essential for nightly master.zip).
func (s *DedupeService) SaveBatch(events []FilingEvent) ([]FilingEvent, error) {
	return s.SaveBatchContext(context.Background(), events)
}

func (s *DedupeService) SaveBatchContext(ctx context.Context, events []FilingEvent) ([]FilingEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO processed_accessions (accession_number, cik, form_type, source_feed)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(accession_number) DO NOTHING;
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = stmt.Close() }()

	var newEvents []FilingEvent

	for _, event := range events {
		res, err := stmt.ExecContext(ctx, event.AccessionNumber, event.CIK, event.FormType, event.SourceFeed)
		if err != nil {
			return nil, err
		}

		rowsAffected, err := res.RowsAffected()
		if err != nil {
			return nil, err
		}

		// If a row was affected, it means the item was NOT a duplicate and was successfully inserted
		if rowsAffected > 0 {
			newEvents = append(newEvents, event)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return newEvents, nil
}

// FilterNew persists the batch and returns only events inserted this time.
func (s *DedupeService) FilterNew(ctx context.Context, events []FilingEvent) ([]FilingEvent, error) {
	return s.SaveBatchContext(ctx, events)
}

func (s *DedupeService) Close() error {
	return s.db.Close()
}
