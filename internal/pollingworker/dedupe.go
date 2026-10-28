package pollingworker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"wingman.com/chopstick/internal/eventbus"
	"wingman.com/chopstick/internal/pollingworker/dbgen"
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
	Selected        *bool     `json:"selected,omitempty"`
	SelectionReason string    `json:"selection_reason,omitempty"`
	SelectionMode   string    `json:"selection_mode,omitempty"`
	SelectionSet    string    `json:"selection_set,omitempty"`
}

const filingEventSchemaVersion = 1

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
	db      *sql.DB
	queries *dbgen.Queries
	mu      sync.Mutex
}

// NewDedupeService initializes SQLite with WAL mode for performance.
func NewDedupeService(dbPath string) (*DedupeService, error) {
	return NewDedupeServiceContext(context.Background(), dbPath)
}

func NewDedupeServiceContext(ctx context.Context, dbPath string) (*DedupeService, error) {
	db, err := sql.Open(
		"sqlite3",
		dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite db: %w", err)
	}

	if err := createSchema(ctx, db); err != nil {
		_ = db.Close()

		return nil, err
	}

	queries, err := dbgen.Prepare(ctx, db)
	if err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("prepare polling queries: %w", err)
	}

	return &DedupeService{db: db, queries: queries}, nil
}

// NewSQLiteStore is the name used by the polling command.
func NewSQLiteStore(dbPath string) (*DedupeService, error) {
	return NewDedupeService(dbPath)
}

// SaveDeadLetter persists a dead letter entry into the database.
func (s *DedupeService) SaveDeadLetter(ctx context.Context, letter DeadLetter) error {
	return s.queries.SaveDeadLetter(ctx, dbgen.SaveDeadLetterParams{
		FeedURL:  letter.FeedURL,
		Title:    letter.Title,
		Link:     letter.Link,
		RawEntry: letter.RawEntry,
		Reason:   letter.Reason,
	})
}

// IsDuplicate performs a fast check against the index.
func (s *DedupeService) IsDuplicate(accessionNumber string) (bool, error) {
	return s.IsDuplicateContext(context.Background(), accessionNumber)
}

// IsDuplicate checks if the given accession number has already been processed.
func (s *DedupeService) IsDuplicateContext(ctx context.Context, accessionNumber string) (bool, error) {
	return s.queries.IsDuplicate(ctx, accessionNumber)
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

	queries := s.queries.WithTx(tx)

	var newEvents []FilingEvent

	for _, event := range events {
		event = normalizeEvent(event)

		result, err := queries.SaveProcessedAccession(
			ctx,
			dbgen.SaveProcessedAccessionParams{
				AccessionNumber: event.AccessionNumber,
				CIK:             event.CIK,
				FormType:        event.FormType,
				SourceFeed:      event.SourceFeed,
			},
		)
		if err != nil {
			return nil, err
		}

		rowsAffected, err := result.RowsAffected()
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
			if err := queries.SaveOutboxMessage(
				ctx,
				dbgen.SaveOutboxMessageParams{
					EventID: event.EventID,
					Subject: subject,
					Payload: payload,
				},
			); err != nil {
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

// PublishPending publishes every pending row and marks it only after PubAck.
func (s *DedupeService) PublishPending(ctx context.Context,
	publisher eventbus.Publisher,
) error {
	if publisher == nil {
		return nil
	}

	messages, err := s.queries.PendingOutboxMessages(ctx)
	if err != nil {
		return fmt.Errorf("read polling outbox: %w", err)
	}

	for _, message := range messages {
		if err := publisher.Publish(ctx, message.Subject, message.EventID,
			message.Payload); err != nil {
			_ = s.queries.RecordOutboxPublishFailure(
				ctx,
				dbgen.RecordOutboxPublishFailureParams{
					LastError: err.Error(),
					ID:        message.ID,
				},
			)

			return err
		}

		if err := s.queries.MarkOutboxPublished(ctx, message.ID); err != nil {
			return fmt.Errorf("mark polling outbox published: %w", err)
		}
	}

	return nil
}

// PendingOutbox returns the number of discovery events awaiting publication.
func (s *DedupeService) PendingOutbox(ctx context.Context) (int, error) {
	count, err := s.queries.PendingOutboxCount(ctx)
	if err != nil {
		return 0, err
	}

	return int(count), nil
}

// FilterNew persists the batch and returns only events inserted this time.
func (s *DedupeService) FilterNew(ctx context.Context, events []FilingEvent) ([]FilingEvent, error) {
	return s.SaveBatchContext(ctx, events)
}

// Close terminates the database connection associated with the deduplication service.
func (s *DedupeService) Close() error {
	return errors.Join(s.queries.Close(), s.db.Close())
}
