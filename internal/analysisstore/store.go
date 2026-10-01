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

	_ "github.com/mattn/go-sqlite3"

	dividendview "wingman.com/fetch-ecb/internal/dividend"
	filingview "wingman.com/fetch-ecb/internal/filing"
)

const schemaVersion = 3

// Store is the SQLite-backed read model for normalized filing data.
type Store struct {
	db *sql.DB
}

// Open opens or creates the analysis database and applies its schema.
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("analysis database path is required")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create analysis database directory: %w", err)
	}

	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open analysis database: %w", err)
	}

	store := &Store{db: db}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()

		return nil, err
	}

	return store, nil
}

//nolint:funcorder // Migration is defined near database construction.
func (s *Store) migrate(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS filing_views (
			cik TEXT NOT NULL,
			accession TEXT NOT NULL,
			form_type TEXT NOT NULL,
			filing_date TEXT NOT NULL,
			report_date TEXT NOT NULL,
			company TEXT NOT NULL,
			status TEXT NOT NULL,
			facts INTEGER NOT NULL,
			schema_version INTEGER NOT NULL,
			payload BLOB NOT NULL,
			updated_at DATETIME NOT NULL,
			PRIMARY KEY (cik, accession)
		)`,
		`CREATE INDEX IF NOT EXISTS filing_views_filing_date
			ON filing_views (filing_date DESC, accession DESC)`,
		`CREATE INDEX IF NOT EXISTS filing_views_cik_date
			ON filing_views (cik, filing_date, accession)`,
		`CREATE TABLE IF NOT EXISTS dividend_views (
			cik TEXT PRIMARY KEY,
			company TEXT NOT NULL,
			schema_version INTEGER NOT NULL,
			payload BLOB NOT NULL,
			updated_at DATETIME NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS dividend_events (
			cik TEXT NOT NULL,
			event_id TEXT NOT NULL,
			declaration_date TEXT,
			ex_dividend_date TEXT,
			record_date TEXT,
			payable_date TEXT,
			amount_per_share TEXT,
			currency TEXT,
			type TEXT NOT NULL,
			status TEXT NOT NULL,
			confidence TEXT NOT NULL,
			payload BLOB NOT NULL,
			PRIMARY KEY (cik, event_id)
		)`,
		`CREATE INDEX IF NOT EXISTS dividend_events_dates
			ON dividend_events (cik, payable_date, ex_dividend_date)`,
		`CREATE TABLE IF NOT EXISTS dividend_observations (
			observation_id TEXT PRIMARY KEY,
			cik TEXT NOT NULL,
			kind TEXT NOT NULL,
			period TEXT NOT NULL,
			context_id TEXT,
			context_start TEXT,
			context_end TEXT,
			accession TEXT NOT NULL,
			value TEXT NOT NULL,
			unit TEXT,
			form_type TEXT NOT NULL,
			filing_date TEXT NOT NULL,
			payload BLOB NOT NULL
		)`,
	}

	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply analysis database schema: %w", err)
		}
	}

	if err := s.migrateDividendObservations(ctx); err != nil {
		return err
	}

	if _, err := s.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS
		dividend_observations_period
		ON dividend_observations (cik, period, filing_date)`); err != nil {
		return fmt.Errorf("apply analysis database indexes: %w", err)
	}

	if _, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO schema_migrations (version, applied_at)
		 VALUES (?, ?)`, schemaVersion, time.Now().UTC()); err != nil {
		return fmt.Errorf("record analysis database schema: %w", err)
	}

	return nil
}

func (s *Store) migrateDividendObservations(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(dividend_observations)`)
	if err != nil {
		return fmt.Errorf("inspect dividend observations schema: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var hasObservationID bool
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType, defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue,
			&primaryKey); err != nil {
			return fmt.Errorf("read dividend observations schema: %w", err)
		}
		if name.Valid && name.String == "observation_id" {
			hasObservationID = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate dividend observations schema: %w", err)
	}
	if hasObservationID {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin dividend observation migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DROP INDEX IF EXISTS
		dividend_observations_period`); err != nil {
		return fmt.Errorf("drop dividend observation index: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE dividend_observations
		RENAME TO dividend_observations_v1`); err != nil {
		return fmt.Errorf("rename dividend observations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE dividend_observations (
		observation_id TEXT PRIMARY KEY,
		cik TEXT NOT NULL,
		kind TEXT NOT NULL,
		period TEXT NOT NULL,
		context_id TEXT,
		context_start TEXT,
		context_end TEXT,
		accession TEXT NOT NULL,
		value TEXT NOT NULL,
		unit TEXT,
		form_type TEXT NOT NULL,
		filing_date TEXT NOT NULL,
		payload BLOB NOT NULL
	)`); err != nil {
		return fmt.Errorf("create dividend observations v2: %w", err)
	}

	legacyRows, err := tx.QueryContext(ctx, `SELECT cik, kind, period,
		accession, value, unit, form_type, filing_date, payload
		FROM dividend_observations_v1`)
	if err != nil {
		return fmt.Errorf("read legacy dividend observations: %w", err)
	}
	for legacyRows.Next() {
		var cik string
		var observation dividendview.Observation
		var payload []byte
		if err := legacyRows.Scan(&cik, &observation.Kind,
			&observation.Period, &observation.Accession, &observation.Value,
			&observation.Unit, &observation.FormType, &observation.FilingDate,
			&payload); err != nil {
			_ = legacyRows.Close()
			return fmt.Errorf("scan legacy dividend observation: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO dividend_observations
			(observation_id, cik, kind, period, accession, value, unit,
			 form_type, filing_date, payload)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, observationID(observation),
			cik, observation.Kind, observation.Period,
			observation.Accession, observation.Value, nullString(observation.Unit),
			observation.FormType, observation.FilingDate, payload); err != nil {
			_ = legacyRows.Close()
			return fmt.Errorf("copy legacy dividend observation: %w", err)
		}
	}
	if err := legacyRows.Err(); err != nil {
		_ = legacyRows.Close()
		return fmt.Errorf("iterate legacy dividend observations: %w", err)
	}
	_ = legacyRows.Close()

	if _, err := tx.ExecContext(ctx, `DROP TABLE dividend_observations_v1`); err != nil {
		return fmt.Errorf("drop legacy dividend observations: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit dividend observation migration: %w", err)
	}

	return nil
}

// Close releases the database connection.
func (s *Store) Close() error { return s.db.Close() }

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

	_, err = tx.ExecContext(ctx, `
		INSERT INTO filing_views
			(cik, accession, form_type, filing_date, report_date, company,
			 status, facts, schema_version, payload, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(cik, accession) DO UPDATE SET
			form_type=excluded.form_type, filing_date=excluded.filing_date,
			report_date=excluded.report_date, company=excluded.company,
			status=excluded.status, facts=excluded.facts,
			schema_version=excluded.schema_version, payload=excluded.payload,
			updated_at=excluded.updated_at`,
		metadata.CIK, metadata.Accession, metadata.FormType, metadata.FilingDate,
		metadata.ReportDate, metadata.Company, view.Counts.Status,
		view.Counts.Facts, view.SchemaVersion, payload, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("save filing view: %w", err)
	}

	return tx.Commit()
}

// HasFilingView reports whether one filing has been fully materialized.
func (s *Store) HasFilingView(ctx context.Context, cik, accession string) (bool, error) {
	cik = canonicalCIK(cik)

	var exists int
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM filing_views WHERE cik = ? AND accession = ?
		)`, cik, accession).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check filing view: %w", err)
	}

	return exists != 0, nil
}

// HasSuccessfulFilingView reports whether a filing completed without parser
// extraction errors.
func (s *Store) HasSuccessfulFilingView(ctx context.Context, cik, accession string) (bool, error) {
	cik = canonicalCIK(cik)

	var status string
	err := s.db.QueryRowContext(ctx, `
		SELECT status FROM filing_views WHERE cik = ? AND accession = ?`,
		cik, accession).Scan(&status)
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

	_, err = tx.ExecContext(ctx, `
		INSERT INTO dividend_views (cik, company, schema_version, payload, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(cik) DO UPDATE SET company=excluded.company,
			schema_version=excluded.schema_version, payload=excluded.payload,
			updated_at=excluded.updated_at`,
		view.CIK, view.Company, view.SchemaVersion, payload, now)
	if err != nil {
		return fmt.Errorf("save dividend view: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM dividend_events WHERE cik = ?`, view.CIK); err != nil {
		return fmt.Errorf("replace dividend events: %w", err)
	}

	for _, event := range view.Events {
		eventPayload, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("encode dividend event: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO dividend_events
				(cik, event_id, declaration_date, ex_dividend_date, record_date,
				 payable_date, amount_per_share, currency, type, status, confidence,
				 payload)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, view.CIK, event.ID,
			nullString(event.DeclarationDate), nullString(event.ExDate),
			nullString(event.RecordDate), nullString(event.PayableDate),
			nullString(event.AmountPerShare), nullString(event.Currency),
			event.Type, event.Status, event.Confidence, eventPayload); err != nil {
			return fmt.Errorf("save dividend event: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM dividend_observations WHERE cik = ?`, view.CIK); err != nil {
		return fmt.Errorf("replace dividend observations: %w", err)
	}

	for _, observation := range view.Observations {
		observationPayload, err := json.Marshal(observation)
		if err != nil {
			return fmt.Errorf("encode dividend observation: %w", err)
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO dividend_observations
				(observation_id, cik, kind, period, context_id, context_start,
				 context_end, accession, value, unit, form_type, filing_date,
				 payload)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			observationID(observation), view.CIK, observation.Kind,
			observation.Period, nullString(observation.ContextID),
			nullString(observation.ContextStart), nullString(observation.ContextEnd),
			observation.Accession, observation.Value,
			nullString(observation.Unit), observation.FormType,
			observation.FilingDate, observationPayload); err != nil {
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

func nullString(value string) any {
	if value == "" {
		return nil
	}

	return value
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

	var payload []byte

	err := s.db.QueryRowContext(ctx,
		`SELECT payload FROM filing_views WHERE cik = ? AND accession = ?`, cik, accession).
		Scan(&payload)
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
	rows, err := s.db.QueryContext(ctx, `SELECT payload FROM filing_views
		ORDER BY filing_date DESC, accession DESC`)
	if err != nil {
		return nil, fmt.Errorf("list filing views: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var views []filingview.View

	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("read filing view: %w", err)
		}

		var view filingview.View
		if err := json.Unmarshal(payload, &view); err != nil {
			return nil, fmt.Errorf("decode filing view: %w", err)
		}

		views = append(views, view)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate filing views: %w", err)
	}

	return views, nil
}

// LoadDividendView loads one company's aggregate dividend view.
func (s *Store) LoadDividendView(ctx context.Context, cik string) (dividendview.View, error) {
	cik = canonicalCIK(cik)

	var payload []byte

	err := s.db.QueryRowContext(ctx,
		`SELECT payload FROM dividend_views WHERE cik = ?`, cik).Scan(&payload)
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
