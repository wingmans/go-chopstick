package normalize

import (
	"math/big"
	"sort"
	"strings"

	"wingman.com/chopstick/internal/filing"
	"wingman.com/chopstick/internal/parser"
)

type statementAccumulator struct {
	Rows       map[string]map[string]*filing.FactSeries
	Selections map[*filing.FactSeries]map[string]selectedFact
}

// buildStatements constructs the financial statements and ratio series from a parsed filing.
func buildStatements(source *parser.ParsedFiling, taxonomy filing.Taxonomy) (
	filing.Statements, []filing.RatioSeries,
) {
	accumulators := map[string]*statementAccumulator{
		"income": {
			Rows:       map[string]map[string]*filing.FactSeries{},
			Selections: map[*filing.FactSeries]map[string]selectedFact{},
		},
		"balance": {
			Rows:       map[string]map[string]*filing.FactSeries{},
			Selections: map[*filing.FactSeries]map[string]selectedFact{},
		},
		"cash_flow": {
			Rows:       map[string]map[string]*filing.FactSeries{},
			Selections: map[*filing.FactSeries]map[string]selectedFact{},
		},
	}

	for _, instance := range source.Instances {
		units := unitMap(instance)

		contexts := make(map[string]parser.FactContext, len(instance.Contexts))
		for _, context := range instance.Contexts {
			if len(context.Dimensions) == 0 {
				contexts[context.ID] = context
			}
		}

		for _, fact := range instance.Facts {
			definition, reference, ok := taxonomy.MetricReference(fact.Concept.Namespace, fact.Concept.Local)
			if !ok {
				continue
			}

			context, ok := contexts[fact.ContextRef]
			if !ok {
				continue
			}

			group, periodKey, periodLabel := classifyPeriod(
				context, source.Metadata.ReportDate)
			if !statementPeriod(definition.Statement, group) {
				continue
			}

			accumulator := accumulators[definition.Statement]
			if accumulator.Rows[group] == nil {
				accumulator.Rows[group] = map[string]*filing.FactSeries{}
			}

			unit := normalizedUnit(fact, units)
			key := definition.Key + "\x00" + unit

			row := accumulator.Rows[group][key]
			if row == nil {
				row = &filing.FactSeries{
					Key: definition.Key, Label: definition.Label,
					Namespace: fact.Concept.Namespace, Concept: fact.Concept.Local,
					Unit: unit, Values: map[string]filing.FactValue{},
				}
				accumulator.Rows[group][key] = row
			}

			selectInto(accumulator.Selections, row, periodKey,
				selectedFact{Fact: fact, Priority: reference.Priority})
			accumulator.Rows[group][key] = row
			_ = periodLabel
		}
	}

	// Some issuers report the balance-sheet total and equity but omit a
	// standalone Liabilities fact. Derive that value only from selected facts
	// in the same instant context and normalized unit. This keeps the source
	// facts untouched and avoids treating liabilities_and_equity as liabilities.
	deriveMissingLiabilities(accumulators["balance"])
	fallbackMissingEquity(accumulators["balance"])

	statements := filing.Statements{
		Income:   filing.StatementView{Title: "Income statement", Groups: statementGroups(accumulators["income"])},
		Balance:  filing.StatementView{Title: "Balance sheet", Groups: statementGroups(accumulators["balance"])},
		CashFlow: filing.StatementView{Title: "Cash flow", Groups: statementGroups(accumulators["cash_flow"])},
	}

	return statements, buildRatios(accumulators, taxonomy)
}

// fallbackMissingEquity exposes NCI-inclusive equity as the main equity row
// only when an issuer does not report plain stockholders' equity. The original
// NCI-inclusive row remains available, and the derived namespace makes the
// semantic fallback visible to downstream readers.
func fallbackMissingEquity(accumulator *statementAccumulator) {
	rows := accumulator.Rows["Instant"]
	if rows == nil {
		return
	}

	for nciKey, nci := range rows {
		if !strings.HasPrefix(nciKey, "equity_including_noncontrolling_interest\x00") {
			continue
		}

		unit := strings.TrimPrefix(nciKey, "equity_including_noncontrolling_interest\x00")

		equityKey := "equity\x00" + unit
		if _, exists := rows[equityKey]; exists {
			continue
		}

		fallback := &filing.FactSeries{
			Key: "equity", Label: "Shareholders' equity", Namespace: "derived",
			Concept: "EquityIncludingNoncontrollingInterestFallback", Unit: unit,
			Values: map[string]filing.FactValue{},
		}

		for period, value := range nci.Values {
			if value.Nil || value.ContextRef == "" {
				continue
			}

			fallback.Values[period] = filing.FactValue{
				Value: value.Value, Nil: false, ContextRef: value.ContextRef,
				Decimals: value.Decimals, UnitRef: value.UnitRef,
			}
		}

		if len(fallback.Values) > 0 {
			rows[equityKey] = fallback
		}
	}
}

func deriveMissingLiabilities(accumulator *statementAccumulator) {
	rows := accumulator.Rows["Instant"]
	if rows == nil {
		return
	}

	for totalKey, total := range rows {
		if !strings.HasPrefix(totalKey, "liabilities_and_equity\x00") {
			continue
		}

		unit := strings.TrimPrefix(totalKey, "liabilities_and_equity\x00")

		liabilitiesKey := "liabilities\x00" + unit
		if _, exists := rows[liabilitiesKey]; exists {
			continue
		}

		equity := rows["equity\x00"+unit]
		if equity == nil {
			equity = rows["equity_including_noncontrolling_interest\x00"+unit]
		}

		if equity == nil {
			continue
		}

		derived := &filing.FactSeries{
			Key: "liabilities", Label: "Liabilities", Namespace: "derived",
			Concept: "DerivedLiabilities", Unit: unit, Values: map[string]filing.FactValue{},
		}

		for period, totalValue := range total.Values {
			equityValue, ok := equity.Values[period]
			if !ok || totalValue.Nil || equityValue.Nil ||
				totalValue.ContextRef == "" || totalValue.ContextRef != equityValue.ContextRef {
				continue
			}

			totalRat, totalOK := new(big.Rat).SetString(totalValue.Value)

			equityRat, equityOK := new(big.Rat).SetString(equityValue.Value)
			if !totalOK || !equityOK {
				continue
			}

			derived.Values[period] = filing.FactValue{
				Value: totalRat.Sub(totalRat, equityRat).RatString(),
				Nil:   false, ContextRef: totalValue.ContextRef,
				Decimals: "", UnitRef: totalValue.UnitRef,
			}
		}

		if len(derived.Values) > 0 {
			rows[liabilitiesKey] = derived
		}
	}
}

func statementPeriod(statement, group string) bool {
	if statement == "balance" {
		return group == "Instant"
	}

	return group == "Fiscal year" || group == "Quarterly" || group == "Year to date"
}

func statementGroups(accumulator *statementAccumulator) []filing.SummaryGroup {
	order := []string{"Fiscal year", "Quarterly", "Year to date", "Instant"}

	groups := make([]filing.SummaryGroup, 0, len(accumulator.Rows))
	for _, title := range order {
		rowsByKey, ok := accumulator.Rows[title]
		if !ok {
			continue
		}

		periods := map[string]struct{}{}

		rows := make([]filing.FactSeries, 0, len(rowsByKey))
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
		groups = append(groups, filing.SummaryGroup{Title: title, Periods: periodKeys, Rows: rows})
	}

	return groups
}

func buildRatios(accumulators map[string]*statementAccumulator, taxonomy filing.Taxonomy) []filing.RatioSeries {
	result := make([]filing.RatioSeries, 0, len(taxonomy.Ratios))
	for _, definition := range taxonomy.Ratios {
		statement := "income"
		if definition.Key == "current_ratio" || definition.Key == "debt_to_equity" {
			statement = "balance"
		}

		values := ratioValues(accumulators[statement], definition)
		if len(values) == 0 {
			continue
		}

		result = append(result, filing.RatioSeries{
			Key: definition.Key, Label: definition.Label,
			Derived: true, Formula: definition.Formula, Values: values,
		})
	}

	return result
}

func ratioValues(accumulator *statementAccumulator, definition filing.RatioDefinition) map[string]string {
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
