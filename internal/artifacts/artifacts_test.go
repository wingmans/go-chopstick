package artifacts

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wingman.com/chopstick/internal/edgar"
)

func TestExtractDecodesGraphicAndCleanUsesManifest(t *testing.T) {
	t.Parallel()

	// Keep artifact tests self-contained. Large filings under data/ are for
	// optional manual inspection and must not become test dependencies.
	directory := t.TempDir()
	source := filepath.Join(directory, "filing.txt")

	content := "plainbegin 644 pixel.jpg\n#04)#\n`\nend\n"
	if err := os.WriteFile(source, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	filing := &edgar.ParsedFiling{
		Metadata: edgar.FilingMetadata{CIK: "0000000001", Accession: "test"},
		Documents: []edgar.SubmissionDocument{{
			Type: "GRAPHIC", Filename: "pixel.jpg", ContentOffset: 5,
			ContentLength: int64(len(content) - 5),
		}},
	}
	destination := filepath.Join(directory, "artifacts")

	manifest, err := Extract(t.Context(), filing, source, destination,
		Selection{All: true})
	if err != nil {
		t.Fatal(err)
	}

	if manifest.TotalBytes != 3 {
		t.Fatalf("decoded bytes = %d", manifest.TotalBytes)
	}

	decoded, err := os.ReadFile(filepath.Join(destination, "pixel.jpg"))
	if err != nil {
		t.Fatal(err)
	}

	if string(decoded) != "ABC" {
		t.Fatalf("decoded content = %q", decoded)
	}

	handler, err := Viewer(destination, "0000000001", "test")
	if err != nil {
		t.Fatal(err)
	}

	index := httptest.NewRecorder()
	handler.ServeHTTP(index, httptest.NewRequestWithContext(t.Context(),
		http.MethodGet, "/", nil))

	if index.Code != http.StatusOK ||
		!strings.Contains(index.Body.String(), "pixel.jpg") ||
		!strings.Contains(index.Body.String(), "<img") ||
		!strings.Contains(index.Body.String(), `id="progress"`) ||
		!strings.Contains(index.Body.String(), `id="document-search"`) ||
		!strings.Contains(index.Body.String(), `id="type-filter"`) ||
		!strings.Contains(index.Body.String(), `id="graphics-only"`) ||
		!strings.Contains(index.Body.String(), `id="fit-image"`) ||
		!strings.Contains(index.Body.String(),
			`href="/viewer-assets/viewer.css"`) ||
		!strings.Contains(index.Body.String(),
			`src="/viewer-assets/viewer.js"`) {
		t.Fatalf("unexpected viewer index: status=%d body=%s",
			index.Code, index.Body.String())
	}

	css := httptest.NewRecorder()
	handler.ServeHTTP(css, httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/viewer-assets/viewer.css", nil))

	if css.Code != http.StatusOK ||
		!strings.HasPrefix(css.Header().Get("Content-Type"), "text/css") ||
		!strings.Contains(css.Body.String(), ".document.selected") {
		t.Fatalf("unexpected viewer CSS: status=%d content-type=%q body=%s",
			css.Code, css.Header().Get("Content-Type"), css.Body.String())
	}

	javascript := httptest.NewRecorder()
	handler.ServeHTTP(javascript, httptest.NewRequestWithContext(t.Context(),
		http.MethodGet,
		"/viewer-assets/viewer.js", nil))

	if javascript.Code != http.StatusOK ||
		!strings.Contains(javascript.Header().Get("Content-Type"),
			"javascript") ||
		!strings.Contains(javascript.Body.String(),
			`event.key === "ArrowRight"`) {
		t.Fatalf("unexpected viewer JS: status=%d content-type=%q body=%s",
			javascript.Code, javascript.Header().Get("Content-Type"),
			javascript.Body.String())
	}

	file := httptest.NewRecorder()
	handler.ServeHTTP(file,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet,
			"/files/pixel.jpg", nil))

	if file.Code != http.StatusOK || file.Body.String() != "ABC" {
		t.Fatalf("unexpected viewer file: status=%d body=%q",
			file.Code, file.Body.String())
	}

	if err := Clean(destination, "0000000001", "test"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("artifact directory still exists: %v", err)
	}
}

func TestDecodeUUToleratesTrimmedTrailingPadding(t *testing.T) {
	t.Parallel()

	var decoded strings.Builder

	encoded := "begin 644 pixel.bin\n#04)\n\"04\n!0\n#\nend\n"
	if err := decodeUU(strings.NewReader(encoded),
		&decoded); err != nil {
		t.Fatal(err)
	}

	want := "AB@A@@\x00\x00\x00"
	if decoded.String() != want {
		t.Fatalf("decoded content = %q, want %q", decoded.String(), want)
	}
}
