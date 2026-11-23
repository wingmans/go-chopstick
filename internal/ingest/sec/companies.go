package sec

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"wingman.com/chopstick/internal/constituents"
)

const DefaultCompanyTickersURL = "https://www.sec.gov/files/company_tickers.json"

// Company is one SEC company-directory entry.
type Company struct {
	CIK    json.Number `json:"cik_str"`
	Ticker string      `json:"ticker"`
	Title  string      `json:"title"`
}

// FetchCompanies reads the SEC company directory indexed by normalized
// ticker.
func FetchCompanies(ctx context.Context, client *http.Client, sourceURL,
	userAgent string,
) (map[string]Company, error) {
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet, sourceURL, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create SEC company request: %w", err)
	}

	request.Header.Set("User-Agent", userAgent)

	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", sourceURL, err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %s", sourceURL,
			response.Status)
	}

	var raw map[string]Company
	if err := json.NewDecoder(response.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode SEC ticker data: %w", err)
	}

	companies := make(map[string]Company, len(raw))
	for _, company := range raw {
		companies[constituents.NormalizeTicker(company.Ticker)] = company
	}

	return companies, nil
}
