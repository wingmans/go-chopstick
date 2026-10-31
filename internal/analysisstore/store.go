// Package analysisstore persists the normalized filing read models.
package analysisstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wingman.com/chopstick/internal/analysisstore/dbgen"
	dividendview "wingman.com/chopstick/internal/dividend"
	filingview "wingman.com/chopstick/internal/filing"
)

// Store is the SQLite-backed read model for normalized filing data.
type Store struct {
	db      *sql.DB
	queries *dbgen.Queries
}

// Open opens or creates the analysis database and applies its schema.
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("analysis database path is required")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create analysis database directory: %w", err)
	}

	db, err := sql.Open(
		"sqlite3",
		path+"?_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate",
	)
	if err != nil {
		return nil, fmt.Errorf("open analysis database: %w", err)
	}

	db.SetMaxOpenConns(1)

	if err := createSchema(ctx, db); err != nil {
		_ = db.Close()

		return nil, err
	}

	queries, err := dbgen.Prepare(ctx, db)
	if err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("prepare analysis queries: %w", err)
	}

	return &Store{db: db, queries: queries}, nil
}

// Close releases the database connection.
func (s *Store) Close() error {
	return errors.Join(s.queries.Close(), s.db.Close())
}

// SaveFilingView atomically replaces one filing view.
func (s *Store) SaveFilingView(ctx context.Context, view filingview.View) error {
	view.Metadata.CIK = canonicalCIK(view.Metadata.CIK)

	payload, err := json.Marshal(view)
	if err != nil {
		return fmt.Errorf("encode filing view: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin filing view transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	metadata := view.Metadata
	queries := s.queries.WithTx(tx)

	if err := queries.UpsertFilingView(ctx, dbgen.UpsertFilingViewParams{
		CIK:           metadata.CIK,
		Accession:     metadata.Accession,
		FormType:      metadata.FormType,
		FilingDate:    metadata.FilingDate,
		ReportDate:    metadata.ReportDate,
		Company:       metadata.Company,
		Status:        view.Counts.Status,
		Facts:         int64(view.Counts.Facts),
		SchemaVersion: int64(view.SchemaVersion),
		Payload:       payload,
		UpdatedAt:     time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("save filing view: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit filing view: %w", err)
	}

	return nil
}

// HasFilingView reports whether one filing has been fully materialized.
func (s *Store) HasFilingView(ctx context.Context, cik, accession string) (bool, error) {
	cik = canonicalCIK(cik)

	exists, err := s.queries.HasFilingView(ctx, dbgen.HasFilingViewParams{
		CIK:       cik,
		Accession: accession,
	})
	if err != nil {
		return false, fmt.Errorf("check filing view: %w", err)
	}

	return exists, nil
}

// HasSuccessfulFilingView reports whether a filing completed without parser
// extraction errors.
func (s *Store) HasSuccessfulFilingView(ctx context.Context, cik, accession string) (bool, error) {
	cik = canonicalCIK(cik)

	status, err := s.queries.FilingViewStatus(
		ctx,
		dbgen.FilingViewStatusParams{CIK: cik, Accession: accession},
	)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("check filing view status: %w", err)
	}

	return status == "complete" || status == "no_xbrl" || status == "unsupported", nil
}

// SaveDividendView atomically replaces a company's aggregate dividend view
// and its queryable event and observation projections.
func (s *Store) SaveDividendView(ctx context.Context, view dividendview.View) error {
	view.CIK = canonicalCIK(view.CIK)

	payload, err := json.Marshal(view)
	if err != nil {
		return fmt.Errorf("encode dividend view: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin dividend view transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()
	queries := s.queries.WithTx(tx)

	if err := queries.UpsertDividendView(
		ctx,
		dbgen.UpsertDividendViewParams{
			CIK:           view.CIK,
			Company:       view.Company,
			SchemaVersion: int64(view.SchemaVersion),
			Payload:       payload,
			UpdatedAt:     now,
		},
	); err != nil {
		return fmt.Errorf("save dividend view: %w", err)
	}

	if err := queries.DeleteDividendEvents(ctx, view.CIK); err != nil {
		return fmt.Errorf("replace dividend events: %w", err)
	}

	for _, event := range view.Events {
		eventPayload, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("encode dividend event: %w", err)
		}

		if err := queries.InsertDividendEvent(
			ctx,
			dbgen.InsertDividendEventParams{
				CIK:             view.CIK,
				EventID:         event.ID,
				DeclarationDate: nullString(event.DeclarationDate),
				ExDividendDate:  nullString(event.ExDate),
				RecordDate:      nullString(event.RecordDate),
				PayableDate:     nullString(event.PayableDate),
				AmountPerShare:  nullString(event.AmountPerShare),
				Currency:        nullString(event.Currency),
				Type:            event.Type,
				Status:          event.Status,
				Confidence:      event.Confidence,
				Payload:         eventPayload,
			},
		); err != nil {
			return fmt.Errorf("save dividend event: %w", err)
		}
	}

	if err := queries.DeleteDividendObservations(ctx, view.CIK); err != nil {
		return fmt.Errorf("replace dividend observations: %w", err)
	}

	for _, observation := range view.Observations {
		observationPayload, err := json.Marshal(observation)
		if err != nil {
			return fmt.Errorf("encode dividend observation: %w", err)
		}

		if err := queries.InsertDividendObservation(
			ctx,
			dbgen.InsertDividendObservationParams{
				ObservationID: observationID(observation),
				CIK:           view.CIK,
				Kind:          observation.Kind,
				Period:        observation.Period,
				ContextID:     nullString(observation.ContextID),
				ContextStart:  nullString(observation.ContextStart),
				ContextEnd:    nullString(observation.ContextEnd),
				Accession:     observation.Accession,
				Value:         observation.Value,
				Unit:          nullString(observation.Unit),
				FormType:      observation.FormType,
				FilingDate:    observation.FilingDate,
				Payload:       observationPayload,
			},
		); err != nil {
			return fmt.Errorf("save dividend observation: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit dividend view: %w", err)
	}

	return nil
}

func observationID(observation dividendview.Observation) string {
	value := strings.Join([]string{
		observation.Kind, observation.Period, observation.ContextID,
		observation.ContextStart, observation.ContextEnd, observation.Accession,
		observation.Value, observation.Unit,
	}, "\x00")
	sum := sha256.Sum256([]byte(value))

	return hex.EncodeToString(sum[:])
}

func nullString(value string) sql.NullString {
	if value == "" {
		return sql.NullString{}
	}

	return sql.NullString{String: value, Valid: true}
}

func canonicalCIK(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 10 {
		return value
	}

	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return value
		}
	}

	value = strings.TrimLeft(value, "0")
	if value == "" {
		value = "0"
	}

	return strings.Repeat("0", 10-len(value)) + value
}

// LoadFilingView loads one normalized filing view.
func (s *Store) LoadFilingView(ctx context.Context, cik, accession string) (filingview.View, error) {
	cik = canonicalCIK(cik)

	payload, err := s.queries.FilingViewPayload(
		ctx,
		dbgen.FilingViewPayloadParams{CIK: cik, Accession: accession},
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return filingview.View{}, os.ErrNotExist
		}

		return filingview.View{}, fmt.Errorf("load filing view: %w", err)
	}

	var view filingview.View
	if err := json.Unmarshal(payload, &view); err != nil {
		return filingview.View{}, fmt.Errorf("decode filing view: %w", err)
	}

	return view, nil
}

// ListFilingViews returns normalized views ordered newest first.
func (s *Store) ListFilingViews(ctx context.Context) ([]filingview.View, error) {
	payloads, err := s.queries.ListFilingViewPayloads(ctx)
	if err != nil {
		return nil, fmt.Errorf("list filing views: %w", err)
	}

	views := make([]filingview.View, 0, len(payloads))

	for _, payload := range payloads {
		var view filingview.View
		if err := json.Unmarshal(payload, &view); err != nil {
			return nil, fmt.Errorf("decode filing view: %w", err)
		}

		views = append(views, view)
	}

	return views, nil
}

// LoadDividendView loads one company's aggregate dividend view.
func (s *Store) LoadDividendView(ctx context.Context, cik string) (dividendview.View, error) {
	cik = canonicalCIK(cik)

	payload, err := s.queries.DividendViewPayload(ctx, cik)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return dividendview.View{}, os.ErrNotExist
		}

		return dividendview.View{}, fmt.Errorf("load dividend view: %w", err)
	}

	var view dividendview.View
	if err := json.Unmarshal(payload, &view); err != nil {
		return dividendview.View{}, fmt.Errorf("decode dividend view: %w", err)
	}

	return view, nil
}
