package filingweb

import "strings"

// canonicalCIK preserves significant digits while normalizing leading padding.
func canonicalCIK(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 10 {
		return ""
	}

	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return ""
		}
	}

	value = strings.TrimLeft(value, "0")
	if value == "" {
		value = "0"
	}

	return strings.Repeat("0", 10-len(value)) + value
}
