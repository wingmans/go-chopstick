package web

import (
	"html/template"
	"log/slog"
	"net/http"
	"strings"

	"wingman.com/fetch-ecb/internal/news"
)

// NewsServer serves the small, stream-backed EDGAR news page.
type NewsServer struct {
	store    *news.Store
	template *template.Template
	logger   *slog.Logger
}

// NewNewsServer creates a news page backed by a disposable read model.
func NewNewsServer(store *news.Store, logger *slog.Logger) *NewsServer {
	return &NewsServer{
		store:  store,
		logger: logger,
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
		http.NotFound(w, r)
	}
}

func (s *NewsServer) index(w http.ResponseWriter, r *http.Request) {
	filter := news.Filter{
		CIK:      strings.TrimSpace(r.URL.Query().Get("cik")),
		FormType: strings.TrimSpace(r.URL.Query().Get("form")),
		From:     strings.TrimSpace(r.URL.Query().Get("from")),
		To:       strings.TrimSpace(r.URL.Query().Get("to")),
		Limit:    100,
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
		Items  []news.Item
		Filter news.Filter
	}{Items: items, Filter: filter}

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
