package quality

import (
	"testing"

	"wingman.com/chopstick/internal/filing"
)

func TestLintReportsOnlyUnmappedTerms(t *testing.T) {
	taxonomy, err := filing.LoadTaxonomy("")
	if err != nil {
		t.Fatalf("filing.LoadTaxonomy returned error: %v", err)
	}

	view := filing.View{
		SchemaVersion: 1, ParserVersion: "test", TaxonomyVersion: "test",
		SourcePath: "", SourceSHA256: "", Metadata: filing.Metadata{
			Accession: "", CIK: "", FormType: "", FilingDate: "",
			ReportDate: "", Company: "",
		},
		Documents: nil, Statements: filing.Statements{
			Income:   filing.StatementView{Title: "", Groups: nil},
			Balance:  filing.StatementView{Title: "", Groups: nil},
			CashFlow: filing.StatementView{Title: "", Groups: nil},
		}, Ratios: nil,
		Quality: filing.Quality{IdentityChecks: nil},
		Counts: filing.Counts{
			Documents: 0, Instances: 0, Facts: 0, Contexts: 0,
			DimensionalFactsExcluded: 0, Status: "",
		}, Diagnostics: nil,
		Summary: []filing.SummaryGroup{{
			Title: "Fiscal year", Periods: nil, Rows: []filing.FactSeries{
				{Key: "revenue", Label: "Revenue", Namespace: "http://fasb.org/us-gaap/2024-01-31", Concept: "Revenues", Unit: "USD", Values: nil},
				{Key: "", Label: "", Namespace: "http://example.test/company/2026", Concept: "AdjustedFoo", Unit: "USD", Values: nil},
			},
		}},
	}

	report := Lint(view, taxonomy)
	if report.Rows != 2 || report.Mapped != 1 || len(report.Unmapped) != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}

	if report.Unmapped[0].Concept != "AdjustedFoo" {
		t.Fatalf("unexpected unmapped term: %+v", report.Unmapped[0])
	}
}
