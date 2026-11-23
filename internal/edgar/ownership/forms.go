// Package ownership parses SEC ownership filings into source-level records.
package ownership

import (
	"sort"
	"strings"
)

const (
	FamilyInstitutional = "institutional"
	FamilyInsider       = "insider"
	FamilyBeneficial    = "beneficial"
)

// NormalizeFormType applies the normalization used by ownership dispatch.
func NormalizeFormType(form string) string {
	return strings.ToUpper(strings.Join(strings.Fields(form), " "))
}

// Family returns the semantic ownership family for a recognized form.
func Family(form string) (string, bool) {
	switch NormalizeFormType(form) {
	case "13F-HR", "13F-HR/A", "13F-NT", "13F-NT/A":
		return FamilyInstitutional, true
	case "3", "3/A", "4", "4/A", "5", "5/A":
		return FamilyInsider, true
	case "SCHEDULE 13D", "SCHEDULE 13D/A", "SCHEDULE 13G",
		"SCHEDULE 13G/A", "SC 13D", "SC 13D/A", "SC 13G", "SC 13G/A":
		return FamilyBeneficial, true
	default:
		return "", false
	}
}

// RecognizesForm reports whether the importer accounts for the form. Some
// recognized legacy forms deliberately have no parser yet.
func RecognizesForm(form string) bool {
	_, ok := Family(form)

	return ok
}

// SupportsForm reports whether the form has a structured parser.
func SupportsForm(form string) bool {
	switch NormalizeFormType(form) {
	case "13F-HR", "13F-HR/A", "13F-NT", "13F-NT/A",
		"3", "3/A", "4", "4/A", "5", "5/A",
		"SCHEDULE 13D", "SCHEDULE 13D/A",
		"SCHEDULE 13G", "SCHEDULE 13G/A":
		return true
	default:
		return false
	}
}

// RecognizedFormTypes returns supported and deferred ownership forms.
func RecognizedFormTypes() []string {
	forms := []string{
		"13F-HR", "13F-HR/A", "13F-NT", "13F-NT/A",
		"3", "3/A", "4", "4/A", "5", "5/A",
		"SCHEDULE 13D", "SCHEDULE 13D/A", "SCHEDULE 13G",
		"SCHEDULE 13G/A", "SC 13D", "SC 13D/A", "SC 13G", "SC 13G/A",
	}

	sort.Strings(forms)

	return forms
}

// SupportedFormTypes returns the forms with structured parsers.
func SupportedFormTypes() []string {
	forms := make([]string, 0, len(RecognizedFormTypes()))
	for _, form := range RecognizedFormTypes() {
		if SupportsForm(form) {
			forms = append(forms, form)
		}
	}

	sort.Strings(forms)

	return forms
}

// IsAmendment reports whether the normalized form is an amendment.
func IsAmendment(form string) bool {
	return strings.HasSuffix(NormalizeFormType(form), "/A")
}
