package filingstore

import (
	"os"
	"path/filepath"
	"testing"

	"wingman.com/fetch-ecb/internal/edgar"
	"wingman.com/fetch-ecb/internal/filingview"
)

func TestJSONDirectoryListsViewSummaries(t *testing.T) {
	t.Parallel()

	parsedDir := t.TempDir()
	store := NewJSONDirectory(parsedDir)

	saveParsedAndView(t, parsedDir, parsedFiling(
		"789019", "0001193125-26-000002", "10-Q", "20260913", "Beta Corp.", 2,
	))
	saveParsedAndView(t, parsedDir, parsedFiling(
		"789019", "0001193125-26-000001", "10-K", "20260914", "Alpha Corp.", 1,
	))

	summaries, err := store.ListSummaries(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if len(summaries) != 2 {
		t.Fatalf("len(summaries)=%d, want 2", len(summaries))
	}

	if summaries[0].Accession != "0001193125-26-000001" ||
		summaries[0].Company != "Alpha Corp." ||
		summaries[0].Facts != 1 ||
		summaries[0].Status != edgar.ParseComplete {
		t.Fatalf("first summary=%+v", summaries[0])
	}

	if summaries[1].Accession != "0001193125-26-000002" ||
		summaries[1].Company != "Beta Corp." ||
		summaries[1].Facts != 2 {
		t.Fatalf("second summary=%+v", summaries[1])
	}
}

func TestJSONDirectoryLoadView(t *testing.T) {
	t.Parallel()

	parsedDir := t.TempDir()
	store := NewJSONDirectory(parsedDir)
	filing := parsedFiling(
		"789019", "0001193125-26-000001", "10-K", "20260914", "Alpha Corp.", 1,
	)
	saveParsedAndView(t, parsedDir, filing)

	view, err := store.LoadView(t.Context(), "0000789019", filing.Metadata.Accession)
	if err != nil {
		t.Fatal(err)
	}

	if view.Metadata.Company != "Alpha Corp." ||
		view.Metadata.Accession != filing.Metadata.Accession {
		t.Fatalf("view metadata=%+v", view.Metadata)
	}
}

func TestJSONDirectoryLoadsCompanyHistoryFromViews(t *testing.T) {
	t.Parallel()

	parsedDir := t.TempDir()
	store := NewJSONDirectory(parsedDir)

	saveParsedAndView(t, parsedDir, parsedFiling(
		"789019", "0001193125-26-000001", "10-K", "20260914", "Alpha Corp.", 0,
	))
	saveParsedAndView(t, parsedDir, parsedFiling(
		"320193", "0001193125-26-000003", "10-K", "20260915", "Other Corp.", 0,
	))

	history, err := store.LoadCompanyHistory(t.Context(), "789019")
	if err != nil {
		t.Fatal(err)
	}

	if history.CIK != "789019" || history.Company != "Alpha Corp." ||
		len(history.Statements) != 3 {
		t.Fatalf("history=%+v", history)
	}
}

func TestJSONDirectoryLoadsFilingAndSourcePath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	parsedDir := filepath.Join(root, "parsed")
	filingsDir := filepath.Join(root, "filings")
	if err := os.MkdirAll(filingsDir, 0o750); err != nil {
		t.Fatal(err)
	}

	sourcePath := filepath.Join(filingsDir, "sample.txt")
	if err := os.WriteFile(sourcePath, []byte("sample"), 0o600); err != nil {
		t.Fatal(err)
	}

	store := NewJSONDirectory(parsedDir)
	filing := parsedFiling(
		"789019", "0001193125-26-000001", "10-K", "20260914", "Alpha Corp.", 0,
	)
	filing.SourceBase = "filings"
	filing.SourcePath = "sample.txt"
	saveParsedAndView(t, parsedDir, filing)

	loaded, err := store.LoadFiling(t.Context(), "0000789019", filing.Metadata.Accession)
	if err != nil {
		t.Fatal(err)
	}

	if loaded.Metadata.CIK != "789019" {
		t.Fatalf("loaded CIK=%q, want 789019", loaded.Metadata.CIK)
	}

	source, err := store.SourcePath(t.Context(), loaded)
	if err != nil {
		t.Fatal(err)
	}

	if source != sourcePath {
		t.Fatalf("source=%q, want %q", source, sourcePath)
	}
}

func parsedFiling(
	cik string,
	accession string,
	formType string,
	filingDate string,
	company string,
	facts int,
) *edgar.ParsedFiling {
	instance := edgar.XBRLInstance{}
	for i := 0; i < facts; i++ {
		instance.Facts = append(instance.Facts, edgar.Fact{})
	}

	return &edgar.ParsedFiling{
		SchemaVersion: edgar.ParsedSchemaVersion,
		ParserVersion: edgar.ParserVersion,
		SourcePath:    "",
		SourceBase:    "",
		SourceSHA256:  "",
		Metadata: edgar.FilingMetadata{
			Accession:  accession,
			CIK:        cik,
			FormType:   formType,
			FilingDate: filingDate,
			Filers: []edgar.SubmissionFiler{{
				CIK: cik, Name: company, Header: []string{},
			}},
			Items: []string{},
		},
		Documents:   []edgar.SubmissionDocument{},
		Instances:   []edgar.XBRLInstance{instance},
		Status:      edgar.ParseComplete,
		Diagnostics: []edgar.ParseDiagnostic{},
	}
}

func saveParsedAndView(t *testing.T, parsedDir string, filing *edgar.ParsedFiling) {
	t.Helper()

	if _, err := edgar.SaveParsedFiling(parsedDir, filing); err != nil {
		t.Fatal(err)
	}

	if _, err := filingview.Save(parsedDir, filing); err != nil {
		t.Fatal(err)
	}
}
