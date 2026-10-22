package filing

import (
	"testing"

	"wingman.com/fetch-ecb/internal/edgar"
)

func TestClassifyQuarterlyPeriods(t *testing.T) {
	tests := []struct {
		name, start, end, wantGroup, wantKey string
	}{
		{"quarter", "2024-10-01", "2024-12-31", "Quarterly", "Q4 FY2024"},
		{"year to date six months", "2024-07-01", "2024-12-31", "Year to date", "YTD FY2024"},
		{"year to date nine months", "2024-04-01", "2024-12-31", "Year to date", "YTD FY2024"},
		{"instant", "", "2024-12-31", "Instant", "2024-12-31"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			context := edgar.FactContext{StartDate: test.start, EndDate: test.end}
			if test.name == "instant" {
				context.Instant = test.end
			}

			group, key, _ := classifyPeriod(context, "2024-12-31")
			if group != test.wantGroup || key != test.wantKey {
				t.Fatalf("got group=%q key=%q, want group=%q key=%q", group, key, test.wantGroup, test.wantKey)
			}
		})
	}
}

func TestClassifyQuarterlyPeriodUsesReportYear(t *testing.T) {
	context := edgar.FactContext{StartDate: "2023-07-01", EndDate: "2023-09-30"}

	group, key, _ := classifyPeriod(context, "2024-06-30")
	if group != "Quarterly" || key != "Q1 FY2024" {
		t.Fatalf("got group=%q key=%q", group, key)
	}
}
