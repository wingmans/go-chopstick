package artifacts

import (
	"os"
	"path/filepath"
	"testing"

	"wingman.com/fetch-ecb/internal/edgar"
)

func TestExtractDecodesGraphicAndCleanUsesManifest(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	source := filepath.Join(directory, "filing.txt")
	content := "plainbegin 644 pixel.bin\n#04)#\n`\nend\n"
	if err := os.WriteFile(source, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	filing := &edgar.ParsedFiling{
		Metadata: edgar.FilingMetadata{CIK: "0000000001", Accession: "test"},
		Documents: []edgar.SubmissionDocument{{
			Type: "GRAPHIC", Filename: "pixel.bin", ContentOffset: 5,
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

	decoded, err := os.ReadFile(filepath.Join(destination, "pixel.bin"))
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
