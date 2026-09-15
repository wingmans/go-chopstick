// Package filingweb serves the local parsed EDGAR dataset.
package filingweb

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"wingman.com/fetch-ecb/internal/edgar"
	"wingman.com/fetch-ecb/internal/filingview"
	"wingman.com/fetch-ecb/internal/xerr"
)

//go:embed index.html detail.html
var templateFiles embed.FS

type Server struct {
	parsedDir string
	template  *template.Template
	logger    *slog.Logger
}

type filingSummary struct {
	CIK        string `json:"cik"`
	Company    string `json:"company"`
	Accession  string `json:"accession"`
	FormType   string `json:"form_type"`
	FilingDate string `json:"filing_date"`
	ReportDate string `json:"report_date"`
	Status     string `json:"status"`
	Facts      int    `json:"facts"`
}

type dashboardData struct {
	FilingCount int             `json:"filing_count"`
	Company     string          `json:"company"`
	CIK         string          `json:"cik"`
	Filings     []filingSummary `json:"filings"`
}

func NewServer(parsedDir string, logger *slog.Logger) *Server {
	return &Server{parsedDir: parsedDir, logger: logger, template: template.Must(template.ParseFS(templateFiles, "*.html"))}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.logger != nil {
		s.logger.Debug("local dashboard request", "method", r.Method, "url", r.URL.String())
	}

	switch {
	case r.URL.Path == "/":
		_ = s.template.ExecuteTemplate(w, "index.html", nil)
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

func (s *Server) apiFilings(w http.ResponseWriter, r *http.Request) {
	filings, err := s.loadSummaries()
	if err != nil {
		s.writeError(w, err)

		return
	}

	cik := strings.TrimSpace(r.URL.Query().Get("cik"))
	formType := strings.TrimSpace(r.URL.Query().Get("form_type"))
	filtered := make([]filingSummary, 0, len(filings))
	company := ""

	for _, filing := range filings {
		if cik != "" && canonicalCIK(filing.CIK) != canonicalCIK(cik) || formType != "" && filing.FormType != formType {
			continue
		}

		if company == "" {
			company = filing.Company
		}

		filtered = append(filtered, filing)
	}

	s.writeJSON(w, dashboardData{FilingCount: len(filtered), Company: company, CIK: cik, Filings: filtered})
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
		Filing: filing, Summary: view.Summary,
		Statements: []filingview.StatementView{
			view.Statements.Income, view.Statements.Balance, view.Statements.CashFlow,
		},
		Ratios: view.Ratios, Facts: view.Counts.Facts,
		DocumentCount: view.Counts.Documents, InstanceCount: view.Counts.Instances,
		ContextCount: view.Counts.Contexts, Diagnostics: len(view.Diagnostics),
	}

	if err := s.template.ExecuteTemplate(w, "detail.html", data); err != nil {
		s.writeError(w, err)
	}
}

type detailData struct {
	Filing        *edgar.ParsedFiling
	Summary       []filingview.SummaryGroup
	Statements    []filingview.StatementView
	Ratios        []filingview.RatioSeries
	Facts         int
	DocumentCount int
	InstanceCount int
	ContextCount  int
	Diagnostics   int
}

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

func (s *Server) sourcePath(filing *edgar.ParsedFiling) (string, error) {
	var source string

	switch filing.SourceBase {
	case "absolute":
		source = filing.SourcePath
	case "filings":
		source = filepath.Join(filepath.Dir(s.parsedDir), "filings", filepath.FromSlash(filing.SourcePath))
	default:
		return "", os.ErrNotExist
	}

	if !withinDirectory(filepath.Dir(s.parsedDir), source) {
		return "", os.ErrNotExist
	}

	return source, nil
}

func (s *Server) loadView(parts []string) (filingview.View, error) {
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return filingview.View{}, os.ErrNotExist
	}

	cik := canonicalCIK(parts[0])
	if cik == "" {
		return filingview.View{}, os.ErrNotExist
	}

	path := filepath.Join(s.parsedDir, cik, parts[1], "filing-view.json")
	if !withinDirectory(s.parsedDir, path) {
		return filingview.View{}, os.ErrNotExist
	}

	return filingview.Load(path)
}

func (s *Server) loadFiling(parts []string) (*edgar.ParsedFiling, error) {
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, os.ErrNotExist
	}

	cik := canonicalCIK(parts[0])
	if cik == "" {
		return nil, os.ErrNotExist
	}

	path := filepath.Join(s.parsedDir, cik, parts[1], "filing.json")
	if !withinDirectory(s.parsedDir, path) {
		return nil, os.ErrNotExist
	}

	filing, err := edgar.LoadParsedFiling(path)

	return filing, err
}

func (s *Server) loadSummaries() ([]filingSummary, error) {
	var result []filingSummary

	err := filepath.Walk(s.parsedDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() || info.Name() != "filing-view.json" {
			return nil
		}

		view, err := filingview.Load(path)
		if err != nil {
			return fmt.Errorf("load %s: %w", path, err)
		}

		result = append(result,
			filingSummary{
				CIK:        view.Metadata.CIK,
				Company:    view.Metadata.Company,
				Accession:  view.Metadata.Accession,
				FormType:   view.Metadata.FormType,
				FilingDate: view.Metadata.FilingDate,
				ReportDate: view.Metadata.ReportDate,
				Status:     view.Counts.Status,
				Facts:      view.Counts.Facts,
			})

		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return []filingSummary{}, nil
	}

	if err != nil {
		return nil, err
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].FilingDate != result[j].FilingDate {
			return result[i].FilingDate > result[j].FilingDate
		}

		return result[i].Accession > result[j].Accession
	})

	return result, nil
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

// canonicalCIK preserves significant digits while normalizing leading padding.
func canonicalCIK(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 10 {
		return ""
	}

	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return ""
		}
	}

	value = strings.TrimLeft(value, "0")
	if value == "" {
		value = "0"
	}

	return strings.Repeat("0", 10-len(value)) + value
}

func withinDirectory(directory, path string) bool {
	directory, err := filepath.Abs(directory)
	if err != nil {
		return false
	}

	path, err = filepath.Abs(path)
	if err != nil {
		return false
	}

	relative, err := filepath.Rel(directory, path)

	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
