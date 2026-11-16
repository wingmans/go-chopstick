package web

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"wingman.com/chopstick/internal/artifacts"
)

const artifactViewerRoute = "/viewer/"

// artifactViewer extracts one retained compound filing into its disposable
// artifact bundle and serves the existing document viewer from that bundle.
func (s *Server) artifactViewer(w http.ResponseWriter, r *http.Request) {
	cik, accession, basePath, ok := artifactViewerTarget(r.URL.Path)
	if !ok {
		http.NotFound(w, r)

		return
	}

	rootPath := strings.TrimSuffix(basePath, "/")
	if r.URL.Path == rootPath {
		w.Header().Set("Location", basePath)
		w.WriteHeader(http.StatusMovedPermanently)

		return
	}

	if !strings.HasPrefix(r.URL.Path, basePath) {
		http.NotFound(w, r)

		return
	}

	filing, err := s.loadRawFiling(r, cik, accession)
	if errors.Is(err, os.ErrNotExist) {
		s.writeRawUnavailable(w, r)

		return
	}

	if err != nil {
		s.writeError(w, err)

		return
	}

	source, err := s.rawStore.SourcePath(r.Context(), filing)
	if errors.Is(err, os.ErrNotExist) {
		s.writeRawUnavailable(w, r)

		return
	}

	if err != nil {
		s.writeError(w, err)

		return
	}

	destination := filepath.Join(s.artifactsDir, cik, accession)

	s.artifactMu.Lock()

	var exists bool

	exists, err = artifacts.BundleExists(destination)
	if err == nil && !exists {
		_, err = artifacts.Extract(r.Context(), filing, source, destination,
			artifacts.Selection{All: true})
	}
	s.artifactMu.Unlock()

	if err != nil {
		s.writeError(w, err)

		return
	}

	handler, err := artifacts.Viewer(destination, filing.Metadata.CIK,
		filing.Metadata.Accession)
	if err != nil {
		s.writeError(w, err)

		return
	}

	request := r.Clone(r.Context())
	request.URL.Path = "/" + strings.TrimPrefix(r.URL.Path, basePath)

	handler.ServeHTTP(w, request)
}

func artifactViewerTarget(path string) (string, string, string, bool) {
	parts := strings.SplitN(strings.TrimPrefix(path, artifactViewerRoute), "/", 3)
	if len(parts) < 2 {
		return "", "", "", false
	}

	cik := canonicalCIK(parts[0])

	accession := strings.TrimSpace(parts[1])
	if cik == "" || accession == "" || accession == "." || accession == ".." ||
		filepath.Base(accession) != accession {
		return "", "", "", false
	}

	basePath := artifactViewerRoute + cik + "/" + accession + "/"

	return cik, accession, basePath, true
}
