package coverage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"wingman.com/fetch-ecb/internal/edgar"
	"wingman.com/fetch-ecb/internal/filingview"
)

//nolint:wsl_v5 // Test setup is kept close to the report assertions.
func TestBuildReportsMissingDataAndAcquisitionRequests(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	master := filepath.Join(root, "master.tsv")
	contents := "789019|Microsoft Corporation|10-K|2024-07-30|" +
		"edgar/data/789019/0001193125-24-123456.txt|" +
		"edgar/data/789019/0001193125-24-123456-index.html\n"
	if err := os.WriteFile(master, []byte(contents), 0o600); err != nil {
		t.Fatalf("write master: %v", err)
	}

	report, err := Build(t.Context(), Config{
		MasterPath: master, IndexesDir: filepath.Join(root, "indexes"),
		FilingsDir: filepath.Join(root, "filings"), ParsedDir: filepath.Join(root, "parsed"),
		Filter: edgar.IndexFilter{
			CIK: "789019", CIKs: nil,
			FormTypes: []string{"10-K"}, Year: 0,
		},
		SetName: "test", FromYear: 2024, ToYear: 2024,
		Taxonomy: filingview.Taxonomy{
			SchemaVersion: 1, TaxonomyVersion: "test",
			Metrics: []filingview.MetricDefinition{{
				Key: "revenue", Label: "Revenue", Statement: "income",
				Concepts: []filingview.ConceptReference{{
					NamespaceFamily: "us-gaap", Name: "Revenue", Priority: 1,
				}},
			}}, Ratios: nil,
		},
		GeneratedAt: time.Date(2026, time.September, 16, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	if report.Summary.Expected != 1 || report.Summary.Missing != 1 {
		t.Fatalf("unexpected summary: %+v", report.Summary)
	}
	if len(report.Filings) != 1 || report.Filings[0].Status != "missing" {
		t.Fatalf("unexpected filing report: %+v", report.Filings)
	}
	if len(report.Acquisition) != 2 {
		t.Fatalf("got %d acquisition requests, want index and filing", len(report.Acquisition))
	}
	if report.Acquisition[0].Kind != "filings" || report.Acquisition[1].Kind != "index" {
		t.Fatalf("unexpected acquisition order: %+v", report.Acquisition)
	}
}

//nolint:wsl_v5 // Test setup is kept close to the metric assertions.
func TestMetricPresenceReportsMappedAndMissingMetrics(t *testing.T) {
	t.Parallel()
	view := filingview.View{
		SchemaVersion: 0, ParserVersion: "", TaxonomyVersion: "", SourcePath: "",
		SourceSHA256: "", Metadata: filingview.Metadata{
			Accession: "", CIK: "", FormType: "", FilingDate: "", ReportDate: "",
			Company: "",
		}, Documents: nil,
		Summary: []filingview.SummaryGroup{{
			Title: "Income", Periods: []string{"FY2024"},
			Rows: []filingview.FactSeries{{
				Key: "revenue", Label: "Revenue", Concept: "Revenue",
				Namespace: "", Unit: "USD",
				Values: map[string]filingview.FactValue{"FY2024": {
					Value: "10", Nil: false, ContextRef: "", Decimals: "",
				}},
			}},
		}},
		Statements: filingview.Statements{
			Income:   filingview.StatementView{Title: "", Groups: nil},
			Balance:  filingview.StatementView{Title: "", Groups: nil},
			CashFlow: filingview.StatementView{Title: "", Groups: nil},
		}, Ratios: nil, Counts: filingview.Counts{
			Documents: 0, Instances: 0, Facts: 0, Contexts: 0, Status: "",
		},
		Diagnostics: nil,
	}
	taxonomy := filingview.Taxonomy{
		SchemaVersion: 1, TaxonomyVersion: "test",
		Metrics: []filingview.MetricDefinition{
			{Key: "revenue", Label: "Revenue", Statement: "income", Concepts: nil},
			{Key: "net_income", Label: "Net income", Statement: "income", Concepts: nil},
		}, Ratios: nil,
	}

	metrics, missing := metricPresence(view, taxonomy)
	if metrics["revenue"] != "present" || metrics["net_income"] != "missing" {
		t.Fatalf("unexpected metric states: %+v", metrics)
	}
	if len(missing) != 1 || missing[0] != "net_income" {
		t.Fatalf("unexpected missing metrics: %v", missing)
	}
}
