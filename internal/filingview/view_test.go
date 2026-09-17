package filingview

import (
	"testing"

	"wingman.com/fetch-ecb/internal/edgar"
)

const testUSGAAPNamespace = "http://fasb.org/us-gaap/2024-01-31"

func TestBuildSelectsDeterministicDuplicateFact(t *testing.T) {
	filing := &edgar.ParsedFiling{
		SchemaVersion: edgar.ParsedSchemaVersion,
		ParserVersion: "test",
		SourcePath:    "",
		SourceBase:    "",
		SourceSHA256:  "",
		Metadata: edgar.FilingMetadata{
			Accession:  "0000000000-26-000001",
			CIK:        "0000789019",
			FormType:   "10-K",
			FilingDate: "2026-07-30",
			ReportDate: "2026-06-30",
			Filers:     nil,
			Items:      nil,
			Header:     nil,
		},
		Documents: nil,
		Instances: []edgar.XBRLInstance{{
			Root:          emptyXMLNode(),
			DocumentIndex: 0,
			Document:      "",
			Facts: []edgar.Fact{
				{
					Namespaces: nil,
					Concept: edgar.QName{
						Namespace: testUSGAAPNamespace,
						Local:     "RevenueFromContractWithCustomerExcludingAssessedTax",
					},
					Value: "100", ContextRef: "fy", UnitRef: "usd", Decimals: "-6", ID: "f1",
					Precision: "", Language: "", Nil: false, Attributes: nil, Structured: nil,
				},
				{
					Namespaces: nil,
					Concept: edgar.QName{
						Namespace: testUSGAAPNamespace,
						Local:     "Revenues",
					},
					Value: "999", ContextRef: "fy", UnitRef: "usd", Decimals: "-6", ID: "f2",
					Precision: "", Language: "", Nil: false, Attributes: nil, Structured: nil,
				},
				{
					Namespaces: nil,
					Concept: edgar.QName{
						Namespace: testUSGAAPNamespace,
						Local:     "RevenueFromContractWithCustomerExcludingAssessedTax",
					},
					Value: "25", ContextRef: "segment", UnitRef: "usd", Decimals: "-6", ID: "f3",
					Precision: "", Language: "", Nil: false, Attributes: nil, Structured: nil,
				},
			},
			Contexts: []edgar.FactContext{
				{
					ID: "fy", Entity: "", Scheme: "", Instant: "",
					StartDate: "2025-07-01", EndDate: "2026-06-30",
					Forever: false, Dimensions: nil, Source: emptyXMLNode(),
				},
				{
					ID: "segment", Entity: "", Scheme: "", Instant: "",
					StartDate: "2025-07-01", EndDate: "2026-06-30",
					Forever: false,
					Dimensions: []edgar.FactDimension{{
						Axis: edgar.QName{Namespace: "urn:test", Local: "SegmentAxis"},
						Member: &edgar.QName{
							Namespace: "urn:test", Local: "CloudMember",
						},
						Typed: nil,
					}},
					Source: emptyXMLNode(),
				},
			},
			Units: []edgar.FactUnit{{
				ID: "usd",
				Measures: []edgar.QName{{
					Namespace: "http://www.xbrl.org/2003/iso4217", Local: "USD",
				}},
				Numerator: nil, Denominator: nil, Source: emptyXMLNode(),
			}},
			References:  nil,
			Footnotes:   nil,
			Unsupported: nil,
		}},
		Status:      edgar.ParseComplete,
		Diagnostics: nil,
	}

	view, err := Build(filing)
	if err != nil {
		t.Fatal(err)
	}

	if view.Counts.DimensionalFactsExcluded != 1 {
		t.Fatalf("dimensional fact count = %d, want 1", view.Counts.DimensionalFactsExcluded)
	}

	summaryRow := onlyRow(t, view.Summary, "Fiscal year")
	assertRevenueSelection(t, summaryRow)

	statementRow := onlyRow(t, view.Statements.Income.Groups, "Fiscal year")
	assertRevenueSelection(t, statementRow)
}

func TestBuildSelectsDuplicateFactIndependentOfEncounterOrder(t *testing.T) {
	first := FactValue{
		Value: "", Nil: false, ContextRef: "", Decimals: "", UnitRef: "",
		priority: 0, concept: edgar.QName{Namespace: "", Local: ""},
		id: "", precision: "",
	}
	second := selectedFact{Fact: edgar.Fact{
		Namespaces: nil,
		Concept:    edgar.QName{Namespace: testUSGAAPNamespace, Local: "Revenues"},
		Value:      "nil-loses", ContextRef: "c2", UnitRef: "usd", Nil: true, ID: "b",
		Decimals: "", Precision: "", Language: "", Attributes: nil, Structured: nil,
	}, Priority: 1}
	third := selectedFact{Fact: edgar.Fact{
		Namespaces: nil,
		Concept:    edgar.QName{Namespace: testUSGAAPNamespace, Local: "Revenues"},
		Value:      "exact-wins", ContextRef: "c1", UnitRef: "usd", Decimals: "INF", ID: "a",
		Precision: "", Language: "", Nil: false, Attributes: nil, Structured: nil,
	}, Priority: 1}

	selected, concept := selectFact(first, edgar.QName{Namespace: "", Local: ""}, second)
	selected, _ = selectFact(selected, concept, third)

	if selected.Value != "exact-wins" || selected.ContextRef != "c1" {
		t.Fatalf("unexpected selected fact: %+v", selected)
	}
}

func onlyRow(t *testing.T, groups []SummaryGroup, title string) FactSeries {
	t.Helper()

	for _, group := range groups {
		if group.Title != title {
			continue
		}

		if len(group.Rows) != 1 {
			t.Fatalf("expected one row in %s, got %+v", title, group.Rows)
		}

		return group.Rows[0]
	}

	t.Fatalf("missing group %q in %+v", title, groups)

	return FactSeries{
		Key: "", Label: "", Namespace: "", Concept: "", Unit: "", Values: nil,
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

func assertRevenueSelection(t *testing.T, row FactSeries) {
	t.Helper()

	if row.Key != "revenue" || row.Concept != "RevenueFromContractWithCustomerExcludingAssessedTax" {
		t.Fatalf("unexpected selected row identity: %+v", row)
	}

	if row.Unit != "USD" {
		t.Fatalf("unexpected normalized unit: %q", row.Unit)
	}

	value := row.Values["FY2026"]
	if value.Value != "100" || value.ContextRef != "fy" || value.UnitRef != "usd" {
		t.Fatalf("unexpected selected value: %+v", value)
	}
}
