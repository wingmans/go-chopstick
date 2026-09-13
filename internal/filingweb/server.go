// Package filingweb serves the local parsed EDGAR dataset.
package filingweb

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"wingman.com/fetch-ecb/internal/edgar"
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
		http.Error(w, err.Error(), http.StatusInternalServerError)

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
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	s.writeJSON(w, filing)
}

func (s *Server) detail(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/filings/"), "/")

	filing, err := s.loadFiling(parts)
	if errors.Is(err, os.ErrNotExist) {
		http.NotFound(w, r)

		return
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	data := detailData{Filing: filing}
	for _, instance := range filing.Instances {
		for _, fact := range instance.Facts {
			data.Facts = append(data.Facts, factRow{
				Concept: fact.Concept.Local, Value: fact.Value, Context: fact.ContextRef,
				Unit: fact.UnitRef, Decimals: fact.Decimals, Nil: fact.Nil,
			})
		}
	}

	if err := s.template.ExecuteTemplate(w, "detail.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type detailData struct {
	Filing *edgar.ParsedFiling
	Facts  []factRow
}

type factRow struct {
	Concept  string
	Value    string
	Context  string
	Unit     string
	Decimals string
	Nil      bool
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

		if info.IsDir() || info.Name() != "filing.json" {
			return nil
		}

		filing, err := edgar.LoadParsedFiling(path)
		if err != nil {
			return fmt.Errorf("load %s: %w", path, err)
		}

		company := ""
		if len(filing.Metadata.Filers) > 0 {
			company = filing.Metadata.Filers[0].Name
		}

		facts := 0
		for _, instance := range filing.Instances {
			facts += len(instance.Facts)
		}

		result = append(result, filingSummary{CIK: filing.Metadata.CIK, Company: company, Accession: filing.Metadata.Accession, FormType: filing.Metadata.FormType, FilingDate: filing.Metadata.FilingDate, ReportDate: filing.Metadata.ReportDate, Status: filing.Status, Facts: facts})

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
	_ = json.NewEncoder(w).Encode(value)
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
