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

	filing, err := s.loadFiling(parts)
	if errors.Is(err, os.ErrNotExist) {
		http.NotFound(w, r)

		return
	}

	if err != nil {
		s.writeError(w, err)

		return
	}

	data := detailData{
		Filing: filing, Facts: []factRow{}, DocumentCount: len(filing.Documents),
		InstanceCount: len(filing.Instances), ContextCount: 0,
		Summary: []summaryGroup{},
	}

	data.Summary = financialSummary(filing)
	for _, instance := range filing.Instances {
		data.ContextCount += len(instance.Contexts)
		for _, fact := range instance.Facts {
			period, dimensions := contextDetails(instance.Contexts, fact.ContextRef)
			data.Facts = append(data.Facts, factRow{
				Concept: conceptLabel(fact.Concept.Local), Value: fact.Value, Context: fact.ContextRef,
				Unit: fact.UnitRef, Decimals: fact.Decimals, Nil: fact.Nil,
				Period: period, Dimensions: dimensions,
			})
		}
	}

	if err := s.template.ExecuteTemplate(w, "detail.html", data); err != nil {
		s.writeError(w, err)
	}
}

type detailData struct {
	Filing        *edgar.ParsedFiling
	Summary       []summaryGroup
	Facts         []factRow
	DocumentCount int
	InstanceCount int
	ContextCount  int
}

type summaryGroup struct {
	Title   string
	Periods []string
	Rows    []factSeries
}

type factSeries struct {
	Label   string
	Concept string
	Unit    string
	Values  map[string]factValue
}

type factValue struct {
	Value string
	Nil   bool
}

func financialSummary(filing *edgar.ParsedFiling) []summaryGroup {
	groupPeriods := map[string]map[string]string{}
	groupSeries := map[string]map[string]*factSeries{}

	for _, instance := range filing.Instances {
		contexts := make(map[string]edgar.FactContext, len(instance.Contexts))
		for _, context := range instance.Contexts {
			if len(context.Dimensions) == 0 {
				contexts[context.ID] = context
			}
		}

		for _, fact := range instance.Facts {
			if _, ok := summaryConceptLabel(fact.Concept.Local); !ok {
				continue
			}

			context, ok := contexts[fact.ContextRef]
			if !ok {
				continue
			}

			group, periodKey, periodLabel := classifyPeriod(context, filing.Metadata.ReportDate)
			if group == "" {
				continue
			}

			key := fact.Concept.Namespace + "\x00" + fact.Concept.Local + "\x00" + fact.UnitRef

			if groupPeriods[group] == nil {
				groupPeriods[group] = map[string]string{}
				groupSeries[group] = map[string]*factSeries{}
			}

			row, ok := groupSeries[group][key]
			if !ok {
				row = &factSeries{
					Label:   conceptLabel(fact.Concept.Local),
					Concept: fact.Concept.Local,
					Unit:    fact.UnitRef,
					Values:  map[string]factValue{},
				}
				groupSeries[group][key] = row
			}

			groupPeriods[group][periodKey] = periodLabel
			row.Values[periodKey] = factValue{Value: fact.Value, Nil: fact.Nil}
		}
	}

	order := []string{"Fiscal year", "Quarterly", "Year to date", "Instant"}
	groups := make([]summaryGroup, 0, len(groupPeriods))

	for _, title := range order {
		periods, ok := groupPeriods[title]
		if !ok {
			continue
		}

		periodKeys := make([]string, 0, len(periods))
		for key := range periods {
			periodKeys = append(periodKeys, key)
		}

		sort.Sort(sort.Reverse(sort.StringSlice(periodKeys)))

		rows := make([]factSeries, 0, len(groupSeries[title]))
		for _, row := range groupSeries[title] {
			rows = append(rows, *row)
		}

		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Label != rows[j].Label {
				return rows[i].Label < rows[j].Label
			}

			return rows[i].Unit < rows[j].Unit
		})

		groups = append(groups, summaryGroup{Title: title, Periods: periodKeys, Rows: rows})
	}

	return groups
}

func classifyPeriod(context edgar.FactContext, reportDate string) (string, string, string) {
	if context.Instant != "" {
		label := displayDate(context.Instant)

		return "Instant", label, label
	}

	start, startErr := parseDate(context.StartDate)

	end, endErr := parseDate(context.EndDate)
	if startErr != nil || endErr != nil || start.After(end) {
		return "", "", ""
	}

	fiscalEnd, err := parseDate(reportDate)
	if err != nil {
		fiscalEnd = end
	}

	months := (end.Year()-start.Year())*12 + int(end.Month()) - int(start.Month()) + 1
	fiscalYear := end.Year()

	if months >= 10 {
		label := fmt.Sprintf("FY%d", fiscalYear)

		return "Fiscal year", label, label
	}

	if months == 3 {
		fiscalStartMonth := fiscalEnd.Month()%12 + 1
		quarter := (int(start.Month())-int(fiscalStartMonth)+12)%12/3 + 1
		label := fmt.Sprintf("Q%d FY%d", quarter, fiscalYear)

		return "Quarterly", label, label
	}

	if months > 3 {
		label := fmt.Sprintf("YTD FY%d", fiscalYear)

		return "Year to date", label, label
	}

	return "", "", ""
}

func displayDate(value string) string {
	for _, layout := range []string{"2006-01-02", "20060102"} {
		date, err := time.Parse(layout, value)
		if err == nil {
			return date.Format("2006-01-02")
		}
	}

	return value
}

func parseDate(value string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02", "20060102"} {
		if date, err := time.Parse(layout, value); err == nil {
			return date, nil
		}
	}

	return time.Time{}, fmt.Errorf("invalid date %q", value)
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

type factRow struct {
	Concept    string
	Value      string
	Context    string
	Unit       string
	Decimals   string
	Nil        bool
	Period     string
	Dimensions int
}

func contextDetails(contexts []edgar.FactContext, id string) (string, int) {
	for _, context := range contexts {
		if context.ID != id {
			continue
		}

		period := context.Instant
		if period == "" && (context.StartDate != "" || context.EndDate != "") {
			period = context.StartDate + " to " + context.EndDate
		}

		return period, len(context.Dimensions)
	}

	return "", 0
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

		result = append(result,
			filingSummary{
				CIK:        filing.Metadata.CIK,
				Company:    company,
				Accession:  filing.Metadata.Accession,
				FormType:   filing.Metadata.FormType,
				FilingDate: filing.Metadata.FilingDate,
				ReportDate: filing.Metadata.ReportDate,
				Status:     filing.Status,
				Facts:      facts,
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
