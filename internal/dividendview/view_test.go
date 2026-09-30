package dividendview

import (
	"os"
	"path/filepath"
	"testing"

	"wingman.com/fetch-ecb/internal/edgar"
)

func TestRebuildExtractsAnnouncementAndEvidence(t *testing.T) {
	root := t.TempDir()
	source := []byte("Microsoft declared a quarterly dividend of $0.83 per share. " +
		"The ex-dividend date is April 10, 2026. The record date is April 11, " +
		"2026. The dividend is payable on April 30, 2026.")

	sourcePath := filepath.Join(root, "announcement.txt")
	if err := os.WriteFile(sourcePath, source, 0o600); err != nil {
		t.Fatal(err)
	}

	filing := &edgar.ParsedFiling{
		SchemaVersion: edgar.ParsedSchemaVersion,
		ParserVersion: edgar.ParserVersion,
		SourceBase:    "absolute",
		SourcePath:    sourcePath,
		Metadata: edgar.FilingMetadata{
			Accession:  "0000789019-26-000001",
			CIK:        "789019",
			FormType:   "8-K",
			FilingDate: "20260401",
			Filers:     []edgar.SubmissionFiler{{Name: "MICROSOFT CORP"}},
		},
		Documents: []edgar.SubmissionDocument{{
			Filename: "release.htm", ContentOffset: 0,
			ContentLength: int64(len(source)),
		}},
	}
	if _, err := edgar.SaveParsedFiling(root, filing); err != nil {
		t.Fatal(err)
	}

	path, err := Rebuild(t.Context(), root, "", filing.Metadata.CIK)
	if err != nil {
		t.Fatal(err)
	}

	view, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(view.Events) != 1 {
		t.Fatalf("events=%d, want 1", len(view.Events))
	}

	event := view.Events[0]
	if event.AmountPerShare != "0.83" || event.DeclarationDate != "20260401" ||
		event.ExDate != "April 10, 2026" || event.RecordDate != "April 11, 2026" ||
		event.PayableDate != "April 30, 2026" {
		t.Fatalf("unexpected event: %+v", event)
	}

	if len(event.Sources) != 1 || event.Sources[0].Evidence == "" {
		t.Fatalf("event source evidence missing: %+v", event.Sources)
	}
}

func TestRebuildExtractsGolden10QReconciliation(t *testing.T) {
	path := filepath.Join("..", "..", "golden", "formtypes",
		"sample_10-Q.txt")

	filing, err := edgar.ParseSubmission(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()

	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}

	filing.SourceBase, filing.SourcePath = "absolute", absolute
	if _, err := edgar.SaveParsedFiling(root, filing); err != nil {
		t.Fatal(err)
	}

	viewPath, err := Rebuild(t.Context(), root, "", filing.Metadata.CIK)
	if err != nil {
		t.Fatal(err)
	}

	view, err := Load(viewPath)
	if err != nil {
		t.Fatal(err)
	}

	if len(view.Observations) == 0 {
		t.Fatal("golden 10-Q produced no dividend observations")
	}

	for _, observation := range view.Observations {
		if observation.FormType != "10-Q" || observation.Accession != filing.Metadata.Accession {
			t.Fatalf("unexpected observation provenance: %+v", observation)
		}
	}
}

func TestRebuildSkipsUnavailableAnnouncementSource(t *testing.T) {
	root := t.TempDir()

	filing := &edgar.ParsedFiling{
		SchemaVersion: edgar.ParsedSchemaVersion,
		ParserVersion: edgar.ParserVersion,
		SourceBase:    "absolute",
		SourcePath:    filepath.Join(root, "missing.txt"),
		Metadata: edgar.FilingMetadata{
			Accession:  "0000789019-26-000002",
			CIK:        "789019",
			FormType:   "8-K",
			FilingDate: "20260402",
		},
	}
	if _, err := edgar.SaveParsedFiling(root, filing); err != nil {
		t.Fatal(err)
	}

	if _, err := Rebuild(t.Context(), root, "", filing.Metadata.CIK); err != nil {
		t.Fatalf("rebuild returned error for unavailable source: %v", err)
	}
}
