package chopstick

import (
	"testing"
	"time"

	"wingman.com/chopstick/internal/edgar"
)

func TestDiscoveryScopeSeparatesFinancialAndOwnershipForms(t *testing.T) {
	t.Parallel()

	filter := edgar.IndexFilter{
		CIKs: []string{"0000320193"},
		FormTypes: []string{
			"10-K", "10-K/A", "10-Q", "10-Q/A", "8-K", "8-K/A",
		},
	}
	record := func(cik, form string) edgar.EdgarIndex {
		return edgar.EdgarIndex{
			CIK: cik, FormType: form,
			DateFiled: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		}
	}

	tests := []struct {
		name   string
		record edgar.EdgarIndex
		want   bool
	}{
		{name: "financial member", record: record("320193", "10-K/A"), want: true},
		{name: "financial outside set", record: record("1", "10-K"), want: false},
		{name: "global institutional", record: record("1", "13F-HR"), want: true},
		{name: "global insider", record: record("1", "4/A"), want: true},
		{name: "global beneficial", record: record("1", "SCHEDULE 13G"), want: true},
		{name: "on demand", record: record("320193", "S-1"), want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := shouldDiscover(test.record, filter); got != test.want {
				t.Fatalf("shouldDiscover() = %t, want %t", got, test.want)
			}
		})
	}
}
