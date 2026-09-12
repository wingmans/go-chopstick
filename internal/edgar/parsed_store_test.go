package edgar

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParsedFilingRoundTrip(t *testing.T) {
	source := writeSubmission(t, submissionText(testInstance, testInstanceFilename))

	filing, err := ParseSubmission(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()

	path, err := SaveParsedFiling(directory, filing)
	if err != nil {
		t.Fatal(err)
	}

	if path != filepath.Join(directory, "0000000123", "0000000123-24-000001", "filing.json") {
		t.Fatalf("unexpected destination %q", path)
	}

	loaded, err := LoadParsedFiling(path)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(filing, loaded) {
		t.Fatal("parsed data changed after persistence")
	}

	if matches, err := loaded.MatchesSource(t.Context(), source); err != nil || !matches {
		t.Fatalf("matches=%v err=%v", matches, err)
	}

	if err := os.WriteFile(source, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}

	if matches, err := loaded.MatchesSource(t.Context(), source); err != nil || matches {
		t.Fatalf("changed source matches=%v err=%v", matches, err)
	}

	loaded.ParserVersion = "old"
	if matches, err := loaded.MatchesSource(t.Context(), "does-not-exist"); err != nil || matches {
		t.Fatal("old parser version reused")
	}

	if _, err := SaveParsedFiling(directory, loaded); err == nil {
		t.Fatal("old parser version saved")
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 {
		t.Fatal("temporary files left behind")
	}
}

func TestParsedStoreFailedRenameCleansTemporaryFile(t *testing.T) {
	filing, err := ParseSubmission(t.Context(), writeSubmission(t, submissionText(testInstance, testInstanceFilename)))
	if err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()

	path, err := filing.ParsedPath(directory)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(path, 0o750); err != nil {
		t.Fatal(err)
	}

	if _, err := SaveParsedFiling(directory, filing); err == nil {
		t.Fatal("expected rename failure")
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 || entries[0].Name() != "filing.json" || !entries[0].IsDir() {
		t.Fatal("failed write changed destination or leaked temporary files")
	}
}

func TestParsedStoreRejectsTraversalAndVersions(t *testing.T) {
	filing, err := ParseSubmission(t.Context(), writeSubmission(t, submissionText(testInstance, testInstanceFilename)))
	if err != nil {
		t.Fatal(err)
	}

	filing.Metadata.CIK = "../escape"
	if _, err := SaveParsedFiling(t.TempDir(), filing); err == nil {
		t.Fatal("accepted CIK traversal")
	}

	filing.Metadata.CIK = "123"

	filing.Metadata.Accession = "../../escape"
	if _, err := SaveParsedFiling(t.TempDir(), filing); err == nil {
		t.Fatal("accepted accession traversal")
	}

	for _, content := range []string{`{"schema_version":999}`, `{"schema_version":1} {}`, `{"schema_version":1} trailing`} {
		path := filepath.Join(t.TempDir(), "filing.json")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}

		if _, err := LoadParsedFiling(path); err == nil {
			t.Fatalf("accepted invalid persisted data %s", strings.TrimSpace(content))
		}
	}
}
