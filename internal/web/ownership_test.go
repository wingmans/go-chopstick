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
	ownershipstore "wingman.com/chopstick/internal/ownership/store"
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
	sourcePath := writeOwnershipViewerSource(t, root)

	ownershipDB, err := ownershipstore.Open(t.Context(), ownershipPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := ownershipDB.Save(t.Context(),
		beneficialTestFiling(sourcePath)); err != nil {
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
	requireOwnershipBody(t, company, "Alice Owner",
		"Schedule 13D/G beneficial ownership",
		`href="https://www.sec.gov/Archives/edgar/data/100/`+
			`0000000100-26-000001.txt"`,
		`href="/viewer/0000000100/0000000100-26-000001/"`,
		`class="viewer-button"`)

	inspector := ownershipRequest(t, server,
		"/debug/raw/filings/0000000100/0000000100-26-000001")
	requireOwnershipBody(t, inspector, "Raw Filing Inspector", "ownership.htm",
		"pixel.jpg")

	document := ownershipRequest(t, server,
		"/debug/raw/filings/0000000100/0000000100-26-000001/documents/0")
	requireOwnershipBody(t, document, "ownership filing")

	viewer := ownershipRequest(t, server,
		"/viewer/0000000100/0000000100-26-000001/")
	requireOwnershipBody(t, viewer, "SEC artifact viewer", "ownership.htm",
		"pixel.jpg", `data-image="true"`)

	viewerRedirect := ownershipRequest(t, server,
		"/viewer/0000000100/0000000100-26-000001")
	if viewerRedirect.Code != http.StatusMovedPermanently ||
		viewerRedirect.Header().Get("Location") !=
			"/viewer/0000000100/0000000100-26-000001/" {
		t.Fatalf("viewer redirect: status=%d location=%q",
			viewerRedirect.Code, viewerRedirect.Header().Get("Location"))
	}

	image := ownershipRequest(t, server,
		"/viewer/0000000100/0000000100-26-000001/files/pixel.jpg")
	requireOwnershipBody(t, image, "ABC")

	missing := ownershipRequest(t, server,
		"/ownership/companies/0000000999")
	if missing.Code != http.StatusNotFound {
		t.Fatalf("outside-set status = %d, want %d",
			missing.Code, http.StatusNotFound)
	}
}

func beneficialTestFiling(sourcePath string) ownership.Filing {
	return ownership.Filing{
		Accession: "0000000100-26-000001", FormType: "SCHEDULE 13G",
		Family: "beneficial", FilingDate: "2026-01-15",
		ReportDate:   "2025-12-31",
		SourcePath:   sourcePath,
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

func writeOwnershipViewerSource(t *testing.T, root string) string {
	t.Helper()

	directory := filepath.Join(root, "cache", "filings", "edgar", "data",
		"100")
	if err := os.MkdirAll(directory, 0o750); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(directory, "0000000100-26-000001.txt")

	source := `<SEC-DOCUMENT>0000000100-26-000001.txt
<SEC-HEADER>header
ACCESSION NUMBER: 0000000100-26-000001
CONFORMED SUBMISSION TYPE: SCHEDULE 13G
CENTRAL INDEX KEY: 0000000100
FILED AS OF DATE: 20260115
</SEC-HEADER>
<DOCUMENT>
<TYPE>SCHEDULE 13G
<SEQUENCE>1
<FILENAME>ownership.htm
<DESCRIPTION>ownership filing
<TEXT>
<html><body>ownership filing<img src="pixel.jpg"></body></html>
</TEXT>
</DOCUMENT>
<DOCUMENT>
<TYPE>GRAPHIC
<SEQUENCE>2
<FILENAME>pixel.jpg
<DESCRIPTION>embedded image
<TEXT>
begin 644 pixel.jpg
#04)#
` + "`" + `
end
</TEXT>
</DOCUMENT>
</SEC-DOCUMENT>
`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
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
