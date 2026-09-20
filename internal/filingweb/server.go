// Package filingweb serves the local parsed EDGAR dataset.
package filingweb

import (
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"wingman.com/fetch-ecb/internal/constituents"
	"wingman.com/fetch-ecb/internal/edgar"
	"wingman.com/fetch-ecb/internal/filingview"
	"wingman.com/fetch-ecb/internal/xerr"
)

//go:embed templates/*.html assets/chopstick.svg assets/filingweb.css assets/htmx-4.0.0.min.js
var templateFiles embed.FS

type Server struct {
	parsedDir   string
	template    *template.Template
	logger      *slog.Logger
	setName     string
	setAsOf     string
	memberByCIK map[string]constituents.Member
	memberByKey map[string][]string
}

// NewServer creates a server instance with default configuration.
func NewServer(parsedDir string, logger *slog.Logger) *Server {
	server, err := NewConfiguredServer(parsedDir, "", "", logger)
	if err != nil {
		panic(err)
	}

	return server
}

// NewConfiguredServer creates a dashboard with an optional constituent set.
// The set is only a human-name lookup; CIK and accession remain filing keys.
func NewConfiguredServer(parsedDir, setDir, setName string, logger *slog.Logger) (*Server, error) {
	server := &Server{
		parsedDir: parsedDir,
		logger:    logger,
		template: template.Must(template.New("filingweb").Funcs(template.FuncMap{
			"formatFact": formatFactValue,
			"add":        add,
			"mul":        mul,
		}).ParseFS(templateFiles, "templates/*.html")),
		setName:     setName,
		setAsOf:     "",
		memberByCIK: map[string]constituents.Member{},
		memberByKey: map[string][]string{},
	}

	if setName == "" {
		return server, nil
	}

	set, err := constituents.LoadJSON(filepath.Join(setDir, setName+".json"))
	if err != nil {
		return nil, err
	}

	server.setAsOf = set.AsOf
	for _, member := range set.Members {
		cik := canonicalCIK(member.CIK)
		server.memberByCIK[cik] = member
		server.memberByKey[strings.ToLower(member.Ticker)] = append(
			server.memberByKey[strings.ToLower(member.Ticker)], cik)
		server.memberByKey[strings.ToLower(member.Name)] = append(
			server.memberByKey[strings.ToLower(member.Name)], cik)
	}

	return server, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.logger != nil {
		s.logger.Debug("local dashboard request", "method", r.Method, "url", r.URL.String())
	}

	switch {
	case r.URL.Path == "/":
		s.index(w, r)
	case r.URL.Path == "/assets/chopstick.svg":
		s.asset(w, r, "assets/chopstick.svg", "image/svg+xml")
	case r.URL.Path == "/assets/filingweb.css":
		s.asset(w, r, "assets/filingweb.css", "text/css")
	case r.URL.Path == "/assets/htmx.min.js":
		s.asset(w, r, "assets/htmx-4.0.0.min.js", "text/javascript")
	case strings.HasPrefix(r.URL.Path, "/companies/"):
		s.company(w, r)
	case strings.HasPrefix(r.URL.Path, "/filings/") && strings.Contains(r.URL.Path, "/documents/"):
		s.document(w, r)
	case strings.HasPrefix(r.URL.Path, "/filings/"):
		s.detail(w, r)
	case r.URL.Path == "/api/filings":
		s.apiFilings(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/filings/"):
		s.apiFiling(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) asset(w http.ResponseWriter, r *http.Request, path, contentType string) {
	data, err := templateFiles.ReadFile(path)
	if err != nil {
		http.NotFound(w, r)

		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := w.Write(data); err != nil && s.logger != nil {
		s.logger.Debug("write asset response failed", "error", err)
	}
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	data, err := s.dashboardData(r)
	if err != nil {
		s.writeError(w, err)

		return
	}

	templateName := "dashboard-page.html"
	if r.Header.Get("HX-Request") == "true" &&
		strings.Contains(r.Header.Get("HX-Target"), "#dashboard") {
		templateName = "dashboard.html"
	}

	if err := s.template.ExecuteTemplate(w, templateName, data); err != nil {
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

	history, err := s.loadCompanyHistory(cik)
	if errors.Is(err, os.ErrNotExist) || len(history.Years) == 0 {
		http.NotFound(w, r)

		return
	}

	if err != nil {
		s.writeError(w, err)

		return
	}

	member := s.memberByCIK[cik]

	title := history.Company
	if member.Ticker != "" {
		title = member.Ticker + " - " + title
	}

	data := companyPageData{
		Page:    pageChrome{Title: title, BodyClass: "company-page"},
		History: history,
		Ticker:  member.Ticker,
	}
	if err := s.template.ExecuteTemplate(w, "company-page.html", data); err != nil {
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

func (s *Server) apiFiling(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/filings/"), "/")

	filing, err := s.loadFiling(parts)
	if errors.Is(err, os.ErrNotExist) {
		http.NotFound(w, r)

		return
	}

	if err != nil {
		s.writeError(w, err)

		return
	}

	s.writeJSON(w, filing)
}

// detail handles requests to the /filings/{accession}/ endpoint, rendering the detailed view of the filing.
func (s *Server) detail(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/filings/"), "/")

	view, err := s.loadView(parts)
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

	filing := &edgar.ParsedFiling{
		SchemaVersion: edgar.ParsedSchemaVersion,
		ParserVersion: view.ParserVersion,
		SourcePath:    view.SourcePath,
		SourceBase:    "",
		SourceSHA256:  view.SourceSHA256,
		Metadata: edgar.FilingMetadata{
			Accession: view.Metadata.Accession, CIK: view.Metadata.CIK,
			FormType: view.Metadata.FormType, FilingDate: view.Metadata.FilingDate,
			ReportDate: view.Metadata.ReportDate,
			Filers:     []edgar.SubmissionFiler{{CIK: view.Metadata.CIK, Name: view.Metadata.Company, Header: []string{}}},
			Items:      []string{}, Header: []string{},
		},
		Documents: view.Documents, Instances: []edgar.XBRLInstance{},
		Diagnostics: []edgar.ParseDiagnostic{}, Status: view.Counts.Status,
	}
	data := detailData{
		Page: pageChrome{
			Title:     view.Metadata.FormType + " " + view.Metadata.Accession,
			BodyClass: "detail-page",
			UseHTMX:   true,
		},
		Filing: filing, Summary: view.Summary,
		Statements: []filingview.StatementView{
			view.Statements.Income, view.Statements.Balance, view.Statements.CashFlow,
		},
		Ratios: view.Ratios, Facts: view.Counts.Facts,
		DocumentCount: view.Counts.Documents, InstanceCount: view.Counts.Instances,
		ContextCount: view.Counts.Contexts, Diagnostics: len(view.Diagnostics),
		BackURL: backURL,
	}

	if err := s.template.ExecuteTemplate(w, "detail-page.html", data); err != nil {
		s.writeError(w, err)
	}
}

func validReturnURL(value string) bool {
	return strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") &&
		!strings.ContainsAny(value, "\r\n")
}

// document handles requests to view a specific document within a filing.
func (s *Server) document(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/filings/"), "/")
	if len(parts) != 4 || parts[2] != "documents" {
		http.NotFound(w, r)

		return
	}

	filing, err := s.loadFiling(parts[:2])
	if errors.Is(err, os.ErrNotExist) {
		http.NotFound(w, r)

		return
	}

	if err != nil {
		s.writeError(w, err)

		return
	}

	index, err := strconv.Atoi(parts[3])
	if err != nil || index < 0 || index >= len(filing.Documents) {
		http.NotFound(w, r)

		return
	}

	document := filing.Documents[index]

	source, err := s.sourcePath(filing)
	if err != nil {
		s.writeError(w, err)

		return
	}

	file, err := os.Open(source)
	if err != nil {
		s.writeError(w, err)

		return
	}
	defer func() { _ = file.Close() }()

	if document.ContentLength <= 0 {
		http.NotFound(w, r)

		return
	}

	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(document.Filename)))
	http.ServeContent(w, r, filepath.Base(document.Filename), time.Time{},
		io.NewSectionReader(file, document.ContentOffset, document.ContentLength))
}

func (s *Server) writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")

	data, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "could not encode response", http.StatusInternalServerError)

		return
	}

	if _, err := w.Write(append(data, '\n')); err != nil && s.logger != nil {
		s.logger.Debug("write JSON response failed", "error", err)
	}
}

func (s *Server) writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := "internal server error"
	structured := xerr.Wrap(xerr.Internal, "DASHBOARD_REQUEST_FAILED", message, err)

	if errors.Is(err, os.ErrNotExist) {
		status = http.StatusNotFound
		message = "filing not found"
		structured = xerr.Wrap(xerr.NotFound, "FILING_NOT_FOUND", message, err)
	}

	if s.logger != nil {
		s.logger.Error("local dashboard request failed", "error", structured, "code", structured.Code, "status", status)
	}

	http.Error(w, message, status)
}
