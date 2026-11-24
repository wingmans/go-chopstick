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
	list, err := fetchCompanyList(ctx, client, sourceURL, userAgent)
	if err != nil {
		return nil, err
	}

	companies := make(map[string]Company, len(list))
	for _, company := range list {
		ticker := constituents.NormalizeTicker(company.Ticker)
		if previous, exists := companies[ticker]; exists {
			return nil, fmt.Errorf(
				"SEC company directory has duplicate ticker %s for CIKs %s and %s",
				ticker, previous.CIK.String(), company.CIK.String(),
			)
		}

		companies[ticker] = company
	}

	return companies, nil
}

// FetchCompaniesByCIK reads the SEC company directory indexed by normalized
// ten-digit CIK. Each CIK retains all listed tickers so callers can match the
// exact CIK and ticker pair without discarding share classes.
func FetchCompaniesByCIK(ctx context.Context, client *http.Client, sourceURL,
	userAgent string,
) (map[string][]Company, error) {
	list, err := fetchCompanyList(ctx, client, sourceURL, userAgent)
	if err != nil {
		return nil, err
	}

	companies := make(map[string][]Company, len(list))
	for _, company := range list {
		cik, err := constituents.NormalizeCIK(company.CIK.String())
		if err != nil {
			return nil, fmt.Errorf("SEC company ticker %s: %w", company.Ticker, err)
		}

		ticker := constituents.NormalizeTicker(company.Ticker)
		for _, previous := range companies[cik] {
			if constituents.NormalizeTicker(previous.Ticker) == ticker {
				return nil, fmt.Errorf(
					"SEC company directory has duplicate identity %s %s",
					cik, ticker,
				)
			}
		}

		companies[cik] = append(companies[cik], company)
	}

	return companies, nil
}

func fetchCompanyList(ctx context.Context, client *http.Client, sourceURL,
	userAgent string,
) ([]Company, error) {
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

	companies := make([]Company, 0, len(raw))
	for _, company := range raw {
		companies = append(companies, company)
	}

	return companies, nil
}
