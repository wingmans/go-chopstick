package ownership

import "testing"

func TestOwnershipFormRegistry(t *testing.T) {
	t.Parallel()

	for _, form := range SupportedFormTypes() {
		if !RecognizesForm(form) || !SupportsForm(form) {
			t.Fatalf("supported form %q is not recognized", form)
		}
	}

	if !RecognizesForm(" sc 13g/a ") || SupportsForm("SC 13G/A") {
		t.Fatal("legacy Schedule 13G classification is incorrect")
	}

	if family, ok := Family("schedule   13d/a"); !ok ||
		family != FamilyBeneficial {
		t.Fatalf("family = %q, %t", family, ok)
	}

	if !IsAmendment(" 4/a ") || IsAmendment("4") {
		t.Fatal("amendment classification is incorrect")
	}
}
