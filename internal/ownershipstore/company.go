package ownershipstore

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"wingman.com/chopstick/internal/constituents"
)

// CompanyOwnership is the ownership information currently known for an
// issuer. The slices are deliberately bounded for the local viewer.
type CompanyOwnership struct {
	Institutional       []InstitutionalCompanyPosition
	Beneficial          []BeneficialCompanyReport
	InsiderHoldings     []CompanyInsiderHolding
	InsiderTransactions []CompanyInsiderTransaction
}

// InstitutionalCompanyPosition is an effective Form 13F position that has an
// explicit security-to-issuer mapping from another ownership filing.
type InstitutionalCompanyPosition struct {
	Accession   string
	FormType    string
	FilingDate  string
	SourceURL   string
	ManagerCIK  string
	ManagerName string
	Period      string
	Security    string
	CUSIP       string
	Value       string
	Amount      string
	AmountType  string
	PutCall     string
}

// BeneficialCompanyReport is the latest Schedule 13D/G state for an owner and
// security.
type BeneficialCompanyReport struct {
	Accession  string
	FormType   string
	FilingDate string
	SourceURL  string
	OwnerCIK   string
	OwnerName  string
	EventDate  string
	Security   string
	CUSIP      string
	Shares     string
	Percentage string
}

// CompanyInsiderHolding is the latest reported holding for an insider and
// security.
type CompanyInsiderHolding struct {
	Accession        string
	FormType         string
	FilingDate       string
	SourceURL        string
	OwnerCIK         string
	OwnerName        string
	AsOfDate         string
	Security         string
	Amount           string
	DirectOrIndirect string
	Derivative       bool
}

// CompanyInsiderTransaction is a recent Form 3/4/5 transaction.
type CompanyInsiderTransaction struct {
	Accession        string
	FormType         string
	FilingDate       string
	SourceURL        string
	OwnerCIK         string
	OwnerName        string
	TransactionDate  string
	Security         string
	Code             string
	AcquiredDisposed string
	Amount           string
	Price            string
	SharesAfter      string
}

// LoadCompanyOwnership returns ownership data tied to the supplied issuer CIK.
func (store *Store) LoadCompanyOwnership(ctx context.Context,
	cik string,
) (CompanyOwnership, error) {
	normalized, err := constituents.NormalizeCIK(cik)
	if err != nil {
		return CompanyOwnership{}, fmt.Errorf("normalize issuer CIK: %w", err)
	}

	issuerID := "cik:" + normalized

	var result CompanyOwnership

	if result.Institutional, err = store.companyInstitutional(
		ctx, issuerID); err != nil {
		return CompanyOwnership{}, err
	}

	if result.Beneficial, err = store.companyBeneficial(
		ctx, issuerID); err != nil {
		return CompanyOwnership{}, err
	}

	if result.InsiderHoldings, err = store.companyInsiderHoldings(
		ctx, issuerID); err != nil {
		return CompanyOwnership{}, err
	}

	if result.InsiderTransactions, err = store.companyInsiderTransactions(
		ctx, issuerID); err != nil {
		return CompanyOwnership{}, err
	}

	return result, nil
}

func (store *Store) companyInstitutional(ctx context.Context,
	issuerID string,
) ([]InstitutionalCompanyPosition, error) {
	return queryCompanyRows(ctx, store.db, `SELECT
		position.accession, filing.form_type, filing.filing_date,
		filing.source_path, manager.cik, manager.name,
		position.report_period, security.title, position.cusip,
		position.reported_value, position.amount, position.amount_type,
		position.put_call
	FROM effective_institutional_positions position
	JOIN ownership_filings filing ON filing.accession = position.accession
	JOIN ownership_parties manager
	  ON manager.party_id = position.manager_party_id
	JOIN ownership_securities security
	  ON security.security_id = position.security_id
	WHERE security.issuer_party_id = ?
	ORDER BY position.report_period DESC, manager.name, position.cusip
	LIMIT 250`, issuerID, "company institutional positions",
		func(rows *sql.Rows) (InstitutionalCompanyPosition, error) {
			var (
				item       InstitutionalCompanyPosition
				sourcePath string
			)
			if err := rows.Scan(&item.Accession, &item.FormType, &item.FilingDate,
				&sourcePath, &item.ManagerCIK, &item.ManagerName, &item.Period,
				&item.Security, &item.CUSIP, &item.Value, &item.Amount,
				&item.AmountType, &item.PutCall); err != nil {
				return item, err
			}

			item.SourceURL = filingSourceURL(sourcePath)

			return item, nil
		})
}

func (store *Store) companyBeneficial(ctx context.Context,
	issuerID string,
) ([]BeneficialCompanyReport, error) {
	return queryCompanyRows(ctx, store.db, `SELECT
		report.accession, report.form_type, report.filing_date,
		filing.source_path, owner.cik, owner.name, report.event_date,
		security.title, report.cusip, report.shares, report.percentage
	FROM latest_beneficial_ownership report
	JOIN ownership_filings filing ON filing.accession = report.accession
	JOIN ownership_parties owner ON owner.party_id = report.owner_party_id
	JOIN ownership_securities security
	  ON security.security_id = report.security_id
	WHERE report.issuer_party_id = ?
	ORDER BY report.event_date DESC, report.filing_date DESC, owner.name
	LIMIT 250`, issuerID, "company beneficial ownership",
		func(rows *sql.Rows) (BeneficialCompanyReport, error) {
			var (
				item       BeneficialCompanyReport
				sourcePath string
			)
			if err := rows.Scan(&item.Accession, &item.FormType, &item.FilingDate,
				&sourcePath, &item.OwnerCIK, &item.OwnerName, &item.EventDate,
				&item.Security, &item.CUSIP, &item.Shares,
				&item.Percentage); err != nil {
				return item, err
			}

			item.SourceURL = filingSourceURL(sourcePath)

			return item, nil
		})
}

func (store *Store) companyInsiderHoldings(ctx context.Context,
	issuerID string,
) ([]CompanyInsiderHolding, error) {
	return queryCompanyRows(ctx, store.db, `SELECT
		holding.accession, filing.form_type, filing.filing_date,
		filing.source_path, owner.cik, owner.name, holding.as_of_date,
		security.title, holding.amount, holding.direct_or_indirect,
		holding.is_derivative
	FROM latest_insider_holdings holding
	JOIN ownership_filings filing ON filing.accession = holding.accession
	JOIN ownership_parties owner ON owner.party_id = holding.owner_party_id
	JOIN ownership_securities security
	  ON security.security_id = holding.security_id
	WHERE holding.issuer_party_id = ?
	ORDER BY holding.as_of_date DESC, owner.name, security.title
	LIMIT 250`, issuerID, "company insider holdings",
		func(rows *sql.Rows) (CompanyInsiderHolding, error) {
			var (
				item       CompanyInsiderHolding
				sourcePath string
			)
			if err := rows.Scan(&item.Accession, &item.FormType, &item.FilingDate,
				&sourcePath, &item.OwnerCIK, &item.OwnerName, &item.AsOfDate,
				&item.Security, &item.Amount, &item.DirectOrIndirect,
				&item.Derivative); err != nil {
				return item, err
			}

			item.SourceURL = filingSourceURL(sourcePath)

			return item, nil
		})
}

func (store *Store) companyInsiderTransactions(ctx context.Context,
	issuerID string,
) ([]CompanyInsiderTransaction, error) {
	return queryCompanyRows(ctx, store.db, `SELECT
		txn.accession, filing.form_type, filing.filing_date,
		filing.source_path, owner.cik, owner.name,
		txn.transaction_date, security.title,
		txn.transaction_code, txn.acquired_disposed,
		txn.amount, txn.price, txn.shares_after
	FROM insider_transactions txn
	JOIN ownership_filings filing ON filing.accession = txn.accession
	JOIN ownership_parties owner ON owner.party_id = txn.owner_party_id
	JOIN ownership_securities security
	  ON security.security_id = txn.security_id
	WHERE txn.issuer_party_id = ? AND filing.status = 'processed'
	ORDER BY txn.transaction_date DESC, filing.filing_date DESC,
		txn.accession DESC
	LIMIT 100`, issuerID, "company insider transactions",
		func(rows *sql.Rows) (CompanyInsiderTransaction, error) {
			var (
				item       CompanyInsiderTransaction
				sourcePath string
			)
			if err := rows.Scan(&item.Accession, &item.FormType, &item.FilingDate,
				&sourcePath, &item.OwnerCIK, &item.OwnerName,
				&item.TransactionDate, &item.Security, &item.Code,
				&item.AcquiredDisposed, &item.Amount, &item.Price,
				&item.SharesAfter); err != nil {
				return item, err
			}

			item.SourceURL = filingSourceURL(sourcePath)

			return item, nil
		})
}

func queryCompanyRows[T any](ctx context.Context, db *sql.DB, query,
	issuerID, description string, scan func(*sql.Rows) (T, error),
) ([]T, error) {
	rows, err := db.QueryContext(ctx, query, issuerID)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", description, err)
	}
	defer func() { _ = rows.Close() }()

	result := make([]T, 0)

	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", description, err)
		}

		result = append(result, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s: %w", description, err)
	}

	return result, nil
}

func filingSourceURL(sourcePath string) string {
	const marker = "edgar/data/"

	index := strings.Index(strings.ToLower(sourcePath), marker)
	if index < 0 {
		return ""
	}

	return "https://www.sec.gov/Archives/" + sourcePath[index:]
}
