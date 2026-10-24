package edgar

import "testing"

func TestSupportedFormTypesAreCentralizedAndValidated(t *testing.T) {
	t.Parallel()

	forms, err := ValidateSupportedFormTypes([]string{"8-k", "10-K", "8-K"})
	if err != nil {
		t.Fatal(err)
	}

	if len(forms) != 2 || forms[0] != "10-K" || forms[1] != "8-K" {
		t.Fatalf("unexpected forms: %#v", forms)
	}

	if _, err := ValidateSupportedFormTypes([]string{"S-1"}); err == nil {
		t.Fatal("expected unsupported form error")
	}
}
