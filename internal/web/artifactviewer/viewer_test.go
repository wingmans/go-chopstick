package artifactviewer

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wingman.com/chopstick/internal/artifacts"
)

func TestViewerServesBundleAndAssets(t *testing.T) {
	directory := t.TempDir()
	manifest := artifacts.Manifest{
		GeneratedBy: "chopstick-edgar-artifacts",
		CIK:         "0000000001",
		Accession:   "test",
		Documents: []artifacts.ManifestDocument{{
			Type: "GRAPHIC", Filename: "pixel.jpg", Bytes: 3,
		}},
	}

	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		filepath.Join(directory, artifacts.ManifestName), payload, 0o600,
	); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(
		filepath.Join(directory, "pixel.jpg"), []byte("ABC"), 0o600,
	); err != nil {
		t.Fatal(err)
	}

	handler, err := Viewer(directory, manifest.CIK, manifest.Accession)
	if err != nil {
		t.Fatal(err)
	}

	assertViewerIndex(t, handler)
	assertViewerAsset(t, handler, "/viewer-assets/viewer.css",
		"text/css", ".document.selected")
	assertViewerAsset(t, handler, "/viewer-assets/viewer.js",
		"javascript", `event.key === "ArrowRight"`)
	assertViewerFile(t, handler)
}

func assertViewerIndex(t *testing.T, handler http.Handler) {
	t.Helper()

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/", nil,
	))

	for _, want := range []string{
		"pixel.jpg", "<img", `id="progress"`, `id="document-search"`,
		`id="type-filter"`, `id="graphics-only"`, `id="fit-image"`,
		`href="viewer-assets/viewer.css"`,
		`src="viewer-assets/viewer.js"`,
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("viewer index missing %q: %s", want,
				response.Body.String())
		}
	}

	if response.Code != http.StatusOK {
		t.Fatalf("viewer index status = %d", response.Code)
	}
}

func assertViewerAsset(t *testing.T, handler http.Handler, path,
	contentType, content string,
) {
	t.Helper()

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, path, nil,
	))

	if response.Code != http.StatusOK ||
		!strings.Contains(response.Header().Get("Content-Type"), contentType) ||
		!strings.Contains(response.Body.String(), content) {
		t.Fatalf("asset %s failed: status=%d content-type=%q body=%s",
			path, response.Code, response.Header().Get("Content-Type"),
			response.Body.String())
	}
}

func assertViewerFile(t *testing.T, handler http.Handler) {
	t.Helper()

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/files/pixel.jpg", nil,
	))

	if response.Code != http.StatusOK || response.Body.String() != "ABC" ||
		response.Header().Get("Content-Security-Policy") != "sandbox" ||
		response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("viewer file failed: status=%d body=%q", response.Code,
			response.Body.String())
	}
}
