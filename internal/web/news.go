package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"wingman.com/fetch-ecb/internal/edgar"
	"wingman.com/fetch-ecb/internal/eventbus"
	"wingman.com/fetch-ecb/internal/news"
	"wingman.com/fetch-ecb/internal/pollingworker"
)

// NewsServer serves the small, stream-backed EDGAR news page.
type NewsServer struct {
	store     *news.Store
	template  *template.Template
	logger    *slog.Logger
	publisher eventbus.Publisher
}

// NewNewsServer creates a news page backed by a disposable read model.
func NewNewsServer(store *news.Store, publisher eventbus.Publisher,
	logger *slog.Logger,
) *NewsServer {
	return &NewsServer{
		store:     store,
		logger:    logger,
		publisher: publisher,
		template: template.Must(template.New("news").Funcs(template.FuncMap{
			"shortAccession": shortAccession,
		}).ParseFS(templateFiles, "templates/news.html")),
	}
}

func (s *NewsServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/":
		s.index(w, r)
	case "/assets/news.css":
		s.asset(w, "assets/news.css", "text/css")
	case "/assets/htmx.min.js":
		s.asset(w, "assets/htmx-4.0.0.min.js", "text/javascript")
	default:
		if strings.HasPrefix(r.URL.Path, "/actions/download/") {
			s.requestDownload(w, r)
		} else {
			http.NotFound(w, r)
		}
	}
}

func (s *NewsServer) index(w http.ResponseWriter, r *http.Request) {
	filter := news.Filter{
		Query:     strings.TrimSpace(r.URL.Query().Get("q")),
		CIK:       strings.TrimSpace(r.URL.Query().Get("cik")),
		FormType:  strings.TrimSpace(r.URL.Query().Get("form")),
		Selection: strings.TrimSpace(r.URL.Query().Get("selection")),
		From:      strings.TrimSpace(r.URL.Query().Get("from")),
		To:        strings.TrimSpace(r.URL.Query().Get("to")),
		Limit:     100,
	}

	items, err := s.store.List(r.Context(), filter)
	if err != nil {
		http.Error(w, "could not read news", http.StatusInternalServerError)

		if s.logger != nil {
			s.logger.Error("read news", "error", err)
		}

		return
	}

	data := struct {
		Items          []news.Item
		Filter         news.Filter
		SupportedForms []string
	}{
		Items: items, Filter: filter,
		SupportedForms: edgar.SupportedFormTypes(),
	}

	templateName := "news-page"
	if strings.EqualFold(r.Header.Get("Hx-Request"), "true") {
		templateName = "news-feed"
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if err := s.template.ExecuteTemplate(w, templateName, data); err != nil &&
		s.logger != nil {
		s.logger.Error("render news", "error", err)
	}
}

func (s *NewsServer) requestDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

		return
	}

	if s.publisher == nil {
		http.Error(w, "event publisher unavailable",
			http.StatusServiceUnavailable)

		return
	}

	accession := strings.TrimPrefix(r.URL.Path, "/actions/download/")
	if accession == "" || strings.Contains(accession, "/") {
		http.NotFound(w, r)

		return
	}

	item, err := s.store.Get(r.Context(), accession)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)

		return
	}

	if err != nil {
		http.Error(w, "could not read filing", http.StatusInternalServerError)

		return
	}

	selected := true
	event := pollingworker.FilingEvent{
		SchemaVersion: 1, EventID: "filing.requested:" + item.AccessionNumber,
		AccessionNumber: item.AccessionNumber, CIK: item.CIK,
		CompanyName: item.CompanyName, FormType: item.FormType,
		FilingDate: item.FilingDate, FilingPath: item.FilingPath,
		FilingURL: item.FilingURL, SourceFeed: item.SourceFeed,
		Timestamp: time.Now().UTC(), Selected: &selected,
		SelectionReason: "requested from the news feed",
		SelectionMode:   "on-demand", SelectionSet: item.SelectionSet,
	}

	payload, err := json.Marshal(event)
	if err != nil {
		http.Error(w, "could not encode request", http.StatusInternalServerError)

		return
	}

	if err := s.publisher.Publish(context.WithoutCancel(r.Context()),
		eventbus.SubjectFor(eventbus.SelectedSubject, event.FormType),
		event.EventID, payload); err != nil {
		http.Error(w, "could not queue filing", http.StatusServiceUnavailable)

		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte("<span class=\"selection selected\">queued</span>"))
}

func (s *NewsServer) asset(w http.ResponseWriter, path, contentType string) {
	data, err := templateFiles.ReadFile(path)
	if err != nil {
		http.Error(w, "asset not found", http.StatusNotFound)

		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

func shortAccession(accession string) string {
	if len(accession) <= 10 {
		return accession
	}

	return accession[len(accession)-10:]
}
