package chopstick

import (
	"wingman.com/chopstick/internal/edgar"
	"wingman.com/chopstick/internal/ownership"
)

// AutomaticOwnershipFormTypes returns the issuer-scoped ownership forms that
// the application discovers and processes without an explicit operator action.
func AutomaticOwnershipFormTypes() []string {
	return []string{
		"4", "4/A",
		"SCHEDULE 13D", "SCHEDULE 13D/A",
		"SCHEDULE 13G", "SCHEDULE 13G/A",
	}
}

func matchesAutomaticOwnershipScope(record edgar.EdgarIndex, ciks []string) bool {
	form := ownership.NormalizeFormType(record.FormType)
	if !isAutomaticOwnershipForm(form) || len(ciks) == 0 {
		return false
	}

	return edgar.MatchesIndexFilter(record, edgar.IndexFilter{CIKs: ciks})
}

func isAutomaticOwnershipForm(form string) bool {
	switch form {
	case "4", "4/A", "SCHEDULE 13D", "SCHEDULE 13D/A",
		"SCHEDULE 13G", "SCHEDULE 13G/A":
		return true
	default:
		return false
	}
}
