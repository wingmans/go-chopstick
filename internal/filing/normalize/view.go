// Package normalize builds filing read models from parsed SEC filings.
package normalize

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"wingman.com/chopstick/internal/filing"
	"wingman.com/chopstick/internal/filing/quality"
	"wingman.com/chopstick/internal/parser"
)

// Build constructs the persisted filing read model from parsed source data.
func Build(source *parser.ParsedFiling) (filing.View, error) {
	if source == nil {
		return filing.View{}, errors.New("parsed filing is nil")
	}

	taxonomy, err := filing.LoadTaxonomy("")
	if err != nil {
		return filing.View{}, fmt.Errorf("load filing taxonomy: %w", err)
	}

	company := ""
	if len(source.Metadata.Filers) > 0 {
		company = source.Metadata.Filers[0].Name
	}

	facts := 0
	contexts := 0
	dimensionalFactsExcluded := 0

	for _, instance := range source.Instances {
		facts += len(instance.Facts)
		contexts += len(instance.Contexts)
		dimensionalFactsExcluded += countDimensionalFacts(instance)
	}

	statements, ratios := buildStatements(source, taxonomy)
	view := filing.View{
		SchemaVersion:   filing.SchemaVersion,
		ParserVersion:   source.ParserVersion,
		TaxonomyVersion: taxonomy.TaxonomyVersion,
		SourcePath:      source.SourcePath,
		SourceSHA256:    source.SourceSHA256,
		Metadata: filing.Metadata{
			Accession: source.Metadata.Accession,
			CIK:       source.Metadata.CIK, FormType: source.Metadata.FormType,
			FilingDate: source.Metadata.FilingDate,
			ReportDate: source.Metadata.ReportDate, Company: company,
		},
		Documents:  source.Documents,
		Summary:    financialSummary(source, taxonomy),
		Statements: statements, Ratios: ratios,
		Quality: filing.Quality{IdentityChecks: nil},
		Counts: filing.Counts{
			Documents: len(source.Documents), Instances: len(source.Instances),
			Facts: facts, Contexts: contexts,
			DimensionalFactsExcluded: dimensionalFactsExcluded,
			Status:                   source.Status,
		},
		Diagnostics: source.Diagnostics,
	}
	view.Quality.IdentityChecks = quality.CheckIdentities(view)

	return view, nil
}

func countDimensionalFacts(instance parser.XBRLInstance) int {
	contexts := make(map[string]parser.FactContext, len(instance.Contexts))
	for _, context := range instance.Contexts {
		contexts[context.ID] = context
	}

	count := 0

	for _, fact := range instance.Facts {
		context, ok := contexts[fact.ContextRef]
		if ok && len(context.Dimensions) > 0 {
			count++
		}
	}

	return count
}

func financialSummary(source *parser.ParsedFiling,
	taxonomy filing.Taxonomy,
) []filing.SummaryGroup {
	periodsByGroup := map[string]map[string]string{}
	seriesByGroup := map[string]map[string]*filing.FactSeries{}
	selections := map[*filing.FactSeries]map[string]selectedFact{}

	for _, instance := range source.Instances {
		units := unitMap(instance)

		contexts := make(map[string]parser.FactContext, len(instance.Contexts))
		for _, context := range instance.Contexts {
			if len(context.Dimensions) == 0 {
				contexts[context.ID] = context
			}
		}

		for _, fact := range instance.Facts {
			definition, reference, ok := taxonomy.MetricReference(
				fact.Concept.Namespace, fact.Concept.Local)
			if !ok {
				continue
			}

			context, ok := contexts[fact.ContextRef]
			if !ok {
				continue
			}

			group, periodKey, periodLabel := classifyPeriod(
				context, source.Metadata.ReportDate)
			if group == "" {
				continue
			}

			unit := normalizedUnit(fact, units)
			key := definition.Key + "\x00" + unit

			if periodsByGroup[group] == nil {
				periodsByGroup[group] = map[string]string{}
				seriesByGroup[group] = map[string]*filing.FactSeries{}
			}

			row := seriesByGroup[group][key]
			if row == nil {
				row = &filing.FactSeries{
					Key: definition.Key, Label: definition.Label,
					Namespace: fact.Concept.Namespace,
					Concept:   fact.Concept.Local, Unit: unit,
					Values: map[string]filing.FactValue{},
				}
				seriesByGroup[group][key] = row
			}

			periodsByGroup[group][periodKey] = periodLabel
			selectInto(selections, row, periodKey,
				selectedFact{Fact: fact, Priority: reference.Priority})
		}
	}

	return summaryGroups(periodsByGroup, seriesByGroup)
}

func summaryGroups(periodsByGroup map[string]map[string]string,
	seriesByGroup map[string]map[string]*filing.FactSeries,
) []filing.SummaryGroup {
	groups := make([]filing.SummaryGroup, 0, len(periodsByGroup))
	for _, title := range []string{
		"Fiscal year", "Quarterly", "Year to date", "Instant",
	} {
		periods, ok := periodsByGroup[title]
		if !ok {
			continue
		}

		periodKeys := make([]string, 0, len(periods))
		for key := range periods {
			periodKeys = append(periodKeys, key)
		}

		sort.Sort(sort.Reverse(sort.StringSlice(periodKeys)))

		rows := make([]filing.FactSeries, 0, len(seriesByGroup[title]))
		for _, row := range seriesByGroup[title] {
			rows = append(rows, *row)
		}

		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Label != rows[j].Label {
				return rows[i].Label < rows[j].Label
			}

			return rows[i].Unit < rows[j].Unit
		})

		groups = append(groups, filing.SummaryGroup{
			Title: title, Periods: periodKeys, Rows: rows,
		})
	}

	return groups
}

func classifyPeriod(context parser.FactContext,
	reportDate string,
) (string, string, string) {
	if context.Instant != "" {
		return "Instant", displayDate(context.Instant),
			displayDate(context.Instant)
	}

	start, startErr := parseDate(context.StartDate)

	end, endErr := parseDate(context.EndDate)
	if startErr != nil || endErr != nil || start.After(end) {
		return "", "", ""
	}

	fiscalEnd, err := parseDate(reportDate)
	if err != nil {
		fiscalEnd = end
	}

	months := (end.Year()-start.Year())*12 +
		int(end.Month()) - int(start.Month()) + 1

	fiscalYear := end.Year()
	if fiscalEnd.Month() != time.December && end.Month() > fiscalEnd.Month() {
		fiscalYear++
	}

	if months >= 10 {
		label := fmt.Sprintf("FY%d", fiscalYear)

		return "Fiscal year", label, label
	}

	if months == 3 {
		fiscalStartMonth := fiscalEnd.Month()%12 + 1
		quarter := (int(start.Month())-int(fiscalStartMonth)+12)%12/3 + 1
		label := fmt.Sprintf("Q%d FY%d", quarter, fiscalYear)

		return "Quarterly", label, label
	}

	if months > 3 {
		label := fmt.Sprintf("YTD FY%d", fiscalYear)

		return "Year to date", label, label
	}

	return "", "", ""
}

func displayDate(value string) string {
	date, err := parseDate(value)
	if err != nil {
		return value
	}

	return date.Format("2006-01-02")
}

func parseDate(value string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02", "20060102"} {
		if date, err := time.Parse(layout, value); err == nil {
			return date, nil
		}
	}

	return time.Time{}, fmt.Errorf("invalid date %q", value)
}
