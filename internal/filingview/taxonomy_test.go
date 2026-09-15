//nolint:wsl_v5 // Tests keep related assertions together.
package filingview

import "testing"

func TestLoadTaxonomyDefault(t *testing.T) {
	taxonomy, err := LoadTaxonomy("")
	if err != nil {
		t.Fatalf("LoadTaxonomy returned error: %v", err)
	}
	if taxonomy.TaxonomyVersion == "" || len(taxonomy.Metrics) == 0 {
		t.Fatalf("default taxonomy is incomplete: %+v", taxonomy)
	}
	if _, ok := taxonomy.metricForConcept(
		"http://fasb.org/us-gaap/2013-01-31", "Revenues"); !ok {
		t.Fatal("expected versioned US-GAAP revenue concept to resolve")
	}
	if _, ok := taxonomy.metricForConcept(
		"http://example.test/company/2026", "Revenues"); ok {
		t.Fatal("company extension must not resolve as US-GAAP")
	}
}

func TestLintViewReportsOnlyUnmappedTerms(t *testing.T) {
	taxonomy, err := LoadTaxonomy("")
	if err != nil {
		t.Fatalf("LoadTaxonomy returned error: %v", err)
	}
	view := View{
		SchemaVersion: 1, ParserVersion: "test", TaxonomyVersion: "test",
		SourcePath: "", SourceSHA256: "", Metadata: Metadata{
			Accession: "", CIK: "", FormType: "", FilingDate: "",
			ReportDate: "", Company: "",
		},
		Documents: nil, Statements: Statements{
			Income:   StatementView{Title: "", Groups: nil},
			Balance:  StatementView{Title: "", Groups: nil},
			CashFlow: StatementView{Title: "", Groups: nil},
		}, Ratios: nil,
		Counts: Counts{
			Documents: 0, Instances: 0, Facts: 0, Contexts: 0, Status: "",
		}, Diagnostics: nil,
		Summary: []SummaryGroup{{
			Title: "Fiscal year", Periods: nil, Rows: []FactSeries{
				{Key: "revenue", Label: "Revenue", Namespace: "http://fasb.org/us-gaap/2024-01-31", Concept: "Revenues", Unit: "USD", Values: nil},
				{Key: "", Label: "", Namespace: "http://example.test/company/2026", Concept: "AdjustedFoo", Unit: "USD", Values: nil},
			},
		}},
	}

	report := LintView(view, taxonomy)
	if report.Rows != 2 || report.Mapped != 1 || len(report.Unmapped) != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.Unmapped[0].Concept != "AdjustedFoo" {
		t.Fatalf("unexpected unmapped term: %+v", report.Unmapped[0])
	}
}
