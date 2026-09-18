package filingview

import (
	"testing"

	"wingman.com/fetch-ecb/internal/edgar"
)

func TestDeriveMissingLiabilitiesUsesSameContextAndUnit(t *testing.T) {
	accumulator := &statementAccumulator{Rows: map[string]map[string]*FactSeries{
		"Instant": {
			"liabilities_and_equity\x00USD": {
				Key: "liabilities_and_equity", Label: "Liabilities and equity",
				Namespace: "us-gaap", Concept: "LiabilitiesAndStockholdersEquity", Unit: "USD",
				Values: map[string]FactValue{
					"2025-12-31": testFactValue("150", "balance"),
				},
			},
			"equity\x00USD": {
				Key: "equity", Label: "Shareholders' equity",
				Namespace: "us-gaap", Concept: "StockholdersEquity", Unit: "USD",
				Values: map[string]FactValue{
					"2025-12-31": testFactValue("90", "balance"),
				},
			},
		},
	}}

	deriveMissingLiabilities(accumulator)

	derived := accumulator.Rows["Instant"]["liabilities\x00USD"]
	if derived == nil {
		t.Fatal("expected derived liabilities row")
	}

	if got := derived.Values["2025-12-31"].Value; got != "60" {
		t.Fatalf("derived liabilities = %q, want 60", got)
	}
}

func TestDeriveMissingLiabilitiesSkipsMismatchedContexts(t *testing.T) {
	accumulator := &statementAccumulator{Rows: map[string]map[string]*FactSeries{
		"Instant": {
			"liabilities_and_equity\x00USD": {
				Key: "liabilities_and_equity", Label: "Liabilities and equity",
				Namespace: "us-gaap", Concept: "LiabilitiesAndStockholdersEquity", Unit: "USD",
				Values: map[string]FactValue{
					"2025-12-31": testFactValue("150", "balance"),
				},
			},
			"equity\x00USD": {
				Key: "equity", Label: "Shareholders' equity",
				Namespace: "us-gaap", Concept: "StockholdersEquity", Unit: "USD",
				Values: map[string]FactValue{
					"2025-12-31": testFactValue("90", "restated-balance"),
				},
			},
		},
	}}

	deriveMissingLiabilities(accumulator)

	if _, ok := accumulator.Rows["Instant"]["liabilities\x00USD"]; ok {
		t.Fatal("did not expect derived liabilities for mismatched contexts")
	}
}

func TestFallbackMissingEquityUsesNCIInclusiveEquity(t *testing.T) {
	accumulator := &statementAccumulator{Rows: map[string]map[string]*FactSeries{
		"Instant": {
			"equity_including_noncontrolling_interest\x00USD": {
				Key: "equity_including_noncontrolling_interest", Label: "Equity including noncontrolling interest",
				Namespace: "us-gaap", Concept: "StockholdersEquityIncludingPortionAttributableToNoncontrollingInterest", Unit: "USD",
				Values: map[string]FactValue{
					"2025-12-31": testFactValue("90", "balance"),
				},
			},
		},
	}}

	fallbackMissingEquity(accumulator)

	fallback := accumulator.Rows["Instant"]["equity\x00USD"]
	if fallback == nil || fallback.Namespace != "derived" || fallback.Concept != "EquityIncludingNoncontrollingInterestFallback" {
		t.Fatalf("unexpected equity fallback: %+v", fallback)
	}

	if got := fallback.Values["2025-12-31"].Value; got != "90" {
		t.Fatalf("equity fallback = %q, want 90", got)
	}
}

func TestFallbackMissingEquityKeepsPlainEquity(t *testing.T) {
	accumulator := &statementAccumulator{Rows: map[string]map[string]*FactSeries{
		"Instant": {
			"equity\x00USD": {
				Key: "equity", Label: "Shareholders' equity",
				Namespace: "us-gaap", Concept: "StockholdersEquity", Unit: "USD",
				Values: map[string]FactValue{
					"2025-12-31": testFactValue("80", "balance"),
				},
			},
			"equity_including_noncontrolling_interest\x00USD": {
				Key: "equity_including_noncontrolling_interest", Label: "Equity including noncontrolling interest",
				Namespace: "us-gaap", Concept: "StockholdersEquityIncludingPortionAttributableToNoncontrollingInterest", Unit: "USD",
				Values: map[string]FactValue{
					"2025-12-31": testFactValue("90", "balance"),
				},
			},
		},
	}}

	fallbackMissingEquity(accumulator)

	if got := accumulator.Rows["Instant"]["equity\x00USD"].Values["2025-12-31"].Value; got != "80" {
		t.Fatalf("plain equity changed to %q, want 80", got)
	}
}

func testFactValue(value, context string) FactValue {
	return FactValue{
		Value: value, Nil: false, ContextRef: context, Decimals: "", UnitRef: "usd",
		priority: 0, concept: edgar.QName{Namespace: "", Local: ""}, id: "", precision: "",
	}
}
