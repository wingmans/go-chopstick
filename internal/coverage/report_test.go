package coverage

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"wingman.com/fetch-ecb/internal/edgar"
	"wingman.com/fetch-ecb/internal/filingview"
)

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
			FormTypes: []string{"10-K"}, Year: 0, FromYear: 0, ToYear: 0,
		},
		SetName: "test", FromYear: 2024, ToYear: 2024,
		Taxonomy: filingview.Taxonomy{
			SchemaVersion: 1, TaxonomyVersion: "test",
			Metrics: []filingview.MetricDefinition{{
				Key: "revenue", Label: "Revenue", Statement: "income",
				CoverageTier: "",
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
					UnitRef: "",
				}},
			}, {
				Key: "eps_basic", Label: "Basic EPS", Concept: "EarningsPerShareBasic",
				Namespace: "", Unit: "USD/shares",
				Values: map[string]filingview.FactValue{"FY2024": {
					Value: "1.23", Nil: false, ContextRef: "", Decimals: "",
					UnitRef: "",
				}},
			}},
		}},
		Statements: filingview.Statements{
			Income: filingview.StatementView{Title: "", Groups: nil},
			Balance: filingview.StatementView{Title: "", Groups: []filingview.SummaryGroup{{
				Title: "Balance", Periods: []string{"2024-06-30"},
				Rows: []filingview.FactSeries{{
					Key: "liabilities_and_equity", Label: "Liabilities and equity",
					Concept: "LiabilitiesAndStockholdersEquity", Namespace: "", Unit: "USD",
					Values: map[string]filingview.FactValue{"2024-06-30": {
						Value: "20", Nil: false, ContextRef: "", Decimals: "",
						UnitRef: "",
					}},
				}},
			}}},
			CashFlow: filingview.StatementView{Title: "", Groups: nil},
		}, Ratios: nil, Quality: filingview.Quality{IdentityChecks: nil},
		Counts: filingview.Counts{
			Documents: 0, Instances: 0, Facts: 0, Contexts: 0,
			DimensionalFactsExcluded: 0, Status: "",
		},
		Diagnostics: nil,
	}
	taxonomy := filingview.Taxonomy{
		SchemaVersion: 1, TaxonomyVersion: "test",
		Metrics: []filingview.MetricDefinition{
			{Key: "revenue", Label: "Revenue", Statement: "income", CoverageTier: "", Concepts: nil},
			{Key: "net_income", Label: "Net income", Statement: "income", CoverageTier: "", Concepts: nil},
			{Key: "eps_diluted", Label: "Diluted EPS", Statement: "income", CoverageTier: "", Concepts: nil},
			{
				Key: "eps_basic", Label: "Basic EPS", Statement: "income",
				CoverageTier: "supplemental", Concepts: nil,
			},
			{
				Key: "gross_profit", Label: "Gross profit", Statement: "income",
				CoverageTier: "industry_sensitive", Concepts: nil,
			},
			{
				Key: "liabilities_and_equity", Label: "Liabilities and equity",
				Statement: "balance", CoverageTier: "supplemental", Concepts: nil,
			},
			{Key: "liabilities", Label: "Liabilities", Statement: "balance", CoverageTier: "", Concepts: nil},
		}, Ratios: nil,
	}

	metrics, missing, missingByTier, relatedEvidence := metricPresence(view, taxonomy)
	if metrics["revenue"] != "present" || metrics["net_income"] != "missing" ||
		metrics["eps_diluted"] != "missing" ||
		metrics["eps_basic"] != "present" ||
		metrics["gross_profit"] != "missing" ||
		metrics["liabilities_and_equity"] != "present" ||
		metrics["liabilities"] != "missing" {
		t.Fatalf("unexpected metric states: %+v", metrics)
	}

	if len(missing) != 4 || missing[0] != "eps_diluted" || missing[1] != "gross_profit" ||
		missing[2] != "liabilities" || missing[3] != "net_income" {
		t.Fatalf("unexpected missing metrics: %v", missing)
	}

	if got := missingByTier["core"]; len(got) != 3 ||
		got[0] != "eps_diluted" || got[1] != "liabilities" || got[2] != "net_income" {
		t.Fatalf("unexpected core missing metrics: %+v", missingByTier)
	}

	if got := missingByTier["industry_sensitive"]; len(got) != 1 || got[0] != "gross_profit" {
		t.Fatalf("unexpected industry-sensitive missing metrics: %+v", missingByTier)
	}

	if got := relatedEvidence["liabilities"]; len(got) != 1 || got[0] != "liabilities_and_equity" {
		t.Fatalf("unexpected related evidence: %+v", relatedEvidence)
	}

	if got := relatedEvidence["eps_diluted"]; len(got) != 1 || got[0] != "eps_basic" {
		t.Fatalf("unexpected EPS related evidence: %+v", relatedEvidence)
	}
}

func TestSourceDimensionalEvidenceReportsBasicEPSForMissingDilutedEPS(t *testing.T) {
	t.Parallel()

	filing := &edgar.ParsedFiling{
		SchemaVersion: 0, ParserVersion: "", SourcePath: "", SourceBase: "",
		SourceSHA256: "", Metadata: edgar.FilingMetadata{
			Accession: "", CIK: "", FormType: "", FilingDate: "", ReportDate: "",
			Filers: nil, Items: nil, Header: nil,
		}, Documents: nil,
		Instances: []edgar.XBRLInstance{{
			Root: emptyXMLNode(), DocumentIndex: 0, Document: "",
			Facts: []edgar.Fact{{
				Namespaces: nil,
				Concept: edgar.QName{
					Namespace: "http://fasb.org/us-gaap/2024-01-31",
					Local:     "EarningsPerShareBasic",
				},
				Value: "1.23", ContextRef: "class-b", UnitRef: "usd-shares",
				Decimals: "", Precision: "", Language: "", Nil: false, ID: "",
				Attributes: nil, Structured: nil,
			}},
			Contexts: []edgar.FactContext{{
				ID: "class-b", Entity: "", Scheme: "", Instant: "",
				StartDate: "2024-01-01", EndDate: "2024-12-31", Forever: false,
				Dimensions: []edgar.FactDimension{{
					Axis: edgar.QName{
						Namespace: "http://fasb.org/us-gaap/2024-01-31",
						Local:     "StatementClassOfStockAxis",
					},
					Member: &edgar.QName{Namespace: "urn:test", Local: "ClassBMember"},
					Typed:  nil,
				}},
				Source: emptyXMLNode(),
			}},
			Units: nil, References: nil, Footnotes: nil, Unsupported: nil,
		}},
		Status: "", Diagnostics: nil,
	}

	evidence := sourceDimensionalEvidence(filing, []string{"eps_diluted"})
	if got := evidence["eps_diluted"]; len(got) != 1 || got[0] != "eps_basic" {
		t.Fatalf("unexpected dimensional evidence: %+v", evidence)
	}
}

func emptyXMLNode() edgar.XMLNode {
	return edgar.XMLNode{
		Name:       edgar.QName{Namespace: "", Local: ""},
		Language:   "",
		Attributes: nil,
		Namespaces: nil,
		Content:    nil,
	}
}
