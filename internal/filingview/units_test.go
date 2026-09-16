package filingview

import (
	"testing"

	"wingman.com/fetch-ecb/internal/edgar"
)

func TestNormalizeUnits(t *testing.T) {
	tests := []struct {
		name string
		unit edgar.FactUnit
		want string
	}{
		{
			name: "usd",
			unit: edgar.FactUnit{
				ID:       "u1",
				Measures: []edgar.QName{{Namespace: "http://www.xbrl.org/2003/iso4217", Local: "USD"}},
			},
			want: "USD",
		},
		{
			name: "shares",
			unit: edgar.FactUnit{
				ID:       "u2",
				Measures: []edgar.QName{{Namespace: "http://www.xbrl.org/2003/instance", Local: "shares"}},
			},
			want: "shares",
		},
		{
			name: "pure",
			unit: edgar.FactUnit{
				ID:       "u3",
				Measures: []edgar.QName{{Namespace: "http://www.xbrl.org/2003/instance", Local: "pure"}},
			},
			want: "pure",
		},
		{
			name: "usd per share",
			unit: edgar.FactUnit{
				ID:          "u4",
				Numerator:   []edgar.QName{{Namespace: "http://www.xbrl.org/2003/iso4217", Local: "USD"}},
				Denominator: []edgar.QName{{Namespace: "http://www.xbrl.org/2003/instance", Local: "shares"}},
			},
			want: "USD/shares",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeUnit(test.unit); got != test.want {
				t.Fatalf("normalizeUnit() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNormalizedUnitPreservesUnknownUnitRef(t *testing.T) {
	got := normalizedUnit(edgar.Fact{UnitRef: "missing"}, map[string]string{})
	if got != "unknown:missing" {
		t.Fatalf("normalizedUnit() = %q, want unknown:missing", got)
	}
}
