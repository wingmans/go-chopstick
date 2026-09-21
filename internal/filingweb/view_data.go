package filingweb

import (
	"net/http"
	"strings"

	"wingman.com/fetch-ecb/internal/edgar"
	"wingman.com/fetch-ecb/internal/filingview"
	"wingman.com/fetch-ecb/internal/filingweb/filingstore"
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
	Page    pageChrome
	History filingview.CompanyHistory
	Ticker  string
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
