package filingweb

import (
	"math/big"
	"strings"

	"wingman.com/fetch-ecb/internal/filingview"
)

func add(left, right int) int {
	return left + right
}

func mul(left, right int) int {
	return left * right
}

func formatFactValue(value filingview.FactValue, unit string) string {
	if value.Nil || strings.TrimSpace(value.Value) == "" {
		return "-"
	}

	number := strings.ReplaceAll(strings.TrimSpace(value.Value), ",", "")

	rational, ok := new(big.Rat).SetString(number)
	if !ok {
		return value.Value
	}

	currency := strings.Contains(strings.ToLower(unit), "usd")
	perShare := strings.Contains(strings.ToLower(unit), "pershare") ||
		strings.Contains(strings.ToLower(unit), "/shares")

	formatted := compactNumber(rational)
	if !strings.ContainsAny(formatted, "KMBT") && (currency || perShare) {
		formatted = trimDecimal(rational.FloatString(2))
	}

	if currency || perShare {
		if trimmed, ok := strings.CutPrefix(formatted, "("); ok {
			formatted = "($" + strings.TrimSuffix(trimmed, ")") + ")"
		} else {
			formatted = "$" + formatted
		}
	}

	return formatted
}

func compactNumber(value *big.Rat) string {
	negative := value.Sign() < 0
	abs := new(big.Rat).Abs(value)
	suffix := ""

	divisor := big.NewRat(1, 1)
	for _, scale := range []struct {
		threshold int64
		suffix    string
	}{
		{1_000_000_000_000, "T"},
		{1_000_000_000, "B"},
		{1_000_000, "M"},
		{1_000, "K"},
	} {
		if abs.Cmp(big.NewRat(scale.threshold, 1)) >= 0 {
			divisor = big.NewRat(scale.threshold, 1)
			suffix = scale.suffix

			break
		}
	}

	formatted := trimDecimal(new(big.Rat).Quo(abs, divisor).FloatString(1)) + suffix
	if negative {
		return "(" + formatted + ")"
	}

	return formatted
}

func trimDecimal(value string) string {
	value = strings.TrimRight(value, "0")
	value = strings.TrimRight(value, ".")

	return value
}
