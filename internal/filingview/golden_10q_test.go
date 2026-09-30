package filingview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"wingman.com/fetch-ecb/internal/edgar"
)

func TestGolden10QBuildsQuarterlyView(t *testing.T) {
	path := filepath.Join("..", "..", "golden", "formtypes", "sample_10-Q.txt")

	filing, err := edgar.ParseSubmission(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	view, err := Build(filing)
	if err != nil {
		t.Fatal(err)
	}

	if view.Metadata.FormType != "10-Q" {
		t.Fatalf("form type = %q", view.Metadata.FormType)
	}

	if view.Metadata.CIK != "0000789019" {
		t.Fatalf("CIK = %q", view.Metadata.CIK)
	}

	if !hasSummaryGroup(view.Summary, "Quarterly") {
		t.Fatal("10-Q view has no Quarterly summary group")
	}

	if !hasSummaryGroup(view.Summary, "Year to date") {
		t.Fatal("10-Q view has no Year to date summary group")
	}

	if !hasSummaryGroup(view.Summary, "Instant") {
		t.Fatal("10-Q view has no Instant summary group")
	}

	resultsPath := filepath.Join("..", "..", "golden", "golden-10-Q-results.json")

	contents, err := os.ReadFile(resultsPath)
	if err != nil {
		t.Fatal(err)
	}

	var expected golden10QResults
	if err := json.Unmarshal(contents, &expected); err != nil {
		t.Fatal(err)
	}

	assertGolden10QResults(t, view, expected)
}

type golden10QResults struct {
	Accession string                                  `json:"accession"`
	FormType  string                                  `json:"form_type"`
	Company   string                                  `json:"company"`
	Groups    map[string]map[string]map[string]string `json:"groups"`
}

func assertGolden10QResults(t *testing.T, view View, expected golden10QResults) {
	t.Helper()

	if view.Metadata.Accession != expected.Accession || view.Metadata.FormType != expected.FormType || view.Metadata.Company != expected.Company {
		t.Fatalf("metadata mismatch: got=%+v expected=%+v", view.Metadata, expected)
	}

	for groupTitle, rows := range expected.Groups {
		group := findSummaryGroup(view.Summary, groupTitle)
		for rowKey, values := range rows {
			row := findFactRow(t, group.Rows, rowKey)
			for period, want := range values {
				value, ok := row.Values[period]
				if !ok || value.Value != want {
					t.Fatalf("%s/%s/%s = %q, want %q", groupTitle, rowKey, period, value.Value, want)
				}
			}
		}
	}
}

func findSummaryGroup(groups []SummaryGroup, title string) SummaryGroup {
	for _, group := range groups {
		if group.Title == title {
			return group
		}
	}

	return SummaryGroup{}
}

func findFactRow(t *testing.T, rows []FactSeries, key string) FactSeries {
	t.Helper()

	for _, row := range rows {
		if row.Key == key {
			return row
		}
	}

	t.Fatalf("missing fact row %q", key)

	return FactSeries{}
}

func hasSummaryGroup(groups []SummaryGroup, title string) bool {
	for _, group := range groups {
		if group.Title == title {
			return true
		}
	}

	return false
}
