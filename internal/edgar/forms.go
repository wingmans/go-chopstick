package edgar

import (
	"fmt"
	"sort"
	"strings"
)

// SupportedFormTypes returns the forms deliberately supported by the parser
// pipeline. Callers receive a copy so this remains the single capability list.
func SupportedFormTypes() []string {
	forms := []string{"10-K", "10-Q", "8-K"}

	sort.Strings(forms)

	return forms
}

// SupportsFormType reports whether a form is an intentional parser input.
func SupportsFormType(form string) bool {
	switch strings.ToUpper(strings.TrimSpace(form)) {
	case "10-K", "10-Q", "8-K":
		return true
	default:
		return false
	}
}

// ValidateSupportedFormTypes normalizes and validates an explicit form list.
func ValidateSupportedFormTypes(forms []string) ([]string, error) {
	seen := make(map[string]struct{}, len(forms))
	result := make([]string, 0, len(forms))

	for _, form := range forms {
		form = strings.ToUpper(strings.TrimSpace(form))
		if form == "" {
			continue
		}

		if !SupportsFormType(form) {
			return nil, fmt.Errorf("unsupported form type %q; supported: %s",
				form, strings.Join(SupportedFormTypes(), ","))
		}

		if _, exists := seen[form]; exists {
			continue
		}

		seen[form] = struct{}{}
		result = append(result, form)
	}

	sort.Strings(result)

	return result, nil
}
