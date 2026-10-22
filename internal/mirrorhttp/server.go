// Package mirrorhttp exposes the transport-independent mirror API over HTTP.
package mirrorhttp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"wingman.com/fetch-ecb/internal/mirror"
)

// Server is a thin HTTP adapter around mirror.API.
type Server struct {
	api mirror.API
	mux *http.ServeMux
}

// New creates a mirror HTTP adapter with no storage dependencies.
func New(api mirror.API) *Server {
	server := &Server{api: api, mux: http.NewServeMux()}
	server.mux.HandleFunc("GET /health", server.health)
	server.mux.HandleFunc("POST /v1/submissions:ensure", server.ensure)
	server.mux.HandleFunc("GET /v1/submissions", server.search)
	server.mux.HandleFunc("GET /v1/submissions/{accession}", server.get)
	server.mux.HandleFunc("GET /v1/submissions/{accession}/content",
		server.content)

	return server
}

// ServeHTTP dispatches one mirror request.
func (s *Server) ServeHTTP(writer http.ResponseWriter,
	request *http.Request,
) {
	s.mux.ServeHTTP(writer, request)
}

func (s *Server) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ensure(writer http.ResponseWriter, request *http.Request) {
	defer func() { _ = request.Body.Close() }()

	decoder := json.NewDecoder(io.LimitReader(request.Body, 1<<20))
	decoder.DisallowUnknownFields()

	var ref mirror.SubmissionRef
	if err := decoder.Decode(&ref); err != nil {
		writeError(writer, fmt.Errorf("decode request: %w", err))

		return
	}

	value, err := s.api.Ensure(request.Context(), ref)
	if err != nil {
		writeError(writer, err)

		return
	}

	writeJSON(writer, http.StatusOK, value)
}

func (s *Server) get(writer http.ResponseWriter, request *http.Request) {
	value, err := s.api.Get(request.Context(), request.PathValue("accession"))
	if err != nil {
		writeError(writer, err)

		return
	}

	writeJSON(writer, http.StatusOK, value)
}

func (s *Server) search(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	filter := mirror.SearchFilter{
		CIK: query.Get("cik"), FormType: query.Get("form"),
		Cursor: query.Get("cursor"),
	}

	var err error
	if raw := strings.TrimSpace(query.Get("filing_year")); raw != "" {
		filter.FilingYear, err = strconv.Atoi(raw)
		if err != nil {
			writeError(writer, errors.New("filing_year must be an integer"))

			return
		}
	}

	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		filter.Limit, err = strconv.Atoi(raw)
		if err != nil {
			writeError(writer, errors.New("limit must be an integer"))

			return
		}
	}

	page, err := s.api.Search(request.Context(), filter)
	if err != nil {
		writeError(writer, err)

		return
	}

	writeJSON(writer, http.StatusOK, page)
}

func (s *Server) content(writer http.ResponseWriter, request *http.Request) {
	content, err := s.api.Open(request.Context(),
		request.PathValue("accession"))
	if err != nil {
		writeError(writer, err)

		return
	}
	defer func() { _ = content.Reader.Close() }()

	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.Header().Set("Content-Length",
		strconv.FormatInt(content.Submission.LogicalBytes, 10))
	writer.WriteHeader(http.StatusOK)
	_, _ = io.Copy(writer, content.Reader)
}

func writeError(writer http.ResponseWriter, err error) {
	status := http.StatusInternalServerError

	switch {
	case errors.Is(err, mirror.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, mirror.ErrInvalidReference):
		status = http.StatusBadRequest
	case errors.Is(err, mirror.ErrUnavailable):
		status = http.StatusServiceUnavailable
	case errors.Is(err, mirror.ErrCorruptObject):
		status = http.StatusInternalServerError
	default:
		if strings.Contains(err.Error(), "must be an integer") ||
			strings.Contains(err.Error(), "decode request") {
			status = http.StatusBadRequest
		}
	}

	writeJSON(writer, status, map[string]string{"error": err.Error()})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)

	if err := json.NewEncoder(writer).Encode(value); err != nil {
		return
	}
}
