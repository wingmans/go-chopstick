package choppie

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"wingman.com/chopstick/internal/edgar"
)

const (
	defaultUserAgent  = "wingman paul@wingmen.io"
	edgarUserAgentEnv = "EDGAR_USER_AGENT"
	edgarBaseURLEnv   = "EDGAR_BASE_URL"
	edgarSetEnv       = "EDGAR_SET"
	edgarFormTypesEnv = "EDGAR_FORM_TYPES"
	edgarFromYearEnv  = "EDGAR_FROM_YEAR"
)

func environmentValue(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}

	return fallback
}

func environmentFromYear() (int, error) {
	value := environmentValue(edgarFromYearEnv,
		strconv.Itoa(edgar.EarliestPipelineYear))

	year, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w",
			edgarFromYearEnv, err)
	}

	return year, nil
}

func configuredFormTypes() ([]string, error) {
	value := environmentValue(edgarFormTypesEnv, "10-K,10-Q,8-K")

	return edgar.ValidateSupportedFormTypes(strings.Split(value, ","))
}
