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
	"math/big"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"wingman.com/fetch-ecb/internal/constituents"
	"wingman.com/fetch-ecb/internal/edgar"
	"wingman.com/fetch-ecb/internal/filingview"
	"wingman.com/fetch-ecb/internal/xerr"
)

//go:embed index.html detail.html company.html
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

type dashboardData struct {
	FilingCount int             `json:"filing_count"`
	Company     string          `json:"company"`
	CIK         string          `json:"cik"`
	Query       string          `json:"query"`
	SetName     string          `json:"set_name"`
	SetAsOf     string          `json:"set_as_of"`
	Filings     []filingSummary `json:"filings"`
}

type companyPageData struct {
	History filingview.CompanyHistory
	Ticker  string
}

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
		}).ParseFS(templateFiles, "*.html")),
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

func formatFactValue(value filingview.FactValue, unit string) string {
	if value.Nil || strings.TrimSpace(value.Value) == "" {
		return "-"
	}

	number := strings.ReplaceAll(strings.TrimSpace(value.Value), ",", "")

	rational, ok := new(big.Rat).SetString(number)
	if !ok {
		return value.Value
	}

	currency := strings.Contains(strings.ToLower(unit), "usd")
	perShare := strings.Contains(strings.ToLower(unit), "pershare") ||
		strings.Contains(strings.ToLower(unit), "/shares")

	formatted := compactNumber(rational)
	if !strings.ContainsAny(formatted, "KMBT") && (currency || perShare) {
		formatted = trimDecimal(rational.FloatString(2))
	}

	if currency || perShare {
		if trimmed, ok := strings.CutPrefix(formatted, "("); ok {
			formatted = "($" + strings.TrimSuffix(trimmed, ")") + ")"
		} else {
			formatted = "$" + formatted
		}
	}

	return formatted
}

func compactNumber(value *big.Rat) string {
	negative := value.Sign() < 0
	abs := new(big.Rat).Abs(value)
	suffix := ""

	divisor := big.NewRat(1, 1)
	for _, scale := range []struct {
		threshold int64
		suffix    string
	}{
		{1_000_000_000_000, "T"},
		{1_000_000_000, "B"},
		{1_000_000, "M"},
		{1_000, "K"},
	} {
		if abs.Cmp(big.NewRat(scale.threshold, 1)) >= 0 {
			divisor = big.NewRat(scale.threshold, 1)
			suffix = scale.suffix

			break
		}
	}

	formatted := trimDecimal(new(big.Rat).Quo(abs, divisor).FloatString(1)) + suffix
	if negative {
		return "(" + formatted + ")"
	}

	return formatted
}

func trimDecimal(value string) string {
	value = strings.TrimRight(value, "0")
	value = strings.TrimRight(value, ".")

	return value
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.logger != nil {
		s.logger.Debug("local dashboard request", "method", r.Method, "url", r.URL.String())
	}

	switch {
	case r.URL.Path == "/":
		_ = s.template.ExecuteTemplate(w, "index.html", nil)
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

	data := companyPageData{History: history, Ticker: member.Ticker}
	if err := s.template.ExecuteTemplate(w, "company.html", data); err != nil {
		s.writeError(w, err)
	}
}

func (s *Server) loadCompanyHistory(cik string) (filingview.CompanyHistory, error) {
	views := make([]filingview.View, 0)

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

		if canonicalCIK(view.Metadata.CIK) == cik {
			views = append(views, view)
		}

		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return filingview.CompanyHistory{
			CIK: "", Company: "", Years: []string{},
			Statements: []filingview.HistoryStatement{},
		}, nil
	}

	if err != nil {
		return filingview.CompanyHistory{}, err
	}

	return filingview.BuildCompanyHistory(views, cik)
}

func (s *Server) apiFilings(w http.ResponseWriter, r *http.Request) {
	filings, err := s.loadSummaries()
	if err != nil {
		s.writeError(w, err)

		return
	}

	cik := strings.TrimSpace(r.URL.Query().Get("cik"))
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	formType := strings.TrimSpace(r.URL.Query().Get("form_type"))

	year := strings.TrimSpace(r.URL.Query().Get("year"))
	if cik == "" && query == "" && formType == "" && year == "" {
		s.writeJSON(w, dashboardData{
			FilingCount: 0, Company: "", CIK: "", Query: "",
			SetName: s.setName, SetAsOf: s.setAsOf,
			Filings: []filingSummary{},
		})

		return
	}

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

	s.writeJSON(w, dashboardData{
		FilingCount: len(filtered), Company: company, CIK: selectedCIK,
		Query: query, SetName: s.setName, SetAsOf: s.setAsOf, Filings: filtered,
	})
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
		Filing: filing, Summary: view.Summary,
		Statements: []filingview.StatementView{
			view.Statements.Income, view.Statements.Balance, view.Statements.CashFlow,
		},
		Ratios: view.Ratios, Facts: view.Counts.Facts,
		DocumentCount: view.Counts.Documents, InstanceCount: view.Counts.Instances,
		ContextCount: view.Counts.Contexts, Diagnostics: len(view.Diagnostics),
		BackURL: backURL,
	}

	if err := s.template.ExecuteTemplate(w, "detail.html", data); err != nil {
		s.writeError(w, err)
	}
}

type detailData struct {
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

func validReturnURL(value string) bool {
	return strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") &&
		!strings.ContainsAny(value, "\r\n")
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
				Ticker:     s.memberByCIK[canonicalCIK(view.Metadata.CIK)].Ticker,
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
