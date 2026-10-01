package web

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

	"wingman.com/fetch-ecb/internal/analysisstore"
	"wingman.com/fetch-ecb/internal/constituents"
	dividendview "wingman.com/fetch-ecb/internal/dividend"
	"wingman.com/fetch-ecb/internal/edgar"
	filingview "wingman.com/fetch-ecb/internal/filing"
)

type goldenDetailResults struct {
	Accession string                                  `json:"accession"`
	FormType  string                                  `json:"form_type"`
	Company   string                                  `json:"company"`
	Groups    map[string]map[string]map[string]string `json:"groups"`
}

func TestGoldenDetailPagePreservesSummaryValues(t *testing.T) {
	filingPath := filepath.Join("..", "..", "golden", "formtypes", "sample_10-K.txt")

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

	analysis, err := analysisstore.Open(context.Background(), filepath.Join(filepath.Dir(parsedDir), "analysis.db"))
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = analysis.Close() }()

	if err := analysis.SaveFilingView(t.Context(), view); err != nil {
		t.Fatal(err)
	}

	dividendPath := filepath.Join(parsedDir, "0000789019", "dividend-view.json")
	dividends := dividendview.View{
		SchemaVersion: dividendview.SchemaVersion, CIK: "0000789019",
		Company: expected.Company,
		Events: []dividendview.Event{{
			ID: "test-dividend", DeclarationDate: "20260401",
			AmountPerShare: "0.83", Currency: "USD", Type: "cash_common",
			Status: "announced", Confidence: "medium",
		}},
		Observations: []dividendview.Observation{{
			Kind: "cash_paid", Period: "2026-06-30", Value: "1234000000",
		}},
	}

	data, err := json.MarshalIndent(dividends, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(dividendPath, data, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := analysis.SaveDividendView(t.Context(), dividends); err != nil {
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
		"id=\"dividend-register\"",
		"data-dividend-id=\"test-dividend\"",
		"USD 0.83",
		"<link rel=\"stylesheet\" href=\"/assets/filingweb.css\">",
		"<link rel=\"icon\" href=\"/assets/chopstick.svg\" type=\"image/svg+xml\">",
		"href=\"/?q=MSFT\"",
		"hx-get=\"/?q=MSFT\"",
		"hx-target=\"body\"",
		"data-testid=\"metric-RevenueFromContractWithCustomerExcludingAssessedTax-FY2026\"",
		"Revenue",
		"$331.8B",
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

	record = httptest.NewRecorder()
	request = httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/companies/789019", nil)
	server.ServeHTTP(record, request)

	if record.Code != http.StatusOK {
		t.Fatalf("company page status=%d body=%s", record.Code,
			record.Body.String())
	}

	for _, marker := range []string{
		"id=\"dividend-register\"", "$0.83/share", "$1.2B",
	} {
		if !strings.Contains(record.Body.String(), marker) {
			t.Errorf("company page is missing %q", marker)
		}
	}

	body = record.Body.String()
	for _, marker := range []string{
		"data-statement=\"Income statement\"",
		"data-statement=\"Balance sheet\"",
		"data-statement=\"Cash flow\"",
		"class=\"history-table\"",
		"--history-width:",
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("company page is missing %q", marker)
		}
	}
}

func TestIndexPageIncludesSearchBreadcrumb(t *testing.T) {
	t.Parallel()

	server := NewServer(t.TempDir(), nil)
	record := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)

	server.ServeHTTP(record, request)

	if record.Code != http.StatusOK {
		t.Fatalf("index status=%d body=%s", record.Code, record.Body.String())
	}

	body := record.Body.String()
	for _, marker := range []string{
		"id=\"search-breadcrumb\"",
		"aria-label=\"Search breadcrumb\"",
		"All filings",
		"hx-get=\"/\"",
		"hx-target=\"#dashboard\"",
		"<link rel=\"icon\" href=\"/assets/chopstick.svg\" type=\"image/svg+xml\">",
		"<link rel=\"stylesheet\" href=\"/assets/filingweb.css\">",
		"<script src=\"/assets/htmx.min.js\"></script>",
		"edgar.search-trail.v1",
		"const searchTrailLimit = 8",
		"function rememberSearch(query, form, year)",
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("index page is missing %q", marker)
		}
	}
}

func TestIndexPageWithoutFiltersDoesNotLoadFilings(t *testing.T) {
	t.Parallel()

	parsedDir := t.TempDir()

	brokenDir := filepath.Join(parsedDir, "0000789019", "0001193125-26-000001")
	if err := os.MkdirAll(brokenDir, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(brokenDir, "filing.json"),
		[]byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}

	server := NewServer(parsedDir, nil)
	record := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(
		context.Background(), http.MethodGet, "/", nil)

	server.ServeHTTP(record, request)

	if record.Code != http.StatusOK {
		t.Fatalf("index status=%d body=%s", record.Code, record.Body.String())
	}
}

func TestCompanyPageRealNVIDIASmoke(t *testing.T) {
	t.Parallel()

	parsedDir := filepath.Join("..", "..", "data", "parsed")
	if _, err := os.Stat(filepath.Join(parsedDir, "0001045810")); err != nil {
		t.Skip("local NVIDIA parsed data is not available")
	}

	if _, err := os.Stat(filepath.Join(filepath.Dir(parsedDir), "analysis.db")); err != nil {
		t.Skip("local NVIDIA analysis database is not available")
	}

	analysis, err := analysisstore.Open(context.Background(), filepath.Join(filepath.Dir(parsedDir), "analysis.db"))
	if err != nil {
		t.Skip("local NVIDIA analysis database cannot be opened")
	}

	views, err := analysis.ListFilingViews(t.Context())
	_ = analysis.Close()

	if err != nil {
		t.Skip("local NVIDIA analysis database cannot be read")
	}

	found := false

	for _, view := range views {
		if view.Metadata.CIK == "0001045810" {
			found = true

			break
		}
	}

	if !found {
		t.Skip("local NVIDIA derived view is not available")
	}

	server := NewServer(parsedDir, nil)
	record := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(
		context.Background(), http.MethodGet, "/companies/0001045810", nil)

	server.ServeHTTP(record, request)

	if record.Code != http.StatusOK {
		t.Fatalf("company status=%d body=%s", record.Code, record.Body.String())
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

func newCIKTestServer(t *testing.T) *Server {
	t.Helper()

	parsedDir := t.TempDir()

	filing := &edgar.ParsedFiling{
		SchemaVersion: edgar.ParsedSchemaVersion,
		ParserVersion: edgar.ParserVersion,
		Metadata: edgar.FilingMetadata{
			CIK: "789019", Accession: "0001193125-26-027207", FormType: "10-Q", FilingDate: "20260913",
			ReportDate: "20260912", Items: []string{}, Header: []string{},
			Filers: []edgar.SubmissionFiler{
				{CIK: "789019", Name: "Example Corp.", Header: []string{}},
			},
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

	view, err := filingview.Build(filing)
	if err != nil {
		t.Fatalf("filingview.Build returned error: %v", err)
	}

	analysis, err := analysisstore.Open(context.Background(), filepath.Join(filepath.Dir(parsedDir), "analysis.db"))
	if err != nil {
		t.Fatalf("analysisstore.Open returned error: %v", err)
	}

	if err := analysis.SaveFilingView(t.Context(), view); err != nil {
		t.Fatalf("SaveFilingView returned error: %v", err)
	}

	if err := analysis.Close(); err != nil {
		t.Fatalf("analysisstore.Close returned error: %v", err)
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

	return server
}

func TestServerFiltersByCIK(t *testing.T) {
	server := newCIKTestServer(t)
	for _, test := range []struct{ name, cik string }{{"unpadded", "789019"}, {"padded", "0000789019"}} {
		t.Run(test.name, func(t *testing.T) {
			requireResponse(t, requestServer(t, server, http.MethodGet, "/api/filings?cik="+test.cik), http.StatusOK, "0001193125-26-027207")
		})
	}
}

func TestServerFiltersByTicker(t *testing.T) {
	requireResponse(t, requestServer(t, newCIKTestServer(t), http.MethodGet, "/api/filings?q=MSFT"), http.StatusOK, `"ticker":"MSFT"`)
}

func TestServerRendersDetails(t *testing.T) {
	requireResponse(t, requestServer(t, newCIKTestServer(t), http.MethodGet, "/filings/789019/0001193125-26-027207"), http.StatusOK, "XBRL facts")
}

func TestServerRendersDashboard(t *testing.T) {
	record := requestServer(t, newCIKTestServer(t), http.MethodGet, "/?q=MSFT")
	requireResponse(t, record, http.StatusOK, "id=\"dashboard\"", "value=\"MSFT\"", "0001193125-26-027207")
}

func TestServerRendersHTMX(t *testing.T) {
	server := newCIKTestServer(t)
	for _, test := range []struct {
		name, target string
		fullPage     bool
	}{
		{"dashboard", "div#dashboard", false},
		{"body", "body", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := httptest.NewRecorder()
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/?q=MSFT", nil)
			request.Header.Set("Hx-Request", "true")
			request.Header.Set("Hx-Target", test.target)
			server.ServeHTTP(record, request)

			if test.fullPage {
				requireResponse(t, record, http.StatusOK, "<!doctype html>", "class=\"dashboard-page\"")

				return
			}

			if record.Code != http.StatusOK || strings.Contains(record.Body.String(), "<!doctype html>") {
				t.Fatalf("unexpected dashboard fragment: status=%d body=%s", record.Code, record.Body.String())
			}

			requireResponse(t, record, http.StatusOK, "id=\"dashboard\"", "0001193125-26-027207")
		})
	}
}

func TestServerServesAssets(t *testing.T) {
	server := newCIKTestServer(t)
	for _, test := range []struct{ path, contentType, marker string }{
		{"/assets/htmx.min.js", "text/javascript", `version="4.0.0"`},
		{"/assets/filingweb.css", "text/css", "body.detail-page"},
		{"/assets/chopstick.svg", "image/svg+xml", "<svg"},
	} {
		t.Run(test.path, func(t *testing.T) {
			record := requestServer(t, server, http.MethodGet, test.path)
			if record.Code != http.StatusOK ||
				!strings.Contains(record.Header().Get("Content-Type"), test.contentType) ||
				!strings.Contains(record.Body.String(), test.marker) {
				t.Fatalf("asset failed: status=%d headers=%v body=%s", record.Code, record.Header(), record.Body.String())
			}
		})
	}
}

// requestServer sends an HTTP request to the given server and returns the response recorder.
func requestServer(t *testing.T, server http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()

	record := httptest.NewRecorder()
	server.ServeHTTP(record, httptest.NewRequestWithContext(context.Background(), method, path, nil))

	return record
}

// requireResponse asserts that the HTTP response has the expected status code and contains the specified markers in the body.
func requireResponse(t *testing.T, record *httptest.ResponseRecorder, status int, markers ...string) {
	t.Helper()

	if record.Code != status {
		t.Fatalf("status=%d body=%s", record.Code, record.Body.String())
	}

	for _, marker := range markers {
		if !strings.Contains(record.Body.String(), marker) {
			t.Fatalf("body missing %q: %s", marker, record.Body.String())
		}
	}
}
