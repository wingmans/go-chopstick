package web

import (
	"net/http"
	"sort"
	"strings"
	"time"

	dividendview "wingman.com/fetch-ecb/internal/dividend"
	"wingman.com/fetch-ecb/internal/edgar"
	filingview "wingman.com/fetch-ecb/internal/filing"
	filingstore "wingman.com/fetch-ecb/internal/web/store"
)

type filingSummary struct {
	CIK        string `json:"cik"`
	Ticker     string `json:"ticker"`
	Company    string `json:"company"`
	Accession  string `json:"accession"`
	FormType   string `json:"form_type"`
	FilingDate string `json:"filing_date"`
	ReportDate string `json:"report_date"`
	Status     string `json:"status"`
	Facts      int    `json:"facts"`
}

type pageChrome struct {
	Title     string
	BodyClass string
	UseHTMX   bool
}

type dashboardData struct {
	Page        pageChrome      `json:"-"`
	FilingCount int             `json:"filing_count"`
	Company     string          `json:"company"`
	CIK         string          `json:"cik"`
	Query       string          `json:"query"`
	FormType    string          `json:"-"`
	Year        string          `json:"-"`
	SetName     string          `json:"set_name"`
	SetAsOf     string          `json:"set_as_of"`
	HasFilters  bool            `json:"-"`
	ReturnTo    string          `json:"-"`
	Filings     []filingSummary `json:"filings"`
}

type companyPageData struct {
	Page          pageChrome
	History       filingview.CompanyHistory
	Ticker        string
	Dividends     dividendview.View
	DividendTable companyDividendTable
	HasDividends  bool
}

type companyDividendTable struct {
	Years []string
	Rows  []companyDividendRow
}

type companyDividendRow struct {
	Label  string
	Values map[string][]string
}

type detailData struct {
	Page          pageChrome
	Filing        *edgar.ParsedFiling
	BackURL       string
	Summary       []filingview.SummaryGroup
	Statements    []filingview.StatementView
	Ratios        []filingview.RatioSeries
	Facts         int
	DocumentCount int
	InstanceCount int
	ContextCount  int
	Diagnostics   int
	Dividends     dividendview.View
	HasDividends  bool
}

func buildCompanyDividendTable(view dividendview.View, years []string) companyDividendTable {
	rows := map[string]map[string][]string{}
	add := func(label, year, value string) {
		if year == "" || value == "" {
			return
		}

		if rows[label] == nil {
			rows[label] = map[string][]string{}
		}

		rows[label][year] = append(rows[label][year], value)
	}

	for _, event := range view.Events {
		year := dividendYear(event.DeclarationDate)
		value := formatFactValue(filingview.FactValue{Value: event.AmountPerShare},
			event.Currency+"/share") + "/share"

		if event.PayableDate != "" {
			value += " · payable " + event.PayableDate
		}

		add("Declared cash dividends", year, value)
	}

	for _, observation := range view.Observations {
		label := "Dividend observation"

		switch observation.Kind {
		case "cash_paid":
			label = "Common-stock cash dividends paid"
		case "per_share_declared":
			label = "Declared dividends per share"
		}

		unit := "USD"
		if observation.Kind == "per_share_declared" {
			unit = "USD/share"
		}

		add(label, dividendYear(observation.Period), formatFactValue(
			filingview.FactValue{Value: observation.Value}, unit))
	}

	labels := make([]string, 0, len(rows))
	for label := range rows {
		labels = append(labels, label)
	}

	sort.Strings(labels)

	result := companyDividendTable{Years: append([]string(nil), years...)}

	for _, label := range labels {
		result.Rows = append(result.Rows, companyDividendRow{
			Label: label, Values: rows[label],
		})
	}

	return result
}

func dividendYear(value string) string {
	if len(value) >= 4 && value[0] >= '0' && value[0] <= '9' &&
		value[1] >= '0' && value[1] <= '9' &&
		value[2] >= '0' && value[2] <= '9' &&
		value[3] >= '0' && value[3] <= '9' {
		return value[:4]
	}

	date, err := time.Parse("January 2, 2006", value)
	if err != nil {
		return ""
	}

	return date.Format("2006")
}

// dashboardData prepares the data needed to render the dashboard page based on the current request.
func (s *Server) dashboardData(r *http.Request) (dashboardData, error) {
	page := pageChrome{
		Title:     "EDGAR local filings",
		BodyClass: "dashboard-page",
		UseHTMX:   true,
	}

	cik := strings.TrimSpace(r.URL.Query().Get("cik"))
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	formType := strings.TrimSpace(r.URL.Query().Get("form_type"))

	year := strings.TrimSpace(r.URL.Query().Get("year"))

	hasFilters := cik != "" || query != "" || formType != "" || year != ""
	if cik == "" && query == "" && formType == "" && year == "" {
		return dashboardData{
			Page: page, FilingCount: 0, Company: "", CIK: "", Query: "",
			FormType: "", Year: "", HasFilters: false, ReturnTo: "/",
			SetName: s.setName, SetAsOf: s.setAsOf,
			Filings: []filingSummary{},
		}, nil
	}

	stored, err := s.store.ListSummaries(r.Context())
	if err != nil {
		return dashboardData{}, err
	}

	filings := s.summaryViewData(stored)

	queryCIKs := s.resolveQuery(query)
	filtered := make([]filingSummary, 0, len(filings))
	company := ""

	selectedCIK := canonicalCIK(cik)
	if selectedCIK == "" && len(queryCIKs) == 1 {
		for match := range queryCIKs {
			selectedCIK = match
		}
	}

	for _, filing := range filings {
		if cik != "" && canonicalCIK(filing.CIK) != canonicalCIK(cik) {
			continue
		}

		if query != "" && len(queryCIKs) == 0 {
			continue
		}

		if len(queryCIKs) > 0 && !queryCIKs[canonicalCIK(filing.CIK)] {
			continue
		}

		if formType != "" && !strings.EqualFold(filing.FormType, formType) {
			continue
		}

		if year != "" && !strings.HasPrefix(filing.FilingDate, year) {
			continue
		}

		if company == "" {
			company = filing.Company
		}

		filtered = append(filtered, filing)
	}

	return dashboardData{
		Page: page, FilingCount: len(filtered), Company: company, CIK: selectedCIK,
		Query: query, FormType: formType, Year: year, SetName: s.setName,
		SetAsOf: s.setAsOf, HasFilters: hasFilters, ReturnTo: r.URL.RequestURI(),
		Filings: filtered,
	}, nil
}

func (s *Server) resolveQuery(query string) map[string]bool {
	if query == "" {
		return nil
	}

	if cik := canonicalCIK(query); cik != "" {
		return map[string]bool{cik: true}
	}

	matches := map[string]bool{}

	lowerQuery := strings.ToLower(query)
	for key, ciks := range s.memberByKey {
		if key == lowerQuery || strings.Contains(key, lowerQuery) {
			for _, cik := range ciks {
				matches[cik] = true
			}
		}
	}

	return matches
}

func (s *Server) summaryViewData(stored []filingstore.Summary) []filingSummary {
	filings := make([]filingSummary, 0, len(stored))
	for _, summary := range stored {
		cik := canonicalCIK(summary.CIK)
		filings = append(filings, filingSummary{
			CIK:        summary.CIK,
			Ticker:     s.memberByCIK[cik].Ticker,
			Company:    summary.Company,
			Accession:  summary.Accession,
			FormType:   summary.FormType,
			FilingDate: summary.FilingDate,
			ReportDate: summary.ReportDate,
			Status:     summary.Status,
			Facts:      summary.Facts,
		})
	}

	return filings
}
