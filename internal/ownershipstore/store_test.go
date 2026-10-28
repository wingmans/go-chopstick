package ownershipstore

import (
	"database/sql"
	"path/filepath"
	"testing"

	"wingman.com/fetch-ecb/internal/ownership"
)

func TestStoreSavesFilingIdempotently(t *testing.T) {
	t.Parallel()

	store, err := Open(t.Context(), filepath.Join(t.TempDir(), "ownership.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })

	filing := ownership.Filing{
		Accession: "0000000001-26-000001", FormType: "4",
		Family: ownership.FamilyInsider, FilingDate: "2026-02-15",
		ReportDate: "2026-02-10", SourcePath: "cached.txt",
		SourceSHA256: "abc", ParserVersion: ownership.ParserVersion,
		Parties: []ownership.Party{
			{Key: "issuer", CIK: "100", Name: "Issuer", Role: "issuer"},
			{
				Key: "owner:1", CIK: "200", Name: "Owner",
				Role: "reporting_owner",
			},
		},
		Securities: []ownership.Security{{
			Key: "security:1", IssuerKey: "issuer", Title: "Common Stock",
			Class: "Common Stock", Kind: "non_derivative", Ticker: "EXM",
		}},
		InsiderTransactions: []ownership.InsiderTransaction{{
			Row: 1, IssuerKey: "issuer", OwnerKey: "owner:1",
			SecurityKey: "security:1", ReportDate: "2026-02-10",
			TransactionDate: "2026-02-09", TransactionCode: "S",
			Amount: "10", SharesAfter: "90", DirectOrIndirect: "D",
		}},
		Signatures: []ownership.Signature{{
			Name: "/s/ Owner", Date: "2026-02-10",
		}},
	}

	if err := store.Save(t.Context(), filing); err != nil {
		t.Fatal(err)
	}

	filing.InsiderTransactions[0].SharesAfter = "80"
	if err := store.Save(t.Context(), filing); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{
		"ownership_filings", "insider_transactions",
		"latest_insider_holdings", "ownership_signatures",
	} {
		count, err := store.RowCount(t.Context(), table)
		if err != nil {
			t.Fatal(err)
		}

		if count != 1 {
			t.Fatalf("%s count = %d", table, count)
		}
	}

	needed, err := store.NeedsImport(t.Context(), filing.Accession,
		ownership.ParserVersion)
	if err != nil || needed {
		t.Fatalf("NeedsImport() = %t, %v", needed, err)
	}
}

func TestEffective13FRestatementAndAddedHoldings(t *testing.T) {
	t.Parallel()

	store, err := Open(t.Context(), filepath.Join(t.TempDir(), "ownership.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })

	original := institutionalFiling("0000000001-26-000001", "2026-02-01", "",
		position("111111111", "Issuer A"),
		position("222222222", "Issuer B"))
	restatement := institutionalFiling("0000000001-26-000002", "2026-02-02",
		"RESTATEMENT", position("111111111", "Issuer A"))

	added := institutionalFiling("0000000001-26-000003", "2026-02-03",
		"NEW HOLDINGS", position("333333333", "Issuer C"))
	for _, filing := range []ownership.Filing{original, restatement, added} {
		if err := store.Save(t.Context(), filing); err != nil {
			t.Fatal(err)
		}
	}

	count, err := store.RowCount(t.Context(),
		"effective_institutional_positions")
	if err != nil {
		t.Fatal(err)
	}

	if count != 2 {
		t.Fatalf("effective position count = %d, want 2", count)
	}
}

func TestStoreRecordsUnsupportedAndRetriesFailures(t *testing.T) {
	t.Parallel()

	store, err := Open(t.Context(), filepath.Join(t.TempDir(), "ownership.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })

	unsupported := Outcome{
		Accession: "0000000001-23-000001", FormType: "SC 13G",
		Family: ownership.FamilyBeneficial, FilingDate: "2023-01-01",
		SourcePath: "cached.txt", ParserVersion: ownership.ParserVersion,
		Status: "unsupported", Diagnostic: "legacy format",
	}
	if err := store.RecordOutcome(t.Context(), unsupported); err != nil {
		t.Fatal(err)
	}

	needed, err := store.NeedsImport(t.Context(), unsupported.Accession,
		ownership.ParserVersion)
	if err != nil || needed {
		t.Fatalf("unsupported NeedsImport() = %t, %v", needed, err)
	}

	failed := unsupported
	failed.Accession = "0000000001-23-000002"

	failed.Status = "failed"
	if err := store.RecordOutcome(t.Context(), failed); err != nil {
		t.Fatal(err)
	}

	needed, err = store.NeedsImport(t.Context(), failed.Accession,
		ownership.ParserVersion)
	if err != nil || !needed {
		t.Fatalf("failed NeedsImport() = %t, %v", needed, err)
	}
}

func TestOwnershipSchemaVersion(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "ownership.db")

	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var version int
	if err := db.QueryRowContext(t.Context(), "PRAGMA user_version").Scan(
		&version); err != nil {
		t.Fatal(err)
	}

	if version != schemaVersion {
		t.Fatalf("schema version = %d, want %d", version, schemaVersion)
	}
}

func position(cusip, issuer string) ownership.InstitutionalPosition {
	return ownership.InstitutionalPosition{
		Row: 1, ManagerKey: "manager", ReportPeriod: "2025-12-31",
		IssuerName: issuer, Class: "COM", CUSIP: cusip,
		Value: "100", Amount: "10", AmountType: "SH",
	}
}

func institutionalFiling(accession, filingDate,
	amendmentType string, positions ...ownership.InstitutionalPosition,
) ownership.Filing {
	for index := range positions {
		positions[index].Row = index + 1
	}

	securities := make([]ownership.Security, 0, len(positions))
	for _, item := range positions {
		securities = append(securities, ownership.Security{
			Key: "cusip:" + item.CUSIP, Title: item.IssuerName,
			Class: item.Class, Kind: "reported", CUSIP: item.CUSIP,
		})
	}

	return ownership.Filing{
		Accession: accession, FormType: "13F-HR",
		Family: ownership.FamilyInstitutional, FilingDate: filingDate,
		ReportDate: "2025-12-31", SourcePath: "cached.txt",
		SourceSHA256: accession, ParserVersion: ownership.ParserVersion,
		Amendment: amendmentType != "", AmendmentType: amendmentType,
		Parties: []ownership.Party{{
			Key: "manager", CIK: "300", Name: "Manager", Role: "manager",
		}},
		Securities: securities, InstitutionalPositions: positions,
	}
}
