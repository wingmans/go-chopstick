package web

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"wingman.com/chopstick/internal/parser"
)

func (s *Server) rawAPIFiling(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/debug/raw/api/filings/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)

		return
	}

	filing, err := s.loadRawFiling(r, parts[0], parts[1])
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

// detail handles requests to the /filings/{accession}/ endpoint, rendering
// the normalized detail view of the filing.
func (s *Server) rawInspector(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path,
		"/debug/raw/filings/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)

		return
	}

	filing, err := s.loadRawFiling(r, parts[0], parts[1])
	if errors.Is(err, os.ErrNotExist) {
		s.writeRawUnavailable(w, r)

		return
	}

	if err != nil {
		s.writeError(w, err)

		return
	}

	data := rawInspectorData{
		Page: pageChrome{
			Title: "Raw Filing Inspector", BodyClass: "raw-page",
			ActiveSection: "filings", UseHTMX: true,
		},
		Filing: filing,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	component := rawInspectorPage(data)
	if isWorkspaceRequest(r) {
		component = rawInspectorPageBody(data)
	}

	if err := component.Render(r.Context(), w); err != nil {
		s.writeError(w, err)
	}
}

// rawDocument serves one source document from the optional raw inspector.
func (s *Server) rawDocument(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path,
		"/debug/raw/filings/"), "/")
	if len(parts) != 4 || parts[2] != "documents" {
		http.NotFound(w, r)

		return
	}

	filing, err := s.loadRawFiling(r, parts[0], parts[1])
	if errors.Is(err, os.ErrNotExist) {
		s.writeRawUnavailable(w, r)

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

	source, err := s.rawStore.SourcePath(r.Context(), filing)
	if errors.Is(err, os.ErrNotExist) {
		s.writeRawUnavailable(w, r)

		return
	}

	if err != nil {
		s.writeError(w, err)

		return
	}

	file, err := os.Open(source) // #nosec G703 -- SourcePath validates the
	// resolved path is inside the configured filings directory.
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

func (s *Server) loadRawFiling(r *http.Request, cik, accession string) (
	*parser.ParsedFiling, error,
) {
	if s.rawStore == nil {
		return nil, os.ErrNotExist
	}

	return s.rawStore.LoadFiling(r.Context(), cik, accession)
}

func (s *Server) writeRawUnavailable(w http.ResponseWriter, r *http.Request) {
	if s.logger != nil {
		s.logger.Debug("raw filing unavailable", "path", r.URL.Path)
	}

	http.Error(w, "raw filing unavailable", http.StatusNotFound)
}
