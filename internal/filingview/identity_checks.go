package filingview

import (
	"math/big"
	"sort"
	"strings"
)

const (
	identityPass    = "pass"
	identityFail    = "fail"
	identitySkipped = "skipped"
)

type identityDefinition struct {
	Name      string
	Statement string
	// Result is the reported metric that should equal the signed Terms. The
	// checks are intentionally tiny and explicit so sign choices are reviewed in
	// code instead of inferred from labels like "expense" or "payment".
	Result string
	Terms  []identityTerm
	// SkipWhenPresent suppresses a broader fallback check when a more direct
	// identity is available for the same period and unit. For example, some
	// filers report noncontrolling-interest-inclusive equity totals; comparing
	// total assets to liabilities plus plain shareholders' equity would then be
	// a false warning if the direct liabilities-and-equity total is present.
	SkipWhenPresent []string
}

type identityTerm struct {
	Metric string
	Sign   int64
}

var accountingIdentities = []identityDefinition{
	{
		Name: "gross_profit = revenue - cost_of_revenue", Statement: "income",
		Result: "gross_profit",
		Terms: []identityTerm{
			{Metric: "revenue", Sign: 1},
			{Metric: "cost_of_revenue", Sign: -1},
		},
	},
	{
		Name: "assets = liabilities_and_equity", Statement: "balance",
		Result: "assets",
		Terms: []identityTerm{
			{Metric: "liabilities_and_equity", Sign: 1},
		},
	},
	{
		Name: "assets = liabilities + equity_including_noncontrolling_interest", Statement: "balance",
		Result: "assets",
		Terms: []identityTerm{
			{Metric: "liabilities", Sign: 1},
			{Metric: "equity_including_noncontrolling_interest", Sign: 1},
		},
		SkipWhenPresent: []string{
			"liabilities_and_equity",
		},
	},
	{
		Name: "assets = liabilities + equity", Statement: "balance",
		Result: "assets",
		Terms: []identityTerm{
			{Metric: "liabilities", Sign: 1},
			{Metric: "equity", Sign: 1},
		},
		SkipWhenPresent: []string{
			"liabilities_and_equity",
			"equity_including_noncontrolling_interest",
		},
	},
}

// CheckAccountingIdentities validates relationships between already-selected
// compact rows. It never invents replacements or mutates facts; a failure is a
// data-quality finding that may indicate rounding, taxonomy selection, sign
// policy, or source-filing presentation needs review.
func CheckAccountingIdentities(view View) []IdentityCheck {
	var result []IdentityCheck

	for _, definition := range accountingIdentities {
		statement := identityStatement(view, definition.Statement)
		for _, group := range statement.Groups {
			result = append(result, checkIdentityGroup(definition, group)...)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}

		if result[i].Period != result[j].Period {
			return result[i].Period < result[j].Period
		}

		return result[i].Unit < result[j].Unit
	})

	return result
}

func checkIdentityGroup(definition identityDefinition, group SummaryGroup) []IdentityCheck {
	rows := rowsByMetricAndUnit(group.Rows)
	units := identityUnits(rows, definition)
	checks := make([]IdentityCheck, 0, len(units)*len(group.Periods))

	for _, unit := range units {
		result := rows[definition.Result+"\x00"+unit]

		for _, period := range group.Periods {
			if identityShouldSkip(definition, period, unit, rows) {
				continue
			}

			checks = append(checks, checkIdentityPeriod(definition, period, unit, result, rows))
		}
	}

	return checks
}

func identityShouldSkip(definition identityDefinition, period, unit string, rows map[string]*FactSeries) bool {
	for _, metric := range definition.SkipWhenPresent {
		if _, ok := identityValue(rows[metric+"\x00"+unit], period); ok {
			return true
		}
	}

	return false
}

func checkIdentityPeriod(definition identityDefinition, period, unit string, result *FactSeries, rows map[string]*FactSeries) IdentityCheck {
	check := IdentityCheck{
		Name: definition.Name, Period: period, Unit: unit,
		Status:  identitySkipped,
		Metrics: identityMetrics(definition),
	}

	resultValue, ok := identityValue(result, period)
	if !ok {
		check.Message = "missing " + definition.Result

		return check
	}

	actual, ok := parseFactRat(resultValue)
	if !ok {
		check.Message = "unparseable " + definition.Result

		return check
	}

	expected := new(big.Rat)
	values := []FactValue{resultValue}
	evidence := []IdentityEvidence{
		identityEvidence(definition.Result, "actual", 0, result, resultValue),
	}

	for _, term := range definition.Terms {
		row := rows[term.Metric+"\x00"+unit]
		value, ok := identityValue(row, period)
		if !ok {
			check.Message = "missing " + term.Metric

			return check
		}

		number, ok := parseFactRat(value)
		if !ok {
			check.Message = "unparseable " + term.Metric

			return check
		}

		values = append(values, value)
		evidence = append(evidence, identityEvidence(term.Metric, "term", term.Sign, row, value))

		expected.Add(expected, new(big.Rat).Mul(number, big.NewRat(term.Sign, 1)))
	}

	tolerance := identityTolerance(values...)
	difference := new(big.Rat).Sub(actual, expected)
	difference.Abs(difference)

	check.Expected = expected.FloatString(2)
	check.Actual = actual.FloatString(2)
	check.Tolerance = tolerance.FloatString(2)
	check.Evidence = evidence

	check.Status = identityPass
	if difference.Cmp(tolerance) > 0 {
		check.Status = identityFail
		check.Message = "reported value differs from identity"
	}

	return check
}

func identityStatement(view View, statement string) StatementView {
	switch statement {
	case "income":
		return view.Statements.Income
	case "balance":
		return view.Statements.Balance
	case "cash_flow":
		return view.Statements.CashFlow
	default:
		return StatementView{}
	}
}

func rowsByMetricAndUnit(rows []FactSeries) map[string]*FactSeries {
	result := make(map[string]*FactSeries, len(rows))
	for index := range rows {
		row := &rows[index]
		if row.Key == "" || row.Unit == "" {
			continue
		}

		result[row.Key+"\x00"+row.Unit] = row
	}

	return result
}

func identityUnits(rows map[string]*FactSeries, definition identityDefinition) []string {
	seen := map[string]struct{}{}

	for key := range rows {
		metric, unit, ok := strings.Cut(key, "\x00")
		if !ok || !identityUsesMetric(definition, metric) {
			continue
		}

		seen[unit] = struct{}{}
	}

	units := make([]string, 0, len(seen))
	for unit := range seen {
		units = append(units, unit)
	}

	sort.Strings(units)

	return units
}

func identityUsesMetric(definition identityDefinition, metric string) bool {
	if definition.Result == metric {
		return true
	}

	for _, term := range definition.Terms {
		if term.Metric == metric {
			return true
		}
	}

	return false
}

func identityMetrics(definition identityDefinition) []string {
	result := []string{definition.Result}
	for _, term := range definition.Terms {
		result = append(result, term.Metric)
	}

	return result
}

func identityEvidence(metric, role string, sign int64, row *FactSeries, value FactValue) IdentityEvidence {
	evidence := IdentityEvidence{
		Metric: metric, Role: role, Sign: sign,
		Value: value.Value, ContextRef: value.ContextRef,
		Decimals: value.Decimals, UnitRef: value.UnitRef,
	}
	if row != nil {
		evidence.Namespace = row.Namespace
		evidence.Concept = row.Concept
	}

	return evidence
}

func identityValue(row *FactSeries, period string) (FactValue, bool) {
	if row == nil {
		return FactValue{}, false
	}

	value, ok := row.Values[period]
	if !ok || value.Nil {
		return FactValue{}, false
	}

	return value, true
}

func parseFactRat(value FactValue) (*big.Rat, bool) {
	source := strings.ReplaceAll(strings.TrimSpace(value.Value), ",", "")

	return new(big.Rat).SetString(source)
}

func identityTolerance(values ...FactValue) *big.Rat {
	tolerance := new(big.Rat)
	for _, value := range values {
		// XBRL decimals describe the rounding precision of the reported value.
		// A value with decimals="-6" is rounded to the nearest million, so the
		// maximum rounding drift for that input is half a million. We add the
		// drift for each side of the equation before deciding whether to warn.
		tolerance.Add(tolerance, decimalsTolerance(value.Decimals))
	}

	return tolerance
}

func decimalsTolerance(decimals string) *big.Rat {
	decimals = strings.TrimSpace(decimals)
	if decimals == "" || strings.EqualFold(decimals, "INF") {
		return new(big.Rat)
	}

	scale := new(big.Int)

	if after, ok := strings.CutPrefix(decimals, "-"); ok {
		exponent := after
		if _, ok := scale.SetString(exponent, 10); !ok {
			return new(big.Rat)
		}

		return new(big.Rat).SetFrac(new(big.Int).Exp(big.NewInt(10), scale, nil), big.NewInt(2))
	}

	if _, ok := scale.SetString(decimals, 10); !ok {
		return new(big.Rat)
	}

	// big.Rat is basically: “do accounting math exactly, without float weirdness.”
	return new(big.Rat).SetFrac(big.NewInt(1), new(big.Int).Mul(big.NewInt(2), new(big.Int).Exp(big.NewInt(10), scale, nil)))
}
