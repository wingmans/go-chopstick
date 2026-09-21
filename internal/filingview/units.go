package filingview

import (
	"strings"

	"wingman.com/fetch-ecb/internal/edgar"
)

// unitMap creates a mapping from unit IDs to their normalized string representations.
func unitMap(instance edgar.XBRLInstance) map[string]string {
	result := make(map[string]string, len(instance.Units))
	for _, unit := range instance.Units {
		result[unit.ID] = normalizeUnit(unit)
	}

	return result
}

// normalizedUnit retrieves the normalized string representation of the unit associated with a fact.
func normalizedUnit(fact edgar.Fact, units map[string]string) string {
	if strings.TrimSpace(fact.UnitRef) == "" {
		return ""
	}

	if unit, ok := units[fact.UnitRef]; ok {
		return unit
	}

	return "unknown:" + fact.UnitRef
}

// normalizeUnit converts a FactUnit into its normalized string representation.
func normalizeUnit(unit edgar.FactUnit) string {
	if len(unit.Measures) > 0 {
		return normalizeMeasures(unit.Measures)
	}

	if len(unit.Numerator) > 0 || len(unit.Denominator) > 0 {
		numerator := normalizeMeasures(unit.Numerator)
		denominator := normalizeMeasures(unit.Denominator)

		if numerator == "" || denominator == "" {
			return "unknown:" + unit.ID
		}

		return numerator + "/" + denominator
	}

	return "unknown:" + unit.ID
}

func normalizeMeasures(measures []edgar.QName) string {
	parts := make([]string, 0, len(measures))
	for _, measure := range measures {
		parts = append(parts, normalizeMeasure(measure))
	}

	return strings.Join(parts, "*")
}

// normalizeMeasure converts an individual measure to a standardized string representation.
func normalizeMeasure(measure edgar.QName) string {
	switch strings.ToLower(measure.Local) {
	case "usd":
		return "USD"
	case "shares", "share":
		return "shares"
	case "pure":
		return "pure"
	default:
		if measure.Namespace != "" {
			return measure.Namespace + "#" + measure.Local
		}

		return measure.Local
	}
}
