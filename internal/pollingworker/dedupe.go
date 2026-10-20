package pollingworker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"wingman.com/fetch-ecb/internal/eventbus"
)

// FilingEvent represents the unified schema for both master.zip and RSS feeds.
type FilingEvent struct {
	SchemaVersion   int       `json:"schema_version"`
	EventID         string    `json:"event_id"`
	AccessionNumber string    `json:"accession_number"`
	CIK             string    `json:"cik"`
	CompanyName     string    `json:"company_name"`
	FormType        string    `json:"form_type"`
	FilingDate      string    `json:"filing_date"`
	FilingPath      string    `json:"filing_path"`
	IndexPath       string    `json:"index_path"`
	FilingURL       string    `json:"filing_url"`
	SourceFeed      string    `json:"source_feed"` // "BATCH" or "ATOM"
	Timestamp       time.Time `json:"observed_at"`
}

const filingEventSchemaVersion = 1

var databaseInitializationMu sync.Mutex

func normalizeEvent(event FilingEvent) FilingEvent {
	if event.SchemaVersion == 0 {
		event.SchemaVersion = filingEventSchemaVersion
	}

	if event.EventID == "" {
		event.EventID = "filing.discovered:" + event.AccessionNumber
	}

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	return event
}

type DeadLetter struct {
	FeedURL  string
	Title    string
	Link     string
	RawEntry string
	Reason   string
}

type DedupeService struct {
	db *sql.DB
	mu sync.Mutex
}

// NewDedupeService initializes SQLite with WAL mode for performance.
func NewDedupeService(dbPath string) (*DedupeService, error) {
	return NewDedupeServiceContext(context.Background(), dbPath)
}

func NewDedupeServiceContext(ctx context.Context, dbPath string) (*DedupeService, error) {
	databaseInitializationMu.Lock()
	defer databaseInitializationMu.Unlock()

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
	deadLetterQuery := `
	CREATE TABLE IF NOT EXISTS polling_dead_letters (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		received_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		feed_url TEXT NOT NULL,
		title TEXT NOT NULL,
		link TEXT NOT NULL,
		raw_entry TEXT NOT NULL,
		reason TEXT NOT NULL
	);`
	outboxQuery := `
	CREATE TABLE IF NOT EXISTS polling_outbox (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		event_id TEXT NOT NULL UNIQUE,
		subject TEXT NOT NULL,
		payload BLOB NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		published_at DATETIME,
		attempts INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL DEFAULT ''
	);`
	failuresQuery := `
	CREATE TABLE IF NOT EXISTS work_failures (
		stage TEXT NOT NULL,
		accession_number TEXT NOT NULL,
		subject TEXT NOT NULL,
		payload BLOB NOT NULL,
		attempts INTEGER NOT NULL DEFAULT 0,
		first_failed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_failed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		last_error TEXT NOT NULL,
		resolved_at DATETIME,
		PRIMARY KEY (stage, accession_number)
	);`

	if _, err := db.ExecContext(ctx, query); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("failed to create schema: %w", err)
	}

	if _, err := db.ExecContext(ctx, deadLetterQuery); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("failed to create dead-letter schema: %w", err)
	}

	if _, err := db.ExecContext(ctx, outboxQuery); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("failed to create outbox schema: %w", err)
	}

	if _, err := db.ExecContext(ctx, failuresQuery); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("failed to create work-failure schema: %w", err)
	}

	return &DedupeService{db: db}, nil
}

// NewSQLiteStore is the name used by the polling command.
func NewSQLiteStore(dbPath string) (*DedupeService, error) {
	return NewDedupeService(dbPath)
}

// SaveDeadLetter persists a dead letter entry into the database.
func (s *DedupeService) SaveDeadLetter(ctx context.Context, letter DeadLetter) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO polling_dead_letters (feed_url, title, link, raw_entry, reason)
		VALUES (?, ?, ?, ?, ?);`, letter.FeedURL, letter.Title, letter.Link,
		letter.RawEntry, letter.Reason)

	return err
}

// IsDuplicate performs a fast check against the index.
func (s *DedupeService) IsDuplicate(accessionNumber string) (bool, error) {
	return s.IsDuplicateContext(context.Background(), accessionNumber)
}

// IsDuplicate checks if the given accession number has already been processed.
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
// It ensures that the accession number is only saved if it hasn't been processed before.
func (s *DedupeService) SaveAccession(event FilingEvent) error {
	return s.SaveAccessionContext(context.Background(), event)
}

func (s *DedupeService) SaveAccessionContext(ctx context.Context, event FilingEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.saveBatchContext(ctx, []FilingEvent{event})

	return err
}

// SaveBatch performs bulk insertions inside a single transaction (essential for nightly master.zip).
func (s *DedupeService) SaveBatch(events []FilingEvent) ([]FilingEvent, error) {
	return s.SaveBatchContext(context.Background(), events)
}

// SaveBatchContext performs bulk insertions inside a single transaction (essential for nightly master.zip).
func (s *DedupeService) SaveBatchContext(ctx context.Context, events []FilingEvent) ([]FilingEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.saveBatchContext(ctx, events)
}

//nolint:funcorder // Kept beside the exported batch methods it implements.
func (s *DedupeService) saveBatchContext(ctx context.Context,
	events []FilingEvent,
) ([]FilingEvent, error) {
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

	outbox, err := tx.PrepareContext(ctx, `
		INSERT INTO polling_outbox (event_id, subject, payload)
		VALUES (?, ?, ?)
		ON CONFLICT(event_id) DO NOTHING;
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = outbox.Close() }()

	var newEvents []FilingEvent

	for _, event := range events {
		event = normalizeEvent(event)

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
			payload, err := json.Marshal(event)
			if err != nil {
				return nil, fmt.Errorf("encode discovery event: %w", err)
			}

			subject := eventbus.SubjectFor(eventbus.DiscoverySubject,
				event.FormType)
			if _, err := outbox.ExecContext(ctx, event.EventID, subject,
				payload); err != nil {
				return nil, fmt.Errorf("enqueue discovery event: %w", err)
			}

			newEvents = append(newEvents, event)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return newEvents, nil
}

type outboxMessage struct {
	ID      int64
	EventID string
	Subject string
	Payload []byte
}

// PublishPending publishes every pending row and marks it only after PubAck.
func (s *DedupeService) PublishPending(ctx context.Context,
	publisher eventbus.Publisher,
) error {
	if publisher == nil {
		return nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, event_id, subject, payload
		FROM polling_outbox
		WHERE published_at IS NULL
		ORDER BY id;
	`)
	if err != nil {
		return fmt.Errorf("read polling outbox: %w", err)
	}

	messages := make([]outboxMessage, 0)

	for rows.Next() {
		var message outboxMessage
		if err := rows.Scan(&message.ID, &message.EventID, &message.Subject,
			&message.Payload); err != nil {
			_ = rows.Close() //nolint:sqlclosecheck // Error path before normal close.

			return fmt.Errorf("scan polling outbox: %w", err)
		}

		messages = append(messages, message)
	}

	if err := rows.Close(); err != nil {
		return fmt.Errorf("close polling outbox: %w", err)
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate polling outbox: %w", err)
	}

	for _, message := range messages {
		if err := publisher.Publish(ctx, message.Subject, message.EventID,
			message.Payload); err != nil {
			_, _ = s.db.ExecContext(ctx, `
				UPDATE polling_outbox
				SET attempts = attempts + 1, last_error = ?
				WHERE id = ?`, err.Error(), message.ID)

			return err
		}

		if _, err := s.db.ExecContext(ctx, `
			UPDATE polling_outbox
			SET published_at = CURRENT_TIMESTAMP,
				attempts = attempts + 1,
				last_error = ''
			WHERE id = ?`, message.ID); err != nil {
			return fmt.Errorf("mark polling outbox published: %w", err)
		}
	}

	return nil
}

// PendingOutbox returns the number of discovery events awaiting publication.
func (s *DedupeService) PendingOutbox(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM polling_outbox WHERE published_at IS NULL
	`).Scan(&count); err != nil {
		return 0, err
	}

	return count, nil
}

// FilterNew persists the batch and returns only events inserted this time.
func (s *DedupeService) FilterNew(ctx context.Context, events []FilingEvent) ([]FilingEvent, error) {
	return s.SaveBatchContext(ctx, events)
}

// Close terminates the database connection associated with the deduplication service.
func (s *DedupeService) Close() error {
	return s.db.Close()
}
