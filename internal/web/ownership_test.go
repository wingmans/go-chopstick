package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wingman.com/chopstick/internal/constituents"
	"wingman.com/chopstick/internal/ownership"
	"wingman.com/chopstick/internal/ownershipstore"
)

func TestOwnershipViewerUsesSelectedCompanySet(t *testing.T) {
	root := t.TempDir()

	setDir := filepath.Join(root, "sets")
	if err := os.MkdirAll(setDir, 0o750); err != nil {
		t.Fatal(err)
	}

	var setData bytes.Buffer

	set := constituents.Set{
		Name: "test", AsOf: "2026-10-28", Source: "test",
		Members: []constituents.Member{
			{CIK: "100", Ticker: "ACME", Name: "Acme Corporation"},
			{CIK: "101", Ticker: "BETA", Name: "Beta Corporation"},
		},
	}
	if err := set.WriteJSON(&setData); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(setDir, "test.json"),
		setData.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	ownershipPath := filepath.Join(root, "ownership.db")

	ownershipDB, err := ownershipstore.Open(t.Context(), ownershipPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := ownershipDB.Save(t.Context(), beneficialTestFiling()); err != nil {
		t.Fatal(err)
	}

	if err := ownershipDB.Save(t.Context(), institutionalTestFiling()); err != nil {
		t.Fatal(err)
	}

	if err := ownershipDB.Close(); err != nil {
		t.Fatal(err)
	}

	server, err := NewConfiguredServerWithDatabases(t.Context(),
		filepath.Join(root, "parsed"), filepath.Join(root, "analysis.db"),
		ownershipPath, setDir, "test", nil)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = server.Close() })

	directory := ownershipRequest(t, server, "/ownership?q=acme")
	requireOwnershipBody(t, directory, "Acme Corporation")

	if strings.Contains(directory.Body.String(), "Beta Corporation") {
		t.Fatal("filtered directory contains Beta Corporation")
	}

	company := ownershipRequest(t, server,
		"/ownership/companies/0000000100")
	requireOwnershipBody(t, company, "Alice Owner", "Large Manager",
		"Schedule 13D/G beneficial ownership",
		"Mapped Form 13F positions")

	missing := ownershipRequest(t, server,
		"/ownership/companies/0000000999")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("outside-set status = %d, want %d",
			missing.Code, http.StatusNotFound)
	}
}

func beneficialTestFiling() ownership.Filing {
	return ownership.Filing{
		Accession: "0000000100-26-000001", FormType: "SC 13G",
		Family: "beneficial", FilingDate: "2026-01-15",
		ReportDate:   "2025-12-31",
		SourcePath:   "/cache/edgar/data/100/filing.txt",
		SourceSHA256: "beneficial", ParserVersion: ownership.ParserVersion,
		Parties: []ownership.Party{
			{Key: "issuer", CIK: "100", Name: "Acme Corporation"},
			{Key: "owner", CIK: "200", Name: "Alice Owner"},
		},
		Securities: []ownership.Security{{
			Key: "stock", IssuerKey: "issuer", Title: "Common stock",
			CUSIP: "000000100",
		}},
		BeneficialReports: []ownership.BeneficialReport{{
			Row: 1, IssuerKey: "issuer", OwnerKey: "owner",
			SecurityKey: "stock", CUSIP: "000000100",
			EventDate: "2025-12-31", Shares: "125000", Percent: "6.2",
		}},
	}
}

func institutionalTestFiling() ownership.Filing {
	return ownership.Filing{
		Accession: "0000000300-26-000001", FormType: "13F-HR",
		Family: "institutional", FilingDate: "2026-02-14",
		ReportDate:   "2025-12-31",
		SourcePath:   "/cache/edgar/data/300/filing.txt",
		SourceSHA256: "institutional", ParserVersion: ownership.ParserVersion,
		Parties: []ownership.Party{{
			Key: "manager", CIK: "300", Name: "Large Manager",
		}},
		Securities: []ownership.Security{{
			Key: "cusip:000000100", Title: "Common stock",
			CUSIP: "000000100",
		}},
		InstitutionalPositions: []ownership.InstitutionalPosition{{
			Row: 1, ManagerKey: "manager", ReportPeriod: "2025-12-31",
			IssuerName: "Acme Corporation", Class: "COM",
			CUSIP: "000000100", Value: "8500", Amount: "100000",
			AmountType: "SH",
		}},
	}
}

func ownershipRequest(t *testing.T, server http.Handler,
	path string,
) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequestWithContext(t.Context(),
		http.MethodGet, path, nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	return response
}

func requireOwnershipBody(t *testing.T, response *httptest.ResponseRecorder,
	values ...string,
) {
	t.Helper()

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}

	for _, value := range values {
		if !strings.Contains(response.Body.String(), value) {
			t.Fatalf("response does not contain %q", value)
		}
	}
}
