package artifacts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wingman.com/chopstick/internal/edgar/parser"
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

	filing := &parser.ParsedFiling{
		Metadata: parser.FilingMetadata{CIK: "0000000001", Accession: "test"},
		Documents: []parser.SubmissionDocument{{
			Type: "GRAPHIC", Filename: "pixel.jpg", ContentOffset: 5,
			ContentLength: int64(len(content) - 5),
		}},
	}
	destination := filepath.Join(directory, "artifacts")

	if exists, err := BundleExists(destination); err != nil || exists {
		t.Fatalf("bundle before extraction: exists=%t error=%v", exists, err)
	}

	manifest, err := Extract(t.Context(), filing, source, destination,
		Selection{All: true})
	if err != nil {
		t.Fatal(err)
	}

	if manifest.TotalBytes != 3 {
		t.Fatalf("decoded bytes = %d", manifest.TotalBytes)
	}

	if exists, err := BundleExists(destination); err != nil || !exists {
		t.Fatalf("bundle after extraction: exists=%t error=%v", exists, err)
	}

	decoded, err := os.ReadFile(filepath.Join(destination, "pixel.jpg"))
	if err != nil {
		t.Fatal(err)
	}

	if string(decoded) != "ABC" {
		t.Fatalf("decoded content = %q", decoded)
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
