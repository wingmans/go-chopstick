package filingview

import (
	"testing"

	"wingman.com/fetch-ecb/internal/edgar"
)

func TestBuildCompanyHistorySelectsNewestAnnualFact(t *testing.T) {
	views := []View{
		historyTestView("789019", "0000000001", "2025-08-01", "2024", "100"),
		historyTestView("789019", "0000000002", "2026-08-01", "2024", "110"),
		historyTestView("789019", "0000000003", "2025-08-01", "2023", "90"),
	}

	history, err := BuildCompanyHistory(views, "0000789019")
	if err != nil {
		t.Fatal(err)
	}

	if got, want := history.Years, []string{"2024", "2023"}; !equalStrings(got, want) {
		t.Fatalf("years = %v, want %v", got, want)
	}

	if len(history.Statements) != 1 || len(history.Statements[0].Rows) != 1 {
		t.Fatalf("unexpected statements: %+v", history.Statements)
	}

	row := history.Statements[0].Rows[0]
	if row.Label != "Revenue" || row.Values["2024"].Value != "110" {
		t.Fatalf("unexpected 2024 history row: %+v", row)
	}

	if row.Sources["2024"] != "0000000002" {
		t.Fatalf("unexpected source: %+v", row.Sources)
	}
}

func historyTestView(cik, accession, filingDate, year, value string) View {
	return View{
		SchemaVersion: SchemaVersion, ParserVersion: "test", TaxonomyVersion: "test",
		SourcePath: "", SourceSHA256: "", Metadata: Metadata{
			Accession: accession, CIK: cik, FormType: "10-K", FilingDate: filingDate,
			ReportDate: year + "-06-30", Company: "Microsoft Corporation",
		}, Documents: []edgar.SubmissionDocument{}, Summary: []SummaryGroup{},
		Statements: Statements{
			Income: StatementView{Title: "Income statement", Groups: []SummaryGroup{{
				Title: "Fiscal year", Periods: []string{"FY" + year}, Rows: []FactSeries{{
					Key: "revenue", Label: "Revenue", Namespace: "us-gaap",
					Concept: "Revenues", Unit: "usd", Values: map[string]FactValue{
						"FY" + year: {Value: value, Nil: false, ContextRef: "c1", Decimals: "-3"},
					},
				}},
			}}},
			Balance:  StatementView{Title: "Balance sheet", Groups: []SummaryGroup{}},
			CashFlow: StatementView{Title: "Cash flow", Groups: []SummaryGroup{}},
		}, Ratios: []RatioSeries{}, Counts: Counts{
			Documents: 0, Instances: 0, Facts: 1, Contexts: 1, Status: "complete",
		}, Diagnostics: []edgar.ParseDiagnostic{},
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}

	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}

	return true
}
