package coverage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

type expectedCoverageFixture struct {
	Cases []expectedCoverageCase `json:"cases"`
}

type expectedCoverageCase struct {
	Name                string              `json:"name"`
	CIK                 string              `json:"cik"`
	Company             string              `json:"company"`
	Accession           string              `json:"accession"`
	DateFiled           string              `json:"date_filed"`
	PresentMetrics      []string            `json:"present_metrics"`
	MissingCore         []string            `json:"missing_core"`
	DimensionalEvidence map[string][]string `json:"dimensional_evidence"`
}

func TestGoldenExpectedCoverageFixture(t *testing.T) {
	t.Parallel()

	fixture := loadExpectedCoverageFixture(t)
	root := t.TempDir()
	master := filepath.Join(root, "master.tsv")

	var masterRows strings.Builder

	for _, testCase := range fixture.Cases {
		filingPath := "edgar/data/" + testCase.CIK + "/" + testCase.Accession + ".txt"
		masterRows.WriteString(strings.Join([]string{
			testCase.CIK,
			testCase.Company,
			"10-K",
			testCase.DateFiled,
			filingPath,
			strings.TrimSuffix(filingPath, ".txt") + "-index.html",
		}, "|"))
		masterRows.WriteByte('\n')

		writeSourceFiling(t, root, filingPath)
		writeParsedFixture(t, root, testCase)
	}

	if err := os.WriteFile(master, []byte(masterRows.String()), 0o600); err != nil {
		t.Fatalf("write master: %v", err)
	}

	report, err := Build(t.Context(), Config{
		MasterPath: master, IndexesDir: filepath.Join(root, "indexes"),
		FilingsDir: filepath.Join(root, "filings"), ParsedDir: filepath.Join(root, "parsed"),
		Filter: edgar.IndexFilter{
			CIK: "", CIKs: nil, FormTypes: []string{"10-K"},
			Year: 0, FromYear: 0, ToYear: 0,
		},
		SetName: "golden-expected-coverage", FromYear: 2018, ToYear: 2026,
		Taxonomy:    expectedCoverageTaxonomy(),
		GeneratedAt: time.Date(2026, time.September, 17, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	if report.Summary.Expected != len(fixture.Cases) ||
		report.Summary.Parsed != len(fixture.Cases) ||
		report.Summary.Missing != 0 ||
		report.Summary.Unparsed != 0 {
		t.Fatalf("unexpected summary: %+v", report.Summary)
	}

	byAccession := make(map[string]Filing, len(report.Filings))
	for _, filing := range report.Filings {
		byAccession[filing.Accession] = filing
	}

	for _, testCase := range fixture.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			t.Parallel()

			filing, ok := byAccession[testCase.Accession]
			if !ok {
				t.Fatalf("missing report row for %s", testCase.Accession)
			}

			if filing.Status != "parsed" {
				t.Fatalf("status=%s, want parsed", filing.Status)
			}

			gotMissingCore := stringSliceValue(filing.MissingByTier["core"])
			if !reflect.DeepEqual(gotMissingCore, testCase.MissingCore) {
				t.Fatalf("core missing=%v, want %v", gotMissingCore, testCase.MissingCore)
			}

			gotDimensionalEvidence := stringSliceMapValue(filing.MissingDimensionalEvidence)
			if !reflect.DeepEqual(gotDimensionalEvidence, testCase.DimensionalEvidence) {
				t.Fatalf("dimensional evidence=%v, want %v",
					gotDimensionalEvidence, testCase.DimensionalEvidence)
			}

			for _, metric := range testCase.PresentMetrics {
				if filing.Metrics[metric] != "present" {
					t.Fatalf("%s state=%q, want present", metric, filing.Metrics[metric])
				}
			}
		})
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

func loadExpectedCoverageFixture(t *testing.T) expectedCoverageFixture {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", "golden-expected-coverage.json"))
	if err != nil {
		t.Fatalf("read expected coverage fixture: %v", err)
	}

	var fixture expectedCoverageFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode expected coverage fixture: %v", err)
	}

	return fixture
}

func stringSliceValue(value []string) []string {
	if value == nil {
		return []string{}
	}

	return value
}

func stringSliceMapValue(value map[string][]string) map[string][]string {
	if value == nil {
		return map[string][]string{}
	}

	return value
}

func writeSourceFiling(t *testing.T, root, filingPath string) {
	t.Helper()

	localPath, err := edgar.LocalFilingPath(filepath.Join(root, "filings"), filingPath)
	if err != nil {
		t.Fatalf("local filing path: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(localPath), 0o750); err != nil {
		t.Fatalf("create source directory: %v", err)
	}

	if err := os.WriteFile(localPath, []byte("fixture filing"), 0o600); err != nil {
		t.Fatalf("write source filing: %v", err)
	}
}

func writeParsedFixture(t *testing.T, root string, testCase expectedCoverageCase) {
	t.Helper()

	filing := parsedCoverageFixture(testCase)
	if _, err := edgar.SaveParsedFiling(filepath.Join(root, "parsed"), filing); err != nil {
		t.Fatalf("save parsed filing: %v", err)
	}

	viewPath := parsedViewPath(filepath.Join(root, "parsed"), testCase.CIK, testCase.Accession)
	if err := os.MkdirAll(filepath.Dir(viewPath), 0o750); err != nil {
		t.Fatalf("create filing view directory: %v", err)
	}

	viewData, err := json.Marshal(filingViewFixture(testCase))
	if err != nil {
		t.Fatalf("encode filing view: %v", err)
	}

	if err := os.WriteFile(viewPath, viewData, 0o600); err != nil {
		t.Fatalf("write filing view: %v", err)
	}
}

func parsedCoverageFixture(testCase expectedCoverageCase) *edgar.ParsedFiling {
	return &edgar.ParsedFiling{
		SchemaVersion: edgar.ParsedSchemaVersion, ParserVersion: edgar.ParserVersion,
		SourcePath: "", SourceBase: "", SourceSHA256: "",
		Metadata: edgar.FilingMetadata{
			Accession: testCase.Accession, CIK: testCase.CIK,
			FormType: "10-K", FilingDate: testCase.DateFiled, ReportDate: "",
			Filers: nil, Items: nil, Header: nil,
		},
		Documents: nil,
		Instances: dimensionalEPSInstances(testCase),
		Status:    edgar.ParseComplete, Diagnostics: nil,
	}
}

func dimensionalEPSInstances(testCase expectedCoverageCase) []edgar.XBRLInstance {
	if _, ok := testCase.DimensionalEvidence["eps_diluted"]; !ok {
		return nil
	}

	return []edgar.XBRLInstance{{
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
	}}
}

func filingViewFixture(testCase expectedCoverageCase) filingview.View {
	rows := make([]filingview.FactSeries, 0, len(testCase.PresentMetrics))
	for _, metric := range testCase.PresentMetrics {
		rows = append(rows, filingViewMetricRow(metric))
	}

	return filingview.View{
		SchemaVersion: filingview.SchemaVersion, ParserVersion: edgar.ParserVersion,
		TaxonomyVersion: "test", SourcePath: "", SourceSHA256: "",
		Metadata: filingview.Metadata{
			Accession: testCase.Accession, CIK: testCase.CIK,
			FormType: "10-K", FilingDate: testCase.DateFiled,
			ReportDate: "", Company: testCase.Company,
		},
		Documents: nil,
		Summary: []filingview.SummaryGroup{{
			Title: "Fixture", Periods: []string{"FY2024"}, Rows: rows,
		}},
		Statements: filingview.Statements{
			Income:   filingview.StatementView{Title: "", Groups: nil},
			Balance:  filingview.StatementView{Title: "", Groups: nil},
			CashFlow: filingview.StatementView{Title: "", Groups: nil},
		},
		Ratios: nil, Quality: filingview.Quality{IdentityChecks: nil},
		Counts: filingview.Counts{
			Documents: 0, Instances: 0, Facts: len(rows), Contexts: 0,
			DimensionalFactsExcluded: 0, Status: edgar.ParseComplete,
		},
		Diagnostics: nil,
	}
}

func filingViewMetricRow(metric string) filingview.FactSeries {
	return filingview.FactSeries{
		Key: metric, Label: metric, Namespace: "http://fasb.org/us-gaap/2024-01-31",
		Concept: metric, Unit: "USD",
		Values: map[string]filingview.FactValue{
			"FY2024": {
				Value: "1", Nil: false, ContextRef: "fixture",
				Decimals: "", UnitRef: "usd",
			},
		},
	}
}

func expectedCoverageTaxonomy() filingview.Taxonomy {
	return filingview.Taxonomy{
		SchemaVersion: 1, TaxonomyVersion: "test",
		Metrics: []filingview.MetricDefinition{
			{
				Key: "revenue", Label: "Revenue", Statement: "income",
				CoverageTier: "", Concepts: []filingview.ConceptReference{{
					NamespaceFamily: "us-gaap", Name: "Revenue", Priority: 1,
				}},
			},
			{
				Key: "cash", Label: "Cash", Statement: "balance",
				CoverageTier: "", Concepts: []filingview.ConceptReference{{
					NamespaceFamily: "us-gaap", Name: "Cash", Priority: 1,
				}},
			},
			{
				Key: "investing_cash_flow", Label: "Investing cash flow",
				Statement: "cash_flow", CoverageTier: "",
				Concepts: []filingview.ConceptReference{{
					NamespaceFamily: "us-gaap", Name: "InvestingCashFlow", Priority: 1,
				}},
			},
			{
				Key: "eps_diluted", Label: "Diluted EPS", Statement: "income",
				CoverageTier: "", Concepts: []filingview.ConceptReference{{
					NamespaceFamily: "us-gaap", Name: "EarningsPerShareDiluted", Priority: 1,
				}},
			},
			{
				Key: "eps_basic", Label: "Basic EPS", Statement: "income",
				CoverageTier: "supplemental", Concepts: []filingview.ConceptReference{{
					NamespaceFamily: "us-gaap", Name: "EarningsPerShareBasic", Priority: 1,
				}},
			},
		},
		Ratios: nil,
	}
}

func TestSourceDimensionalEvidenceReportsBasicEPSForMissingDilutedEPS(t *testing.T) {
	t.Parallel()

	filing := parsedCoverageFixture(expectedCoverageCase{
		Name: "", CIK: "1067983", Company: "", Accession: "0000950170-24-019719",
		DateFiled: "2024-02-26", PresentMetrics: nil, MissingCore: nil,
		DimensionalEvidence: map[string][]string{"eps_diluted": {"eps_basic"}},
	})

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
