//nolint:wsl_v5 // Test setup is kept close to the assertion it supports.
package constituents

import (
	"strings"
	"testing"
)

func TestSetWriteJSONNormalizesAndSortsMembers(t *testing.T) {
	t.Parallel()

	set := Set{
		Name: "sp500", AsOf: "2026-09-14", Source: "test",
		Members: []Member{
			{CIK: "789019", Ticker: "msft", Name: " Microsoft Corporation "},
			{CIK: "320193", Ticker: "aapl", Name: "Apple Inc."},
		},
	}
	var output strings.Builder
	if err := set.WriteJSON(&output); err != nil {
		t.Fatalf("WriteJSON returned error: %v", err)
	}
	if !strings.Contains(output.String(), `"cik": "0000320193"`) {
		t.Fatal("expected normalized CIK")
	}
	if strings.Index(output.String(), `"ticker": "AAPL"`) > strings.Index(output.String(), `"ticker": "MSFT"`) {
		t.Fatal("expected members sorted by ticker")
	}
}

func TestSetValidateRejectsDuplicateTickers(t *testing.T) {
	t.Parallel()

	set := Set{
		Name: "sp500", AsOf: "2026-09-14", Source: "test",
		Members: []Member{
			{CIK: "1", Ticker: "AAA", Name: "A"},
			{CIK: "2", Ticker: "AAA", Name: "B"},
		},
	}
	if err := set.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate ticker") {
		t.Fatalf("expected duplicate ticker error, got %v", err)
	}
}
