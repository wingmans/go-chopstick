package filingview

import (
	"math/big"
	"sort"
	"strings"

	"wingman.com/fetch-ecb/internal/edgar"
)

type statementAccumulator struct {
	Rows map[string]map[string]*FactSeries
}

func buildStatements(filing *edgar.ParsedFiling) (Statements, []RatioSeries) {
	taxonomy := defaultTaxonomy()
	accumulators := map[string]*statementAccumulator{
		"income":    {Rows: map[string]map[string]*FactSeries{}},
		"balance":   {Rows: map[string]map[string]*FactSeries{}},
		"cash_flow": {Rows: map[string]map[string]*FactSeries{}},
	}

	for _, instance := range filing.Instances {
		contexts := make(map[string]edgar.FactContext, len(instance.Contexts))
		for _, context := range instance.Contexts {
			if len(context.Dimensions) == 0 {
				contexts[context.ID] = context
			}
		}

		for _, fact := range instance.Facts {
			definition, reference, ok := taxonomy.metricReferenceForConcept(fact.Concept.Namespace, fact.Concept.Local)
			if !ok {
				continue
			}

			context, ok := contexts[fact.ContextRef]
			if !ok {
				continue
			}

			group, periodKey, periodLabel := classifyPeriod(context, filing.Metadata.ReportDate)
			if !statementPeriod(definition.Statement, group) {
				continue
			}

			accumulator := accumulators[definition.Statement]
			if accumulator.Rows[group] == nil {
				accumulator.Rows[group] = map[string]*FactSeries{}
			}

			key := definition.Key + "\x00" + fact.UnitRef

			row := accumulator.Rows[group][key]
			if row == nil {
				row = &FactSeries{
					Key: definition.Key, Label: definition.Label,
					Namespace: fact.Concept.Namespace, Concept: fact.Concept.Local,
					Unit: fact.UnitRef, Values: map[string]FactValue{},
				}
				accumulator.Rows[group][key] = row
			}

			selectedValue, selectedConcept := selectFact(row.Values[periodKey],
				edgar.QName{Namespace: row.Namespace, Local: row.Concept},
				selectedFact{Fact: fact, Priority: reference.Priority})
			row.Values[periodKey] = selectedValue
			row.Namespace = selectedConcept.Namespace
			row.Concept = selectedConcept.Local
			accumulator.Rows[group][key] = row
			_ = periodLabel
		}
	}

	statements := Statements{
		Income:   StatementView{Title: "Income statement", Groups: statementGroups(accumulators["income"])},
		Balance:  StatementView{Title: "Balance sheet", Groups: statementGroups(accumulators["balance"])},
		CashFlow: StatementView{Title: "Cash flow", Groups: statementGroups(accumulators["cash_flow"])},
	}

	return statements, buildRatios(accumulators, taxonomy)
}

func statementPeriod(statement, group string) bool {
	if statement == "balance" {
		return group == "Instant"
	}

	return group == "Fiscal year" || group == "Quarterly" || group == "Year to date"
}

func statementGroups(accumulator *statementAccumulator) []SummaryGroup {
	order := []string{"Fiscal year", "Quarterly", "Year to date", "Instant"}

	groups := make([]SummaryGroup, 0, len(accumulator.Rows))
	for _, title := range order {
		rowsByKey, ok := accumulator.Rows[title]
		if !ok {
			continue
		}

		periods := map[string]struct{}{}

		rows := make([]FactSeries, 0, len(rowsByKey))
		for _, row := range rowsByKey {
			rows = append(rows, *row)
			for period := range row.Values {
				periods[period] = struct{}{}
			}
		}

		periodKeys := make([]string, 0, len(periods))
		for period := range periods {
			periodKeys = append(periodKeys, period)
		}

		sort.Sort(sort.Reverse(sort.StringSlice(periodKeys)))
		sort.Slice(rows, func(i, j int) bool { return rows[i].Label < rows[j].Label })
		groups = append(groups, SummaryGroup{Title: title, Periods: periodKeys, Rows: rows})
	}

	return groups
}

func buildRatios(accumulators map[string]*statementAccumulator, taxonomy Taxonomy) []RatioSeries {
	result := make([]RatioSeries, 0, len(taxonomy.ratioDefinitions()))
	for _, definition := range taxonomy.ratioDefinitions() {
		statement := "income"
		if definition.Key == "current_ratio" || definition.Key == "debt_to_equity" {
			statement = "balance"
		}

		values := ratioValues(accumulators[statement], definition)
		if len(values) == 0 {
			continue
		}

		result = append(result, RatioSeries{
			Key: definition.Key, Label: definition.Label,
			Derived: true, Formula: definition.Formula, Values: values,
		})
	}

	return result
}

func ratioValues(accumulator *statementAccumulator, definition RatioDefinition) map[string]string {
	result := map[string]string{}

	for group, rows := range accumulator.Rows {
		for key, numerator := range rows {
			if !strings.HasPrefix(key, definition.Numerator+"\x00") {
				continue
			}

			unit := strings.TrimPrefix(key, definition.Numerator+"\x00")

			denominator := rows[definition.Denominator+"\x00"+unit]
			if denominator == nil {
				continue
			}

			for period, numeratorValue := range numerator.Values {
				denominatorValue, ok := denominator.Values[period]
				if !ok || numeratorValue.Nil || denominatorValue.Nil {
					continue
				}

				ratio, ok := ratioString(numeratorValue.Value,
					denominatorValue.Value, definition.Format)
				if ok {
					result[group+":"+period] = ratio
				}
			}
		}
	}

	return result
}

func ratioString(numerator, denominator, format string) (string, bool) {
	numerator = strings.ReplaceAll(strings.TrimSpace(numerator), ",", "")
	denominator = strings.ReplaceAll(strings.TrimSpace(denominator), ",", "")

	n, ok := new(big.Rat).SetString(numerator)
	if !ok {
		return "", false
	}

	d, ok := new(big.Rat).SetString(denominator)
	if !ok || d.Sign() == 0 {
		return "", false
	}

	ratio := new(big.Rat).Quo(n, d)
	if format == "percent" {
		ratio.Mul(ratio, big.NewRat(100, 1))

		return ratio.FloatString(2) + "%", true
	}

	return ratio.FloatString(2) + "x", true
}
