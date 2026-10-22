package filing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wingman.com/fetch-ecb/internal/edgar"
)

func TestGolden10Q2026ParsedCorpus(t *testing.T) {
	var fixture struct {
		Companies map[string]string `json:"companies"`
		Concepts  []string          `json:"concepts"`
	}

	data, err := os.ReadFile(filepath.Join("..", "..", "golden", "golden-10-Q-2026-results.json"))
	if err != nil {
		t.Fatal(err)
	}

	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}

	for ticker, cik := range fixture.Companies {
		t.Run(ticker, func(t *testing.T) {
			paths, err := filepath.Glob(filepath.Join("..", "..", "golden", "10q-2026", "*.txt"))
			if err != nil {
				t.Fatal(err)
			}

			if len(paths) == 0 {
				t.Fatal("no parsed filing views found")
			}

			matched := 0

			for _, path := range paths {
				filing, err := edgar.ParseSubmission(t.Context(), path)
				if err != nil {
					t.Fatal(err)
				}

				if filing.Metadata.CIK != cik {
					continue
				}

				view, err := Build(filing)
				if err != nil {
					t.Fatal(err)
				}

				if view.Metadata.FormType != "10-Q" ||
					(!strings.HasPrefix(view.Metadata.ReportDate, "2025") && !strings.HasPrefix(view.Metadata.ReportDate, "2026")) {
					continue
				}

				matched++

				for _, concept := range fixture.Concepts {
					if concept == "gross_profit" && (ticker == "COST" || ticker == "WMT") {
						continue
					}

					if concept == "liabilities" && ticker == "WMT" {
						continue
					}

					if !viewHasConcept(view, concept) {
						t.Errorf("%s %s: missing concept %q", view.Metadata.Accession, view.Metadata.ReportDate, concept)
					}
				}
			}

			if matched == 0 {
				t.Fatal("no fiscal-2026 10-Q views found")
			}
		})
	}
}

func TestGolden10Q2026SubmissionPath(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "golden",
		"golden-10-Q-2026-results.json"))
	if err != nil {
		t.Fatal(err)
	}

	var fixture struct {
		Companies map[string]string `json:"companies"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}

	paths, err := filepath.Glob(filepath.Join("..", "..", "golden",
		"10q-2026", "*.txt"))
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]int{}

	for _, path := range paths {
		filing, err := edgar.ParseSubmission(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}

		if !known10QCik(fixture.Companies, filing.Metadata.CIK) {
			t.Fatalf("%s: unexpected CIK %q", path, filing.Metadata.CIK)
		}

		if filing.Metadata.FormType != "10-Q" {
			t.Fatalf("%s: form type = %q, want 10-Q", path,
				filing.Metadata.FormType)
		}

		view, err := Build(filing)
		if err != nil {
			t.Fatalf("%s: build view: %v", path, err)
		}

		if view.Metadata.FormType != "10-Q" || view.Metadata.CIK != filing.Metadata.CIK {
			t.Fatalf("%s: invalid view metadata: %+v", path, view.Metadata)
		}

		if !hasSummaryGroup(view.Summary, "Instant") {
			t.Fatalf("%s: 10-Q view has no Instant summary", path)
		}

		seen[filing.Metadata.CIK]++
	}

	for ticker, cik := range fixture.Companies {
		if seen[cik] == 0 {
			t.Errorf("%s (%s): no parsed 10-Q submissions", ticker, cik)
		}
	}
}

func known10QCik(companies map[string]string, cik string) bool {
	for _, known := range companies {
		if known == cik {
			return true
		}
	}

	return false
}

func viewHasConcept(view View, key string) bool {
	if key == "cash_and_cash_equivalents" {
		key = "cash"
	}

	for _, group := range view.Summary {
		for _, row := range group.Rows {
			if row.Key == key && len(row.Values) > 0 {
				return true
			}
		}
	}

	return false
}
