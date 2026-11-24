package operator

import (
	"os"
	"strings"
)

const (
	edgarBaseURLEnv  = "EDGAR_BASE_URL"
	edgarSetEnv      = "EDGAR_SET"
	edgarFromYearEnv = "EDGAR_FROM_YEAR"
)

func environmentValue(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}

	return fallback
}
