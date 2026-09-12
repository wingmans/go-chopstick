package edgar

import (
	"reflect"
	"strings"
	"testing"
)

const testNamespace = "urn:test"

func TestXBRLSourceValues(t *testing.T) {
	instance, recognized, err := readXBRL(t.Context(), strings.NewReader(testInstance))
	if err != nil || !recognized {
		t.Fatalf("recognized=%v error=%v", recognized, err)
	}

	if len(instance.Facts) != 5 || len(instance.Contexts) != 2 || len(instance.Units) != 2 {
		t.Fatal("missing instance data")
	}

	facts := instance.FactsByConcept(QName{Namespace: testNamespace, Local: "Revenue"})
	if len(facts) != 2 || facts[0].Value != "9007199254740993.01" || facts[0].Decimals != "2" || facts[0].Language != "en" {
		t.Fatalf("duplicate or exact values lost: %+v", facts)
	}

	if !instance.Facts[3].Nil || instance.Facts[3].Value != "" || instance.Facts[2].Nil || instance.Facts[2].Value != "0" {
		t.Fatal("nil and zero conflated")
	}

	if instance.Facts[4].Value != " A & B " {
		t.Fatal("text value changed")
	}

	if len(instance.FactsByConcept(QName{Namespace: testNamespace, Local: "Missing"})) != 0 {
		t.Fatal("invented missing fact")
	}
}

func TestXBRLContextsUnitsAndLinks(t *testing.T) {
	instance, recognized, err := readXBRL(t.Context(), strings.NewReader(testInstance))
	if err != nil || !recognized {
		t.Fatalf("recognized=%v error=%v", recognized, err)
	}

	value, ok := instance.Context("c")
	if !ok || value.Entity != "123" || value.Scheme != "urn:cik" || value.StartDate != "2024-01-01" || value.EndDate != "2024-12-31" {
		t.Fatalf("unexpected context: %+v", value)
	}

	if len(value.Dimensions) != 2 || value.Dimensions[0].Axis != (QName{Namespace: testNamespace, Local: "Axis"}) ||
		*value.Dimensions[0].Member != (QName{Namespace: testNamespace, Local: "Member"}) || value.Dimensions[1].Typed == nil {
		t.Fatal("dimensions lost")
	}

	unit, ok := instance.Unit("u")
	if !ok || unit.Measures[0] != (QName{Namespace: "urn:currency", Local: "USD"}) {
		t.Fatal("nested namespace declaration lost")
	}

	ratio, ok := instance.Unit("ratio")
	if !ok || len(ratio.Numerator) != 1 || len(ratio.Denominator) != 1 {
		t.Fatal("divided unit lost")
	}

	if len(instance.References) != 1 || len(instance.Footnotes) != 1 {
		t.Fatal("references lost")
	}

	if len(instance.referenceErrors()) != 0 {
		t.Fatal("unexpected reference errors")
	}
}

func TestXBRLAlternatePrefixes(t *testing.T) {
	first, _, err := readXBRL(t.Context(), strings.NewReader(testInstance))
	if err != nil {
		t.Fatal(err)
	}

	changed := strings.NewReplacer("xmlns:i=", "xmlns:instance=", "<i:", "<instance:", "</i:", "</instance:",
		"i:shares", "instance:shares", "xmlns:t=", "xmlns:company=", "t:", "company:").Replace(testInstance)

	second, recognized, err := readXBRL(t.Context(), strings.NewReader(changed))
	if err != nil || !recognized {
		t.Fatalf("recognized=%v error=%v", recognized, err)
	}

	for index := range first.Facts {
		first.Facts[index].Namespaces = nil
		second.Facts[index].Namespaces = nil
	}

	if !reflect.DeepEqual(first.Facts, second.Facts) {
		t.Fatal("facts depend on namespace prefixes")
	}

	if first.Contexts[0].Dimensions[0].Axis != second.Contexts[0].Dimensions[0].Axis {
		t.Fatal("dimension depends on prefix")
	}
}

func TestXBRLRejectsMalformedInstances(t *testing.T) {
	for name, source := range map[string]string{
		"trailing root":     testInstance + "<extra/>",
		"trailing text":     testInstance + "bad",
		"unbound dimension": strings.ReplaceAll(testInstance, `dimension="t:Axis"`, `dimension="missing:Axis"`),
		"nil with content":  strings.ReplaceAll(testInstance, `xsi:nil="true"/>`, `xsi:nil="true">1</t:Absent>`),
		"invalid nil":       strings.ReplaceAll(testInstance, `xsi:nil="true"`, `xsi:nil="yes"`),
		"missing entity":    strings.ReplaceAll(testInstance, "i:entity", "i:other"),
		"missing measure":   strings.ReplaceAll(testInstance, "i:measure", "i:other"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := readXBRL(t.Context(), strings.NewReader(source)); err == nil {
				t.Fatal("expected XML or structural error")
			}
		})
	}
}
