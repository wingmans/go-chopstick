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
				ID: "u1", Measures: []edgar.QName{{
					Namespace: "http://www.xbrl.org/2003/iso4217", Local: "USD",
				}},
				Numerator: nil, Denominator: nil, Source: emptyXMLNode(),
			},
			want: "USD",
		},
		{
			name: "shares",
			unit: edgar.FactUnit{
				ID: "u2", Measures: []edgar.QName{{
					Namespace: "http://www.xbrl.org/2003/instance", Local: "shares",
				}},
				Numerator: nil, Denominator: nil, Source: emptyXMLNode(),
			},
			want: "shares",
		},
		{
			name: "pure",
			unit: edgar.FactUnit{
				ID: "u3", Measures: []edgar.QName{{
					Namespace: "http://www.xbrl.org/2003/instance", Local: "pure",
				}},
				Numerator: nil, Denominator: nil, Source: emptyXMLNode(),
			},
			want: "pure",
		},
		{
			name: "usd per share",
			unit: edgar.FactUnit{
				ID: "u4", Measures: nil,
				Numerator: []edgar.QName{{
					Namespace: "http://www.xbrl.org/2003/iso4217", Local: "USD",
				}},
				Denominator: []edgar.QName{{
					Namespace: "http://www.xbrl.org/2003/instance", Local: "shares",
				}},
				Source: emptyXMLNode(),
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
	got := normalizedUnit(edgar.Fact{
		Namespaces: nil,
		Concept:    edgar.QName{Namespace: "", Local: ""},
		Value:      "",
		ContextRef: "",
		UnitRef:    "missing",
		Decimals:   "",
		Precision:  "",
		Language:   "",
		Nil:        false,
		ID:         "",
		Attributes: nil,
		Structured: nil,
	}, map[string]string{})
	if got != "unknown:missing" {
		t.Fatalf("normalizedUnit() = %q, want unknown:missing", got)
	}
}
