package filingview

import (
	"fmt"
	"sort"
	"strings"

	"wingman.com/fetch-ecb/internal/edgar"
)

const coverageTextLimit = 25

type CoverageReport struct {
	TaxonomyVersion string     `json:"taxonomy_version"`
	Facts           int        `json:"facts"`
	Mapped          int        `json:"mapped"`
	Unmapped        []LintTerm `json:"unmapped"`
	Extensions      []LintTerm `json:"extensions"`
	Dimensional     int        `json:"dimensional"`
	MissingContext  int        `json:"missing_context"`
	InvalidPeriods  int        `json:"invalid_periods"`
	MissingUnits    int        `json:"missing_units"`
	DuplicateFacts  int        `json:"duplicate_facts"`
}

// Coverage inspects source-level facts before compact projection. It reports
// findings without changing the parsed filing or taxonomy.
func Coverage(filing *edgar.ParsedFiling, taxonomy Taxonomy) CoverageReport {
	report := CoverageReport{
		TaxonomyVersion: taxonomy.TaxonomyVersion,
		Facts:           0, Mapped: 0, Unmapped: nil, Extensions: nil,
		Dimensional: 0, MissingContext: 0, InvalidPeriods: 0,
		MissingUnits: 0, DuplicateFacts: 0,
	}
	unmapped := map[string]*LintTerm{}
	extensions := map[string]*LintTerm{}
	seen := map[string]struct{}{}

	for _, instance := range filing.Instances {
		contexts := make(map[string]edgar.FactContext, len(instance.Contexts))
		for _, context := range instance.Contexts {
			contexts[context.ID] = context
		}

		for _, fact := range instance.Facts {
			report.Facts++

			_, mapped := taxonomy.metricForConcept(
				fact.Concept.Namespace, fact.Concept.Local)
			if mapped {
				report.Mapped++
				if !fact.Nil && fact.UnitRef == "" {
					report.MissingUnits++
				}
			} else {
				addLintTerm(unmapped, fact.Concept.Namespace, fact.Concept.Local)

				if companyExtension(fact.Concept.Namespace) {
					addLintTerm(extensions, fact.Concept.Namespace, fact.Concept.Local)
				}
			}

			context, ok := contexts[fact.ContextRef]
			if !ok {
				report.MissingContext++
			} else {
				if len(context.Dimensions) > 0 {
					report.Dimensional++
				}

				if !validContextPeriod(context) {
					report.InvalidPeriods++
				}
			}

			key := fact.Concept.Namespace + "\x00" + fact.Concept.Local +
				"\x00" + fact.ContextRef + "\x00" + fact.UnitRef
			if _, exists := seen[key]; exists {
				report.DuplicateFacts++
			} else {
				seen[key] = struct{}{}
			}
		}
	}

	report.Unmapped = sortedLintTerms(unmapped)
	report.Extensions = sortedLintTerms(extensions)

	return report
}

func addLintTerm(terms map[string]*LintTerm, namespace, concept string) {
	key := namespace + "\x00" + concept

	term := terms[key]
	if term == nil {
		term = &LintTerm{Namespace: namespace, Concept: concept, Count: 0}
		terms[key] = term
	}

	term.Count++
}

func sortedLintTerms(terms map[string]*LintTerm) []LintTerm {
	result := make([]LintTerm, 0, len(terms))
	for _, term := range terms {
		result = append(result, *term)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Concept != result[j].Concept {
			return result[i].Concept < result[j].Concept
		}

		return result[i].Namespace < result[j].Namespace
	})

	return result
}

func validContextPeriod(context edgar.FactContext) bool {
	if context.Instant != "" {
		_, err := parseDate(context.Instant)

		return err == nil
	}

	if context.StartDate == "" || context.EndDate == "" {
		return false
	}

	start, startErr := parseDate(context.StartDate)
	end, endErr := parseDate(context.EndDate)

	return startErr == nil && endErr == nil && !start.After(end)
}

func companyExtension(namespace string) bool {
	for _, standard := range []string{
		"/us-gaap/", "/us-gaap:", "xbrl.sec.gov/dei", "xbrl.org/",
		"www.xbrl.org/",
	} {
		if strings.Contains(namespace, standard) {
			return false
		}
	}

	return namespace != ""
}

func (r CoverageReport) String() string {
	var result strings.Builder
	fmt.Fprintf(&result,
		"taxonomy=%s facts=%d mapped=%d unmapped=%d extensions=%d dimensional=%d missing_context=%d invalid_periods=%d missing_units=%d duplicate_facts=%d",
		r.TaxonomyVersion, r.Facts, r.Mapped, len(r.Unmapped),
		len(r.Extensions), r.Dimensional, r.MissingContext,
		r.InvalidPeriods, r.MissingUnits, r.DuplicateFacts,
	)

	for _, term := range topCoverageTerms(r.Unmapped, coverageTextLimit) {
		fmt.Fprintf(&result, "\n  unmapped %s %s count=%d", term.Namespace,
			term.Concept, term.Count)
	}

	if omitted := len(r.Unmapped) - minInt(len(r.Unmapped), coverageTextLimit); omitted > 0 {
		fmt.Fprintf(&result, "\n  unmapped ... omitted=%d", omitted)
	}

	for _, term := range topCoverageTerms(r.Extensions, coverageTextLimit) {
		fmt.Fprintf(&result, "\n  extension %s %s count=%d", term.Namespace,
			term.Concept, term.Count)
	}

	if omitted := len(r.Extensions) - minInt(len(r.Extensions), coverageTextLimit); omitted > 0 {
		fmt.Fprintf(&result, "\n  extension ... omitted=%d", omitted)
	}

	return result.String()
}

func topCoverageTerms(terms []LintTerm, limit int) []LintTerm {
	if limit <= 0 || len(terms) == 0 {
		return nil
	}

	result := append([]LintTerm(nil), terms...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}

		if result[i].Concept != result[j].Concept {
			return result[i].Concept < result[j].Concept
		}

		return result[i].Namespace < result[j].Namespace
	})

	if len(result) > limit {
		result = result[:limit]
	}

	return result
}

func minInt(left, right int) int {
	if left < right {
		return left
	}

	return right
}
