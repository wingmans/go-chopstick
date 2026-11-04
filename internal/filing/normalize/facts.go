package normalize

import (
	"math"
	"strconv"
	"strings"

	"wingman.com/chopstick/internal/filing"
	"wingman.com/chopstick/internal/parser"
)

type selectedFact struct {
	Fact     parser.Fact
	Priority int
}

func selectInto(selections map[*filing.FactSeries]map[string]selectedFact,
	row *filing.FactSeries, period string, candidate selectedFact,
) {
	if selections[row] == nil {
		selections[row] = map[string]selectedFact{}
	}

	current, exists := selections[row][period]
	if exists && !betterSelectedFact(candidate, current) {
		return
	}

	selections[row][period] = candidate
	row.Values[period] = factValue(candidate)
	row.Namespace = candidate.Fact.Concept.Namespace
	row.Concept = candidate.Fact.Concept.Local
}

func betterSelectedFact(candidate, current selectedFact) bool {
	if candidate.Priority != current.Priority {
		return candidate.Priority < current.Priority
	}

	if candidate.Fact.Nil != current.Fact.Nil {
		return !candidate.Fact.Nil
	}

	if candidatePrecisionScore(candidate.Fact) != candidatePrecisionScore(current.Fact) {
		return candidatePrecisionScore(candidate.Fact) > candidatePrecisionScore(current.Fact)
	}

	if candidate.Fact.ID != current.Fact.ID {
		return candidate.Fact.ID < current.Fact.ID
	}

	if candidate.Fact.Concept.Local != current.Fact.Concept.Local {
		return candidate.Fact.Concept.Local < current.Fact.Concept.Local
	}

	if candidate.Fact.Concept.Namespace != current.Fact.Concept.Namespace {
		return candidate.Fact.Concept.Namespace < current.Fact.Concept.Namespace
	}

	if candidate.Fact.ContextRef != current.Fact.ContextRef {
		return candidate.Fact.ContextRef < current.Fact.ContextRef
	}

	return candidate.Fact.Value < current.Fact.Value
}

func factValue(candidate selectedFact) filing.FactValue {
	fact := candidate.Fact

	return filing.FactValue{
		Value:      fact.Value,
		Nil:        fact.Nil,
		ContextRef: fact.ContextRef,
		Decimals:   fact.Decimals,
		UnitRef:    fact.UnitRef,
	}
}

func candidatePrecisionScore(fact parser.Fact) int {
	if strings.EqualFold(fact.Decimals, "INF") || strings.EqualFold(fact.Precision, "INF") {
		return math.MaxInt
	}

	if fact.Decimals != "" {
		if decimals, err := strconv.Atoi(fact.Decimals); err == nil {
			return decimals
		}
	}

	if fact.Precision != "" {
		if precision, err := strconv.Atoi(fact.Precision); err == nil {
			return precision
		}
	}

	return math.MinInt
}
