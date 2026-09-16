package filingweb

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wingman.com/fetch-ecb/internal/constituents"
	"wingman.com/fetch-ecb/internal/edgar"
	"wingman.com/fetch-ecb/internal/filingview"
)

type goldenDetailResults struct {
	Accession string                                  `json:"accession"`
	FormType  string                                  `json:"form_type"`
	Company   string                                  `json:"company"`
	Groups    map[string]map[string]map[string]string `json:"groups"`
}

func TestGoldenDetailPagePreservesSummaryValues(t *testing.T) {
	filingPath := filepath.Join("..", "..", "golden", "sample_10-K.txt")

	filing, err := edgar.ParseSubmission(t.Context(), filingPath)
	if err != nil {
		t.Fatal(err)
	}

	resultsFile := filepath.Join("..", "..", "golden", "golden-results.json")

	resultsData, err := os.ReadFile(resultsFile)
	if err != nil {
		t.Fatal(err)
	}

	var expected goldenDetailResults
	if err := json.Unmarshal(resultsData, &expected); err != nil {
		t.Fatal(err)
	}

	if filing.Metadata.Accession != expected.Accession ||
		filing.Metadata.FormType != expected.FormType {
		t.Fatalf("golden identity changed: accession=%s form=%s",
			filing.Metadata.Accession, filing.Metadata.FormType)
	}

	if len(filing.Metadata.Filers) == 0 ||
		filing.Metadata.Filers[0].Name != expected.Company {
		t.Fatalf("golden company changed: got %q", filing.Metadata.Filers)
	}

	view, err := filingview.Build(filing)
	if err != nil {
		t.Fatal(err)
	}

	for groupName, expectedRows := range expected.Groups {
		group := summaryGroupByTitle(view.Summary, groupName)
		if group == nil {
			t.Fatalf("summary group %q is missing", groupName)
		}

		for concept, expectedValues := range expectedRows {
			row := summaryRowByConcept(group.Rows, concept)
			if row == nil {
				t.Fatalf("summary concept %q is missing from %q", concept,
					groupName)
			}

			for period, expectedValue := range expectedValues {
				actual, ok := row.Values[period]
				if !ok || actual.Nil || actual.Value != expectedValue {
					t.Fatalf("%s %s %s: got %+v, want %q", groupName,
						concept, period, actual, expectedValue)
				}
			}
		}
	}

	parsedDir := t.TempDir()
	if _, err := edgar.SaveParsedFiling(parsedDir, filing); err != nil {
		t.Fatal(err)
	}

	if _, err := filingview.Save(parsedDir, filing); err != nil {
		t.Fatal(err)
	}

	parsedPath, err := filing.ParsedPath(parsedDir)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(parsedPath); err != nil {
		t.Fatal(err)
	}

	server := NewServer(parsedDir, nil)
	record := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/filings/789019/"+expected.Accession+
			"?return_to=%2F%3Fq%3DMSFT", nil)
	server.ServeHTTP(record, request)

	if record.Code != http.StatusOK {
		t.Fatalf("details page status=%d body=%s", record.Code,
			record.Body.String())
	}

	body := record.Body.String()
	for _, marker := range []string{
		"<title>10-K 0001193125-26-323660</title>",
		"<th>Line item</th>",
		"data-statement=\"Income statement\"",
		"data-statement=\"Balance sheet\"",
		"data-statement=\"Cash flow\"",
		"id=\"key-ratios\"",
		"id=\"filing-documents\"",
		"id=\"financial-summary\"",
		"href=\"/?q=MSFT\"",
		"data-testid=\"metric-RevenueFromContractWithCustomerExcludingAssessedTax-FY2026\"",
		"Revenue",
		"331839000000",
		"133749000000",
		"758376000000",
		"data-ratio=\"gross_margin\"",
		"67.94%",
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("details page is missing %q", marker)
		}
	}
}

func summaryGroupByTitle(groups []filingview.SummaryGroup, title string) *filingview.SummaryGroup {
	for index := range groups {
		if groups[index].Title == title {
			return &groups[index]
		}
	}

	return nil
}

func summaryRowByConcept(rows []filingview.FactSeries, concept string) *filingview.FactSeries {
	for index := range rows {
		if rows[index].Concept == concept {
			return &rows[index]
		}
	}

	return nil
}

//nolint:wsl_v5 // Test setup stays close to the behavior it exercises.
func TestServerNormalizesCIKPadding(t *testing.T) {
	parsedDir := t.TempDir()

	filing := &edgar.ParsedFiling{
		SchemaVersion: edgar.ParsedSchemaVersion,
		ParserVersion: edgar.ParserVersion,
		Metadata: edgar.FilingMetadata{
			CIK: "789019", Accession: "0001193125-26-027207", FormType: "10-Q", FilingDate: "20260913",
			ReportDate: "20260912", Items: []string{}, Header: []string{},
			Filers: []edgar.SubmissionFiler{{CIK: "789019", Name: "Example Corp.", Header: []string{}}},
		},
		SourcePath: "", SourceBase: "", SourceSHA256: "",
		Documents:   []edgar.SubmissionDocument{},
		Instances:   []edgar.XBRLInstance{},
		Diagnostics: []edgar.ParseDiagnostic{},
		Status:      edgar.ParseComplete,
	}
	if _, err := edgar.SaveParsedFiling(parsedDir, filing); err != nil {
		t.Fatalf("SaveParsedFiling returned error: %v", err)
	}

	if _, err := filingview.Save(parsedDir, filing); err != nil {
		t.Fatalf("filingview.Save returned error: %v", err)
	}

	setDir := t.TempDir()
	var setData bytes.Buffer
	set := constituents.Set{
		Name: "golden", AsOf: "2026-09-14", Source: "test",
		Members: []constituents.Member{{
			CIK: "0000789019", Ticker: "MSFT", Name: "Microsoft Corporation",
		}},
	}
	if err := set.WriteJSON(&setData); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(setDir, "golden.json"),
		setData.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	server, err := NewConfiguredServer(parsedDir, setDir, "golden", nil)
	if err != nil {
		t.Fatal(err)
	}

	record := httptest.NewRecorder()
	server.ServeHTTP(record, httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/filings", nil))
	if record.Code != http.StatusOK ||
		strings.Contains(record.Body.String(), "0001193125-26-027207") {
		t.Fatalf("unfiltered request returned filings: status=%d body=%s",
			record.Code, record.Body.String())
	}

	for _, cik := range []string{"789019", "0000789019"} {
		record = httptest.NewRecorder()
		request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/filings?cik="+cik, nil)
		server.ServeHTTP(record, request)

		if record.Code != 200 || !strings.Contains(record.Body.String(), "0001193125-26-027207") {
			t.Fatalf("CIK %s did not return the filing: status=%d body=%s", cik, record.Code, record.Body.String())
		}
	}

	record = httptest.NewRecorder()
	server.ServeHTTP(record, httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/filings?q=MSFT", nil))
	if record.Code != http.StatusOK ||
		!strings.Contains(record.Body.String(), "\"ticker\":\"MSFT\"") {
		t.Fatalf("ticker lookup failed: status=%d body=%s", record.Code,
			record.Body.String())
	}

	record = httptest.NewRecorder()
	server.ServeHTTP(record, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/filings/789019/0001193125-26-027207", nil))

	if record.Code != 200 || !strings.Contains(record.Body.String(), "XBRL facts") {
		t.Fatalf("details page failed: status=%d body=%s", record.Code, record.Body.String())
	}
}
