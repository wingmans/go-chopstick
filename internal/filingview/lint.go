//nolint:wsl_v5 // The report builder keeps the scan and aggregation together.
package filingview

import (
	"fmt"
	"sort"
	"strings"
)

type LintTerm struct {
	Namespace string `json:"namespace"`
	Concept   string `json:"concept"`
	Count     int    `json:"count"`
}

type LintReport struct {
	TaxonomyVersion string     `json:"taxonomy_version"`
	Rows            int        `json:"rows"`
	Mapped          int        `json:"mapped"`
	Unmapped        []LintTerm `json:"unmapped"`
}

// LintView checks the compact model against the taxonomy. Unmapped terms are
// findings, not errors: a filing can contain valid facts outside our model.
func LintView(view View, taxonomy Taxonomy) LintReport {
	counts := map[string]*LintTerm{}
	rows := 0
	mapped := 0
	seenRows := make(map[string]struct{})
	visit := func(groups []SummaryGroup) {
		for _, group := range groups {
			for _, row := range group.Rows {
				rowKey := group.Title + "\x00" + row.Namespace + "\x00" +
					row.Concept + "\x00" + row.Unit
				if _, seen := seenRows[rowKey]; seen {
					continue
				}
				seenRows[rowKey] = struct{}{}

				rows++
				_, known := taxonomy.metricForConcept(row.Namespace, row.Concept)
				if row.Key != "" || known {
					mapped++

					continue
				}

				key := row.Namespace + "\x00" + row.Concept
				term := counts[key]
				if term == nil {
					term = &LintTerm{
						Namespace: row.Namespace, Concept: row.Concept, Count: 0,
					}
					counts[key] = term
				}
				term.Count++
			}
		}
	}

	visit(view.Summary)
	visit(view.Statements.Income.Groups)
	visit(view.Statements.Balance.Groups)
	visit(view.Statements.CashFlow.Groups)
	result := make([]LintTerm, 0, len(counts))
	for _, term := range counts {
		result = append(result, *term)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Concept != result[j].Concept {
			return result[i].Concept < result[j].Concept
		}

		return result[i].Namespace < result[j].Namespace
	})

	return LintReport{
		TaxonomyVersion: taxonomy.TaxonomyVersion,
		Rows:            rows, Mapped: mapped,
		Unmapped: result,
	}
}

func (r LintReport) String() string {
	var result strings.Builder
	fmt.Fprintf(&result, "taxonomy=%s rows=%d mapped=%d unmapped=%d",
		r.TaxonomyVersion, r.Rows, r.Mapped, len(r.Unmapped))
	for _, term := range r.Unmapped {
		fmt.Fprintf(&result, "\n  %s %s count=%d", term.Namespace, term.Concept, term.Count)
	}

	return result.String()
}
