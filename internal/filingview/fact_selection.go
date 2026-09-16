package filingview

import (
	"math"
	"strconv"
	"strings"

	"wingman.com/fetch-ecb/internal/edgar"
)

type selectedFact struct {
	Fact     edgar.Fact
	Priority int
}

func selectFact(existing FactValue, existingConcept edgar.QName, candidate selectedFact) (FactValue, edgar.QName) {
	value := factValue(candidate)
	if existing.ContextRef == "" {
		return value, candidate.Fact.Concept
	}

	current := selectedFact{
		Fact: edgar.Fact{
			Concept:    existingConcept,
			Value:      existing.Value,
			ContextRef: existing.ContextRef,
			UnitRef:    existing.UnitRef,
			Decimals:   existing.Decimals,
			Precision:  existing.precision,
			Nil:        existing.Nil,
			ID:         existing.id,
		},
		Priority: existing.priority,
	}

	if betterSelectedFact(candidate, current) {
		return value, candidate.Fact.Concept
	}

	return existing, existingConcept
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

func factValue(candidate selectedFact) FactValue {
	fact := candidate.Fact

	return FactValue{
		Value:      fact.Value,
		Nil:        fact.Nil,
		ContextRef: fact.ContextRef,
		Decimals:   fact.Decimals,
		UnitRef:    fact.UnitRef,
		priority:   candidate.Priority,
		concept:    fact.Concept,
		id:         fact.ID,
		precision:  fact.Precision,
	}
}

func candidatePrecisionScore(fact edgar.Fact) int {
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
