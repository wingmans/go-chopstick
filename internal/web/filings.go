package web

import (
	"errors"
	"net/http"
	"os"
	"strings"

	filingview "wingman.com/chopstick/internal/filing"
	"wingman.com/chopstick/internal/parser"
)

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	data, err := s.dashboardData(r)
	if err != nil {
		s.writeError(w, err)

		return
	}

	component := dashboardPage(data)
	if isWorkspaceRequest(r) {
		component = dashboardPageBody(data)
	} else if r.Header.Get("Hx-Request") == "true" &&
		strings.Contains(r.Header.Get("Hx-Target"), "#dashboard") {
		component = dashboardFragment(data)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if err := component.Render(r.Context(), w); err != nil {
		s.writeError(w, err)
	}
}

// company handles requests to view a specific company's page.
func (s *Server) company(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/companies/"), "/")
	if len(parts) != 1 {
		http.NotFound(w, r)

		return
	}

	cik := canonicalCIK(parts[0])
	if cik == "" {
		http.NotFound(w, r)

		return
	}

	history, err := s.store.LoadCompanyHistory(r.Context(), cik)
	if errors.Is(err, os.ErrNotExist) || len(history.Years) == 0 {
		http.NotFound(w, r)

		return
	}

	if err != nil {
		s.writeError(w, err)

		return
	}

	member := s.memberByCIK[cik]

	dividends, dividendErr := s.store.LoadDividendView(r.Context(), cik)
	if dividendErr != nil && !errors.Is(dividendErr, os.ErrNotExist) {
		s.writeError(w, dividendErr)

		return
	}

	title := history.Company
	if member.Ticker != "" {
		title = member.Ticker + " - " + title
	}

	data := companyPageData{
		Page: pageChrome{
			Title: title, BodyClass: "company-page",
			ActiveSection: "filings", UseHTMX: true,
		},
		History:       history,
		Ticker:        member.Ticker,
		Dividends:     dividends,
		DividendTable: buildCompanyDividendTable(dividends, history.Years),
		HasDividends:  len(dividends.Events) > 0 || len(dividends.Observations) > 0,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	component := companyPage(data)
	if isWorkspaceRequest(r) {
		component = companyPageBody(data)
	}

	if err := component.Render(r.Context(), w); err != nil {
		s.writeError(w, err)
	}
}

// apiFilings handles requests to the /api/filings endpoint, returning the filtered filings as JSON.
func (s *Server) apiFilings(w http.ResponseWriter, r *http.Request) {
	data, err := s.dashboardData(r)
	if err != nil {
		s.writeError(w, err)

		return
	}

	s.writeJSON(w, data)
}

func (s *Server) detail(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/filings/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)

		return
	}

	view, err := s.store.LoadView(r.Context(), parts[0], parts[1])
	if errors.Is(err, os.ErrNotExist) {
		http.NotFound(w, r)

		return
	}

	if err != nil {
		s.writeError(w, err)

		return
	}

	backURL := "/"
	if candidate := r.URL.Query().Get("return_to"); validReturnURL(candidate) {
		backURL = candidate
	}

	viewFiling := &parser.ParsedFiling{
		SchemaVersion: parser.ParsedSchemaVersion,
		ParserVersion: view.ParserVersion,
		SourcePath:    view.SourcePath,
		SourceBase:    "",
		SourceSHA256:  view.SourceSHA256,
		Metadata: parser.FilingMetadata{
			Accession: view.Metadata.Accession, CIK: view.Metadata.CIK,
			FormType: view.Metadata.FormType, FilingDate: view.Metadata.FilingDate,
			ReportDate: view.Metadata.ReportDate,
			Filers:     []parser.SubmissionFiler{{CIK: view.Metadata.CIK, Name: view.Metadata.Company, Header: []string{}}},
			Items:      []string{}, Header: []string{},
		},
		Documents: view.Documents, Instances: []parser.XBRLInstance{},
		Diagnostics: []parser.ParseDiagnostic{}, Status: view.Counts.Status,
	}

	data := detailData{
		Page: pageChrome{
			Title:     view.Metadata.FormType + " " + view.Metadata.Accession,
			BodyClass: "detail-page", ActiveSection: "filings",
			UseHTMX: true,
		},
		Filing: viewFiling, Summary: view.Summary,
		Statements: []filingview.StatementView{
			view.Statements.Income, view.Statements.Balance, view.Statements.CashFlow,
		},
		Ratios: view.Ratios, Facts: view.Counts.Facts,
		DocumentCount: view.Counts.Documents, InstanceCount: view.Counts.Instances,
		ContextCount: view.Counts.Contexts, Diagnostics: len(view.Diagnostics),
		BackURL: backURL,
	}
	if dividends, dividendErr := s.store.LoadDividendView(r.Context(), view.Metadata.CIK); dividendErr == nil {
		data.Dividends = dividends
		data.HasDividends = len(dividends.Events) > 0 || len(dividends.Observations) > 0
	} else if !errors.Is(dividendErr, os.ErrNotExist) {
		s.writeError(w, dividendErr)

		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	component := detailPage(data)
	if isWorkspaceRequest(r) {
		component = detailPageBody(data)
	}

	if err := component.Render(r.Context(), w); err != nil {
		s.writeError(w, err)
	}
}

func validReturnURL(value string) bool {
	return strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") &&
		!strings.ContainsAny(value, "\r\n")
}

// rawInspector renders the optional raw filing inspection page.
