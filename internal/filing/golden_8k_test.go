package filing

import (
	"path/filepath"
	"testing"

	"wingman.com/fetch-ecb/internal/edgar"
)

func TestGolden8KBuildsCurrentView(t *testing.T) {
	path := filepath.Join("..", "..", "golden", "formtypes",
		"sample_8-K.txt")

	filing, err := edgar.ParseSubmission(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	if filing.Status != edgar.ParseComplete {
		t.Fatalf("status = %q, want %q", filing.Status, edgar.ParseComplete)
	}

	if filing.Metadata.Accession != "0001193125-26-191457" ||
		filing.Metadata.CIK != "0000789019" ||
		filing.Metadata.FormType != "8-K" ||
		filing.Metadata.FilingDate != "20260429" ||
		filing.Metadata.ReportDate != "20260429" {
		t.Fatalf("unexpected metadata: %+v", filing.Metadata)
	}

	if len(filing.Documents) != 10 || len(filing.Instances) != 1 {
		t.Fatalf("documents=%d instances=%d", len(filing.Documents),
			len(filing.Instances))
	}

	instance := filing.Instances[0]
	if len(instance.Facts) != 28 || len(instance.Contexts) != 4 ||
		len(instance.Units) != 0 {
		t.Fatalf("facts=%d contexts=%d units=%d", len(instance.Facts),
			len(instance.Contexts), len(instance.Units))
	}

	view, err := Build(filing)
	if err != nil {
		t.Fatal(err)
	}

	if view.Metadata.Accession != filing.Metadata.Accession ||
		view.Metadata.CIK != filing.Metadata.CIK ||
		view.Metadata.FormType != filing.Metadata.FormType ||
		view.Metadata.FilingDate != filing.Metadata.FilingDate ||
		view.Metadata.ReportDate != filing.Metadata.ReportDate ||
		view.Metadata.Company != "MICROSOFT CORP" {
		t.Fatalf("unexpected view metadata: %+v", view.Metadata)
	}

	if view.Counts.Documents != 10 || view.Counts.Instances != 1 ||
		view.Counts.Facts != 28 || view.Counts.Contexts != 4 ||
		view.Counts.Status != edgar.ParseComplete {
		t.Fatalf("unexpected view counts: %+v", view.Counts)
	}

	if len(view.Documents) != len(filing.Documents) {
		t.Fatalf("view documents=%d, filing documents=%d", len(view.Documents),
			len(filing.Documents))
	}
}
