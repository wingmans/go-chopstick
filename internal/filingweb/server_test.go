package filingweb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"wingman.com/fetch-ecb/internal/edgar"
)

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

	server := NewServer(parsedDir, nil)

	for _, cik := range []string{"789019", "0000789019"} {
		record := httptest.NewRecorder()
		request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/filings?cik="+cik, nil)
		server.ServeHTTP(record, request)

		if record.Code != 200 || !strings.Contains(record.Body.String(), "0001193125-26-027207") {
			t.Fatalf("CIK %s did not return the filing: status=%d body=%s", cik, record.Code, record.Body.String())
		}
	}

	record := httptest.NewRecorder()
	server.ServeHTTP(record, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/filings/789019/0001193125-26-027207", nil))

	if record.Code != 200 || !strings.Contains(record.Body.String(), "XBRL facts") {
		t.Fatalf("details page failed: status=%d body=%s", record.Code, record.Body.String())
	}
}
