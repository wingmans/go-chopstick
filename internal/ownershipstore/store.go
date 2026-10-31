// Package ownershipstore persists normalized SEC ownership filings.
package ownershipstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"wingman.com/chopstick/internal/ownership"
)

// Store is the SQLite ownership database.
type Store struct {
	db *sql.DB
}

// Outcome records a filing that did not produce normalized rows.
type Outcome struct {
	Accession     string
	FormType      string
	Family        string
	FilingDate    string
	ReportDate    string
	SourcePath    string
	SourceSHA256  string
	ParserVersion string
	Status        string
	Diagnostic    string
}

// FilingStatus is the durable processing state for one accession.
type FilingStatus struct {
	Accession     string
	FormType      string
	Family        string
	FilingDate    string
	ParserVersion string
	Status        string
	Diagnostic    string
}

// Coverage records the reconciliation result for a family and interval.
type Coverage struct {
	Family      string
	FromDate    string
	ThroughDate string
	Version     string
	Expected    int64
	Processed   int64
	Failed      int64
	Unsupported int64
	Status      string
	StartedAt   time.Time
	CompletedAt time.Time
}

// Count summarizes durable filing outcomes.
type Count struct {
	Family string
	Status string
	Count  int64
}

// Open opens or creates the ownership database.
func Open(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("ownership database path is required")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create ownership database directory: %w", err)
	}

	db, err := sql.Open("sqlite3", path+
		"?_journal_mode=WAL&_busy_timeout=5000&_txlock=immediate"+
		"&_foreign_keys=on")
	if err != nil {
		return nil, fmt.Errorf("open ownership database: %w", err)
	}

	db.SetMaxOpenConns(1)

	if err := createSchema(ctx, db); err != nil {
		_ = db.Close()

		return nil, err
	}

	if _, err := db.ExecContext(ctx, `CREATE TEMP TABLE ownership_run_seen (
		accession TEXT PRIMARY KEY
	)`); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("create ownership run deduplication table: %w", err)
	}

	return &Store{db: db}, nil
}

// Close releases the database connection.
func (store *Store) Close() error {
	return store.db.Close()
}

// FirstSeen reports whether accession has not previously been observed by
// this Store instance. The temporary SQLite table keeps batch deduplication
// bounded without retaining every accession in Go memory.
func (store *Store) FirstSeen(ctx context.Context,
	accession string,
) (bool, error) {
	result, err := store.db.ExecContext(ctx, `INSERT OR IGNORE INTO
		ownership_run_seen (accession) VALUES (?)`, accession)
	if err != nil {
		return false, fmt.Errorf("record ownership run accession: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read ownership run accession result: %w", err)
	}

	return rows == 1, nil
}

// NeedsImport reports whether an accession lacks a terminal outcome at the
// requested parser version.
func (store *Store) NeedsImport(ctx context.Context, accession,
	parserVersion string,
) (bool, error) {
	var status, version, sourcePath, sourceHash string

	err := store.db.QueryRowContext(ctx, `SELECT
		status, parser_version, source_path, source_sha256
		FROM ownership_filings WHERE accession = ?`, accession).Scan(
		&status, &version, &sourcePath, &sourceHash)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}

	if err != nil {
		return false, fmt.Errorf("read ownership filing state: %w", err)
	}

	return version != parserVersion || status == "failed" ||
		(status == "processed" && (sourcePath == "" || sourceHash == "")), nil
}

// Status returns the durable outcome for one accession.
func (store *Store) Status(ctx context.Context,
	accession string,
) (FilingStatus, error) {
	var result FilingStatus

	err := store.db.QueryRowContext(ctx, `SELECT
		accession, form_type, form_family, filing_date,
		parser_version, status, diagnostic
		FROM ownership_filings WHERE accession = ?`, accession).Scan(
		&result.Accession, &result.FormType, &result.Family,
		&result.FilingDate, &result.ParserVersion, &result.Status,
		&result.Diagnostic)
	if err != nil {
		return FilingStatus{}, fmt.Errorf("read ownership filing status: %w", err)
	}

	return result, nil
}

// Save atomically replaces normalized rows for one accession.
func (store *Store) Save(ctx context.Context, filing ownership.Filing) error {
	if filing.Accession == "" || filing.FormType == "" || filing.Family == "" {
		return errors.New("ownership filing identity is incomplete")
	}

	if filing.ParserVersion == "" {
		return errors.New("ownership parser version is empty")
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin ownership filing transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := upsertFiling(ctx, tx, filing, "processed", "", now); err != nil {
		return err
	}

	if err := deleteFilingRows(ctx, tx, filing.Accession); err != nil {
		return err
	}

	partyIDs := make(map[string]string, len(filing.Parties))

	partyOrdinals := make(map[string]int, len(filing.Parties))
	for index, party := range filing.Parties {
		id, cik, err := partyIdentity(filing.Accession, party)
		if err != nil {
			return err
		}

		partyIDs[party.Key] = id
		partyOrdinals[party.Key] = index + 1

		if _, err := tx.ExecContext(ctx, `INSERT INTO ownership_parties (
			party_id, cik, name, party_type, updated_at
		) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(party_id) DO UPDATE SET
			cik = CASE WHEN excluded.cik != '' THEN excluded.cik ELSE cik END,
			name = CASE WHEN excluded.name != '' THEN excluded.name ELSE name END,
			party_type = CASE
				WHEN excluded.party_type != '' THEN excluded.party_type
				ELSE party_type
			END,
			updated_at = excluded.updated_at`, id, cik, party.Name, party.Type,
			now); err != nil {
			return fmt.Errorf("save ownership party %s: %w", party.Key, err)
		}

		if _, err := tx.ExecContext(ctx, `INSERT INTO ownership_filing_parties (
			accession, party_id, role, ordinal, is_director, is_officer,
			is_ten_percent_owner, is_other, officer_title, member_of_group
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, filing.Accession, id,
			party.Role, index+1, boolInt(party.IsDirector),
			boolInt(party.IsOfficer), boolInt(party.IsTenPercent),
			boolInt(party.IsOther), party.OfficerTitle,
			party.MemberOfGroup); err != nil {
			return fmt.Errorf("save filing party %s: %w", party.Key, err)
		}
	}

	securityIDs := make(map[string]string, len(filing.Securities))
	for _, security := range filing.Securities {
		issuerID := partyIDs[security.IssuerKey]
		id := securityIdentity(issuerID, security)
		securityIDs[security.Key] = id

		var issuer any
		if issuerID != "" {
			issuer = issuerID
		}

		if _, err := tx.ExecContext(ctx, `INSERT INTO ownership_securities (
			security_id, issuer_party_id, title, security_class,
			security_kind, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(security_id) DO UPDATE SET
			issuer_party_id = COALESCE(
				excluded.issuer_party_id, issuer_party_id),
			title = CASE WHEN excluded.title != '' THEN excluded.title ELSE title END,
			security_class = CASE
				WHEN excluded.security_class != '' THEN excluded.security_class
				ELSE security_class
			END,
			security_kind = CASE
				WHEN excluded.security_kind != '' THEN excluded.security_kind
				ELSE security_kind
			END,
			updated_at = excluded.updated_at`, id, issuer, security.Title,
			security.Class, security.Kind, now); err != nil {
			return fmt.Errorf("save ownership security %s: %w",
				security.Key, err)
		}

		identifiers := make([][2]string, 0, 2+len(security.AlternateCUSIPs))
		if security.CUSIP != "" {
			identifiers = append(identifiers,
				[2]string{"cusip", normalizeIdentifier(security.CUSIP)})
		}

		for _, cusip := range security.AlternateCUSIPs {
			identifiers = append(identifiers,
				[2]string{"cusip", normalizeIdentifier(cusip)})
		}

		if security.Ticker != "" {
			identifiers = append(identifiers,
				[2]string{"ticker", normalizeIdentifier(security.Ticker)})
		}

		for _, identifier := range identifiers {
			if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO
				ownership_security_identifiers (
					security_id, identifier_type, identifier_value,
					source_accession, confidence
				) VALUES (?, ?, ?, ?, ?)`, id, identifier[0], identifier[1],
				filing.Accession, "reported"); err != nil {
				return fmt.Errorf("save security identifier: %w", err)
			}
		}
	}

	if err := saveInstitutional(ctx, tx, filing, partyIDs,
		securityIDs); err != nil {
		return err
	}

	if err := saveBeneficial(ctx, tx, filing, partyIDs,
		securityIDs); err != nil {
		return err
	}

	if err := saveInsider(ctx, tx, filing, partyIDs, partyOrdinals,
		securityIDs); err != nil {
		return err
	}

	for index, signature := range filing.Signatures {
		if _, err := tx.ExecContext(ctx, `INSERT INTO ownership_signatures (
			accession, ordinal, signer_name, signature_date
		) VALUES (?, ?, ?, ?)`, filing.Accession, index+1,
			signature.Name, signature.Date); err != nil {
			return fmt.Errorf("save ownership signature %d: %w", index+1, err)
		}
	}

	for _, footnote := range filing.Footnotes {
		if footnote.ID == "" {
			continue
		}

		if _, err := tx.ExecContext(ctx, `INSERT INTO ownership_footnotes (
			accession, footnote_id, footnote_text
		) VALUES (?, ?, ?)`, filing.Accession, footnote.ID,
			footnote.Text); err != nil {
			return fmt.Errorf("save ownership footnote %s: %w",
				footnote.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit ownership filing: %w", err)
	}

	return nil
}

// RecordOutcome stores a failed or unsupported terminal outcome.
func (store *Store) RecordOutcome(ctx context.Context, outcome Outcome) error {
	if outcome.Status != "failed" && outcome.Status != "unsupported" {
		return fmt.Errorf("invalid ownership outcome status %q", outcome.Status)
	}

	filing := ownership.Filing{
		Accession: outcome.Accession, FormType: outcome.FormType,
		Family: outcome.Family, FilingDate: outcome.FilingDate,
		ReportDate: outcome.ReportDate, SourcePath: outcome.SourcePath,
		SourceSHA256:  outcome.SourceSHA256,
		ParserVersion: outcome.ParserVersion,
		Amendment:     ownership.IsAmendment(outcome.FormType),
	}
	if filing.ParserVersion == "" {
		filing.ParserVersion = ownership.ParserVersion
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin ownership outcome transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := upsertFiling(ctx, tx, filing, outcome.Status,
		truncateDiagnostic(outcome.Diagnostic),
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit ownership outcome: %w", err)
	}

	return nil
}

func upsertFiling(ctx context.Context, tx *sql.Tx, filing ownership.Filing,
	status, diagnostic, now string,
) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO ownership_filings (
		accession, form_type, form_family, filing_date, report_date,
		source_path, source_sha256, parser_version, is_amendment,
		amendment_type, amendment_number, original_submission_date,
		previous_accession, report_kind, status, diagnostic, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(accession) DO UPDATE SET
		form_type = excluded.form_type,
		form_family = excluded.form_family,
		filing_date = excluded.filing_date,
		report_date = excluded.report_date,
		source_path = excluded.source_path,
		source_sha256 = excluded.source_sha256,
		parser_version = excluded.parser_version,
		is_amendment = excluded.is_amendment,
		amendment_type = excluded.amendment_type,
		amendment_number = excluded.amendment_number,
		original_submission_date = excluded.original_submission_date,
		previous_accession = excluded.previous_accession,
		report_kind = excluded.report_kind,
		status = excluded.status,
		diagnostic = excluded.diagnostic,
		updated_at = excluded.updated_at`, filing.Accession, filing.FormType,
		filing.Family, filing.FilingDate, filing.ReportDate,
		filing.SourcePath, filing.SourceSHA256, filing.ParserVersion,
		boolInt(filing.Amendment), filing.AmendmentType,
		filing.AmendmentNumber, filing.OriginalSubmissionDate,
		filing.PreviousAccession, filing.ReportKind, status, diagnostic, now)
	if err != nil {
		return fmt.Errorf("save ownership filing %s: %w", filing.Accession, err)
	}

	return nil
}

func deleteFilingRows(ctx context.Context, tx *sql.Tx,
	accession string,
) error {
	statements := []struct {
		name  string
		query string
	}{
		{"ownership_security_identifiers", `DELETE FROM
			ownership_security_identifiers WHERE source_accession = ?`},
		{"institutional_positions", `DELETE FROM
			institutional_positions WHERE accession = ?`},
		{"institutional_summaries", `DELETE FROM
			institutional_summaries WHERE accession = ?`},
		{"institutional_notices", `DELETE FROM
			institutional_notices WHERE accession = ?`},
		{"beneficial_ownership_reports", `DELETE FROM
			beneficial_ownership_reports WHERE accession = ?`},
		{"beneficial_ownership_narratives", `DELETE FROM
			beneficial_ownership_narratives WHERE accession = ?`},
		{"insider_holdings", `DELETE FROM
			insider_holdings WHERE accession = ?`},
		{"insider_transactions", `DELETE FROM
			insider_transactions WHERE accession = ?`},
		{"ownership_footnotes", `DELETE FROM
			ownership_footnotes WHERE accession = ?`},
		{"ownership_signatures", `DELETE FROM
			ownership_signatures WHERE accession = ?`},
		{"ownership_filing_parties", `DELETE FROM
			ownership_filing_parties WHERE accession = ?`},
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement.query, accession); err != nil {
			return fmt.Errorf("replace %s rows: %w", statement.name, err)
		}
	}

	return nil
}

func saveInstitutional(ctx context.Context, tx *sql.Tx,
	filing ownership.Filing, partyIDs, securityIDs map[string]string,
) error {
	if filing.InstitutionalSummary != nil {
		_, err := tx.ExecContext(ctx, `INSERT INTO institutional_summaries (
			accession, entry_count, total_value, included_manager_count
		) VALUES (?, ?, ?, ?)`, filing.Accession,
			filing.InstitutionalSummary.EntryCount,
			filing.InstitutionalSummary.TotalValue,
			filing.InstitutionalSummary.IncludedManagerCount)
		if err != nil {
			return fmt.Errorf("save institutional summary: %w", err)
		}
	}

	for _, position := range filing.InstitutionalPositions {
		securityID := securityIDs["cusip:"+position.CUSIP]
		if securityID == "" {
			return fmt.Errorf("position row %d has no security", position.Row)
		}

		_, err := tx.ExecContext(ctx, `INSERT INTO institutional_positions (
			accession, row_number, manager_party_id, security_id,
			report_period, issuer_name, security_class, cusip, figi,
			reported_value, amount, amount_type, put_call,
			investment_discretion, other_managers, voting_sole,
			voting_shared, voting_none
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			filing.Accession, position.Row, partyIDs[position.ManagerKey],
			securityID, position.ReportPeriod, position.IssuerName,
			position.Class, position.CUSIP, position.FIGI, position.Value,
			position.Amount, position.AmountType, position.PutCall,
			position.InvestmentDiscretion, position.OtherManagers,
			position.VotingSole, position.VotingShared, position.VotingNone)
		if err != nil {
			return fmt.Errorf("save institutional position %d: %w",
				position.Row, err)
		}
	}

	for _, notice := range filing.InstitutionalNotices {
		_, err := tx.ExecContext(ctx, `INSERT INTO institutional_notices (
			accession, row_number, manager_party_id, report_period,
			notice_kind, reporting_manager_name, reporting_manager_file
		) VALUES (?, ?, ?, ?, ?, ?, ?)`, filing.Accession, notice.Row,
			partyIDs[notice.ManagerKey], notice.ReportPeriod, notice.Kind,
			notice.ReportingManagerName, notice.ReportingManagerFile)
		if err != nil {
			return fmt.Errorf("save institutional notice %d: %w",
				notice.Row, err)
		}
	}

	return nil
}

func saveBeneficial(ctx context.Context, tx *sql.Tx,
	filing ownership.Filing, partyIDs, securityIDs map[string]string,
) error {
	for _, report := range filing.BeneficialReports {
		_, err := tx.ExecContext(ctx, `INSERT INTO beneficial_ownership_reports (
			accession, row_number, issuer_party_id, owner_party_id,
			security_id, cusip, event_date, shares, percentage,
			sole_voting_power, shared_voting_power,
			sole_dispositive_power, shared_dispositive_power,
			reporting_person_type, filer_category
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			filing.Accession, report.Row, partyIDs[report.IssuerKey],
			partyIDs[report.OwnerKey], securityIDs[report.SecurityKey],
			report.CUSIP, report.EventDate, report.Shares, report.Percent,
			report.SoleVotingPower, report.SharedVotingPower,
			report.SoleDispositivePower, report.SharedDispositivePower,
			report.ReportingPersonType, report.FilerCategory)
		if err != nil {
			return fmt.Errorf("save beneficial ownership row %d: %w",
				report.Row, err)
		}
	}

	for _, narrative := range filing.BeneficialNarratives {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO beneficial_ownership_narratives (
				accession, row_number, item_name, field_name, field_text
			) VALUES (?, ?, ?, ?, ?)`, filing.Accession, narrative.Row,
			narrative.Item, narrative.Field, narrative.Text)
		if err != nil {
			return fmt.Errorf("save beneficial narrative %d: %w",
				narrative.Row, err)
		}
	}

	return nil
}

func saveInsider(ctx context.Context, tx *sql.Tx,
	filing ownership.Filing, partyIDs map[string]string,
	partyOrdinals map[string]int, securityIDs map[string]string,
) error {
	for _, holding := range filing.InsiderHoldings {
		_, err := tx.ExecContext(ctx, `INSERT INTO insider_holdings (
			accession, row_number, owner_ordinal, issuer_party_id,
			owner_party_id, security_id, report_date, is_derivative,
			amount, direct_or_indirect, nature_of_ownership,
			transaction_form_type, exercise_price, exercise_date,
			expiration_date, underlying_title, underlying_shares,
			footnote_refs
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			filing.Accession, holding.Row, partyOrdinals[holding.OwnerKey],
			partyIDs[holding.IssuerKey], partyIDs[holding.OwnerKey],
			securityIDs[holding.SecurityKey], holding.ReportDate,
			boolInt(holding.Derivative), holding.Amount,
			holding.DirectOrIndirect, holding.NatureOfOwnership,
			holding.TransactionFormType, holding.ExercisePrice,
			holding.ExerciseDate, holding.ExpirationDate,
			holding.UnderlyingTitle, holding.UnderlyingShares,
			holding.FootnoteRefs)
		if err != nil {
			return fmt.Errorf("save insider holding %d: %w", holding.Row, err)
		}
	}

	for _, transaction := range filing.InsiderTransactions {
		_, err := tx.ExecContext(ctx, `INSERT INTO insider_transactions (
			accession, row_number, owner_ordinal, issuer_party_id,
			owner_party_id, security_id, report_date, is_derivative,
			transaction_date, execution_date, transaction_form_type,
			transaction_code, equity_swap_involved, acquired_disposed,
			amount, price, shares_after, direct_or_indirect,
			nature_of_ownership, exercise_price, exercise_date,
			expiration_date, underlying_title, underlying_shares,
			footnote_refs
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?)`, filing.Accession, transaction.Row,
			partyOrdinals[transaction.OwnerKey],
			partyIDs[transaction.IssuerKey], partyIDs[transaction.OwnerKey],
			securityIDs[transaction.SecurityKey], transaction.ReportDate,
			boolInt(transaction.Derivative), transaction.TransactionDate,
			transaction.ExecutionDate, transaction.TransactionFormType,
			transaction.TransactionCode, transaction.EquitySwapInvolved,
			transaction.AcquiredDisposed, transaction.Amount,
			transaction.Price, transaction.SharesAfter,
			transaction.DirectOrIndirect, transaction.NatureOfOwnership,
			transaction.ExercisePrice, transaction.ExerciseDate,
			transaction.ExpirationDate, transaction.UnderlyingTitle,
			transaction.UnderlyingShares, transaction.FootnoteRefs)
		if err != nil {
			return fmt.Errorf("save insider transaction %d: %w",
				transaction.Row, err)
		}
	}

	return nil
}

// SaveCoverage replaces the coverage summary for one family and interval.
func (store *Store) SaveCoverage(ctx context.Context,
	coverage Coverage,
) error {
	_, err := store.db.ExecContext(ctx, `INSERT INTO ownership_coverage (
		form_family, from_date, through_date, parser_version,
		expected_count, processed_count, failed_count, unsupported_count,
		coverage_status, started_at, completed_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(form_family, from_date, through_date, parser_version)
	DO UPDATE SET
		expected_count = excluded.expected_count,
		processed_count = excluded.processed_count,
		failed_count = excluded.failed_count,
		unsupported_count = excluded.unsupported_count,
		coverage_status = excluded.coverage_status,
		started_at = excluded.started_at,
		completed_at = excluded.completed_at`, coverage.Family,
		coverage.FromDate, coverage.ThroughDate, coverage.Version,
		coverage.Expected, coverage.Processed, coverage.Failed,
		coverage.Unsupported, coverage.Status,
		formatOptionalTime(coverage.StartedAt),
		formatOptionalTime(coverage.CompletedAt))
	if err != nil {
		return fmt.Errorf("save ownership coverage: %w", err)
	}

	return nil
}

// Counts returns filing outcomes in the requested inclusive date interval.
func (store *Store) Counts(ctx context.Context, fromDate,
	throughDate string,
) ([]Count, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT
		form_family, status, COUNT(*)
		FROM ownership_filings
		WHERE (? = '' OR filing_date >= ?)
		  AND (? = '' OR filing_date <= ?)
		GROUP BY form_family, status
		ORDER BY form_family, status`, fromDate, fromDate,
		throughDate, throughDate)
	if err != nil {
		return nil, fmt.Errorf("count ownership outcomes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var counts []Count

	for rows.Next() {
		var count Count
		if err := rows.Scan(&count.Family, &count.Status,
			&count.Count); err != nil {
			return nil, fmt.Errorf("scan ownership outcome count: %w", err)
		}

		counts = append(counts, count)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read ownership outcome counts: %w", err)
	}

	return counts, nil
}

// LatestCoverage returns the most recently completed interval for each form
// family at parserVersion.
func (store *Store) LatestCoverage(ctx context.Context,
	parserVersion string,
) ([]Coverage, error) {
	rows, err := store.db.QueryContext(ctx, `WITH ranked AS (
		SELECT
			form_family, from_date, through_date, parser_version,
			expected_count, processed_count, failed_count,
			unsupported_count, coverage_status, started_at, completed_at,
			ROW_NUMBER() OVER (
				PARTITION BY form_family
				ORDER BY completed_at DESC, from_date ASC
			) AS rank_number
		FROM ownership_coverage
		WHERE parser_version = ?
	)
	SELECT
		form_family, from_date, through_date, parser_version,
		expected_count, processed_count, failed_count, unsupported_count,
		coverage_status, started_at, completed_at
	FROM ranked
	WHERE rank_number = 1
	ORDER BY form_family`, parserVersion)
	if err != nil {
		return nil, fmt.Errorf("read ownership coverage: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []Coverage

	for rows.Next() {
		var (
			coverage               Coverage
			startedAt, completedAt string
		)
		if err := rows.Scan(&coverage.Family, &coverage.FromDate,
			&coverage.ThroughDate, &coverage.Version, &coverage.Expected,
			&coverage.Processed, &coverage.Failed, &coverage.Unsupported,
			&coverage.Status, &startedAt, &completedAt); err != nil {
			return nil, fmt.Errorf("scan ownership coverage: %w", err)
		}

		coverage.StartedAt, err = parseOptionalTime(startedAt)
		if err != nil {
			return nil, err
		}

		coverage.CompletedAt, err = parseOptionalTime(completedAt)
		if err != nil {
			return nil, err
		}

		result = append(result, coverage)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read ownership coverage rows: %w", err)
	}

	return result, nil
}

// RowCount returns the number of rows in a known ownership table or view.
func (store *Store) RowCount(ctx context.Context, name string) (int64, error) {
	allowed := map[string]bool{
		"ownership_filings": true, "institutional_positions": true,
		"institutional_notices": true, "institutional_summaries": true,
		"beneficial_ownership_reports":    true,
		"beneficial_ownership_narratives": true,
		"insider_holdings":                true, "insider_transactions": true,
		"ownership_signatures":              true,
		"effective_institutional_positions": true,
		"latest_beneficial_ownership":       true,
		"latest_insider_holdings":           true,
	}
	if !allowed[name] {
		return 0, fmt.Errorf("unknown ownership table %q", name)
	}

	var count int64
	if err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM "+name).Scan(&count); err != nil {
		return 0, fmt.Errorf("count %s: %w", name, err)
	}

	return count, nil
}

func partyIdentity(accession string,
	party ownership.Party,
) (string, string, error) {
	if strings.TrimSpace(party.CIK) != "" {
		cik, err := canonicalCIK(party.CIK)
		if err != nil {
			return "", "", fmt.Errorf("party %s: %w", party.Key, err)
		}

		return "cik:" + cik, cik, nil
	}

	return "filing:" + accession + ":party:" + party.Key, "", nil
}

func canonicalCIK(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}

	if strings.Trim(value, "0123456789") != "" || len(value) > 10 {
		return "", fmt.Errorf("invalid CIK %q", value)
	}

	return strings.Repeat("0", 10-len(value)) + value, nil
}

func securityIdentity(issuerID string, security ownership.Security) string {
	if cusip := normalizeIdentifier(security.CUSIP); cusip != "" {
		return "cusip:" + cusip
	}

	value := strings.Join([]string{
		issuerID, security.Kind, security.Title, security.Class,
	}, "\x00")
	sum := sha256.Sum256([]byte(value))

	return "security:" + hex.EncodeToString(sum[:])
}

func normalizeIdentifier(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func truncateDiagnostic(value string) string {
	const maximum = 4000
	if len(value) <= maximum {
		return value
	}

	return value[:maximum]
}

func boolInt(value bool) int64 {
	if value {
		return 1
	}

	return 0
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}

	return value.UTC().Format(time.RFC3339Nano)
}

func parseOptionalTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}

	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse ownership coverage time: %w", err)
	}

	return parsed, nil
}
