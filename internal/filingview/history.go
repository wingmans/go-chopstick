package filingview

import (
	"errors"
	"sort"
	"strings"
)

const companyHistoryYears = 10

// CompanyHistory is a derived annual history for one CIK.
type CompanyHistory struct {
	CIK        string             `json:"cik"`
	Company    string             `json:"company"`
	Years      []string           `json:"years"`
	Statements []HistoryStatement `json:"statements"`
}

type HistoryStatement struct {
	Title string       `json:"title"`
	Rows  []HistoryRow `json:"rows"`
}

type HistoryRow struct {
	Key     string               `json:"key"`
	Label   string               `json:"label"`
	Unit    string               `json:"unit"`
	Values  map[string]FactValue `json:"values"`
	Sources map[string]string    `json:"sources"`
}

type historyCandidate struct {
	Value      FactValue
	Accession  string
	FilingDate string
	Concept    string
}

type historyRows map[string]map[string]*historyCandidate

// BuildCompanyHistory combines annual values from local filing views.
// The newest filing wins when a period is present more than once.
func BuildCompanyHistory(views []View, cik string) (CompanyHistory, error) {
	if strings.TrimSpace(cik) == "" {
		return CompanyHistory{}, errors.New("company history CIK is empty")
	}

	cik = historyCIK(cik)
	rowsByStatement := map[string]historyRows{}
	company := ""

	for _, view := range views {
		if historyCIK(view.Metadata.CIK) != cik ||
			(view.Metadata.FormType != "10-K" && view.Metadata.FormType != "10-K/A") {
			continue
		}

		if company == "" {
			company = view.Metadata.Company
		}

		for _, statement := range []StatementView{
			view.Statements.Income, view.Statements.Balance, view.Statements.CashFlow,
		} {
			for _, group := range statement.Groups {
				if !historyGroupAllowed(group.Title) {
					continue
				}

				if rowsByStatement[statement.Title] == nil {
					rowsByStatement[statement.Title] = historyRows{}
				}

				for _, row := range group.Rows {
					rowKey := row.Key + "\x00" + row.Unit
					if rowsByStatement[statement.Title][rowKey] == nil {
						rowsByStatement[statement.Title][rowKey] = map[string]*historyCandidate{}
					}

					for period, value := range row.Values {
						year := historyYear(period)
						if year == "" {
							continue
						}

						candidate := &historyCandidate{
							Value: value, Accession: view.Metadata.Accession,
							FilingDate: view.Metadata.FilingDate, Concept: row.Concept,
						}

						current := rowsByStatement[statement.Title][rowKey][year]
						if current == nil || newerHistoryCandidate(candidate, current) {
							rowsByStatement[statement.Title][rowKey][year] = candidate
						}
					}
				}
			}
		}
	}

	years := historyYears(rowsByStatement)
	if len(years) > companyHistoryYears {
		years = years[:companyHistoryYears]
	}

	result := CompanyHistory{
		CIK: cik, Company: company, Years: years, Statements: []HistoryStatement{},
	}

	for _, title := range []string{"Income statement", "Balance sheet", "Cash flow"} {
		statement := rowsByStatement[title]

		rows := make([]HistoryRow, 0, len(statement))
		for key, values := range statement {
			parts := strings.SplitN(key, "\x00", 2)

			row := HistoryRow{
				Key: parts[0], Label: historyMetricLabel(parts[0]), Unit: "",
				Values: map[string]FactValue{}, Sources: map[string]string{},
			}
			if len(parts) == 2 {
				row.Unit = parts[1]
			}

			for _, year := range years {
				candidate := values[year]
				if candidate == nil {
					continue
				}

				row.Values[year] = candidate.Value
				row.Sources[year] = candidate.Accession
			}

			rows = append(rows, row)
		}

		sort.Slice(rows, func(i, j int) bool { return rows[i].Label < rows[j].Label })
		result.Statements = append(result.Statements,
			HistoryStatement{Title: title, Rows: rows})
	}

	return result, nil
}

func historyCIK(value string) string {
	value = strings.TrimLeft(strings.TrimSpace(value), "0")
	if value == "" {
		return "0"
	}

	return value
}

func historyMetricLabel(key string) string {
	for _, metric := range defaultTaxonomy().Metrics {
		if metric.Key == key {
			return metric.Label
		}
	}

	return key
}

func historyGroupAllowed(title string) bool {
	return title == "Fiscal year" || title == "Instant"
}

func historyYear(period string) string {
	if !strings.HasPrefix(period, "FY") || len(period) != 6 {
		if len(period) >= 4 && period[0] >= '0' && period[0] <= '9' {
			return period[:4]
		}

		return ""
	}

	return period[2:]
}

func historyYears(rows map[string]historyRows) []string {
	seen := map[string]struct{}{}

	for _, statement := range rows {
		for _, row := range statement {
			for year := range row {
				seen[year] = struct{}{}
			}
		}
	}

	years := make([]string, 0, len(seen))
	for year := range seen {
		years = append(years, year)
	}

	sort.Sort(sort.Reverse(sort.StringSlice(years)))

	return years
}

func newerHistoryCandidate(candidate, current *historyCandidate) bool {
	if candidate.FilingDate != current.FilingDate {
		return candidate.FilingDate > current.FilingDate
	}

	if candidate.Accession != current.Accession {
		return candidate.Accession > current.Accession
	}

	return candidate.Concept < current.Concept
}
