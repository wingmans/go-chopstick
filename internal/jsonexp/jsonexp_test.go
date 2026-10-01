// Package jsonexp contains experiments for the parsed-filing JSON boundary.
// It is intentionally separate from production persistence code.
//
// The tests are an A/B harness, not application tests. They answer whether a
// change to JSON reading or writing is worth carrying into the parser. Keep
// the fixture and the large local artifact as separate cases: the fixture is
// stable in CI, while the local artifact represents realistic filing size.
package jsonexp

import (
	"bufio"
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"wingman.com/fetch-ecb/internal/parser"
)

const (
	fixturePath = "testdata/filing.json"
	// largePath is deliberately a package-level path rather than an environment
	// variable. It points at a real parsed filing when the local data set is
	// available; the related benchmark skips when it is not.
	largePath = "../../data/parsed/0000753308/0000753308-19-000118/filing.json"
)

// TestJSONImplementationsAgree verifies that the two JSON implementations
// produce the same ParsedFiling for the committed fixture. A production
// migration must keep this test, plus the golden and pipeline tests, passing.
func TestJSONImplementationsAgree(t *testing.T) {
	current, err := loadCurrent(fixturePath)
	if err != nil {
		t.Fatal(err)
	}

	v2, err := loadV2(fixturePath)
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(current, v2) {
		t.Fatalf("encoding/json and encoding/json/v2 decoded values differ")
	}

	if current.Metadata.Accession != "0000000000-24-000001" {
		t.Fatalf("fixture accession=%q", current.Metadata.Accession)
	}
}

// BenchmarkParsedFilingDecode measures the performance of decoding ParsedFiling
// instances from JSON using different implementations. It includes both the
// committed fixture and a large local artifact.
func BenchmarkParsedFilingDecode(b *testing.B) {
	// Each iteration opens and decodes the file. This intentionally measures
	// the operation that matters when a parser artifact must be loaded, rather
	// than measuring only an in-memory decode.
	benchDecode(b, fixturePath)
	benchDecode(b, largePath)
}

func benchDecode(b *testing.B, path string) {
	// The subtest name includes the artifact directory so results remain
	// readable when more small, medium, and large filings are added later.
	b.Run(filepath.Base(filepath.Dir(path))+"/current", func(b *testing.B) {
		benchmarkDecoder(b, path, loadCurrent)
	})
	b.Run(filepath.Base(filepath.Dir(path))+"/current-buffered", func(b *testing.B) {
		benchmarkDecoder(b, path, loadCurrentBuffered)
	})
	b.Run(filepath.Base(filepath.Dir(path))+"/jsonv2", func(b *testing.B) {
		benchmarkDecoder(b, path, loadV2)
	})
}

// benchmarkDecoder is a helper for running subbenchmarks that decode ParsedFiling
// instances from JSON using a specified loading function. It is used by
// BenchmarkParsedFilingDecode.
func benchmarkDecoder(
	b *testing.B,
	path string,
	load func(string) (*parser.ParsedFiling, error),
) {
	b.Helper()
	// Large data files are local benchmark inputs, not committed test
	// dependencies. A missing artifact should not make go test fail.
	if _, err := os.Stat(path); err != nil {
		b.Skipf("benchmark artifact is unavailable: %v", err)
	}

	b.ReportAllocs()
	for b.Loop() {
		filing, err := load(path)
		if err != nil {
			b.Fatal(err)
		}
		if filing.Metadata.Accession == "" {
			b.Fatal("decoded filing has no accession")
		}
	}
}

// BenchmarkParsedFilingEncode measures the performance of encoding ParsedFiling
// instances to JSON using different implementations. It includes both the
// committed fixture and a large local artifact.
func BenchmarkParsedFilingEncode(b *testing.B) {
	// Decode once before the subbenchmarks. Encoding benchmarks should compare
	// serialization cost, not repeatedly include the cost of loading JSON.
	filing, err := loadCurrent(largePath)
	if err != nil {
		b.Skipf("benchmark artifact is unavailable: %v", err)
	}

	b.Run("current-indented", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if err := encodeCurrent(b, filing, true); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("current-compact", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if err := encodeCurrent(b, filing, false); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("jsonv2-compact", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if err := jsonv2.MarshalWrite(io.Discard, filing); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// encodeCurrent is a helper for encoding a ParsedFiling using the current
// implementation, optionally with indentation.
func encodeCurrent(b *testing.B, filing *parser.ParsedFiling, indented bool) error {
	// Indentation is kept as a separate case because the current artifacts are
	// human-readable, while compact output may be a cheap storage optimization.
	b.Helper()
	encoder := json.NewEncoder(io.Discard)
	if indented {
		encoder.SetIndent("", "  ")
	}

	return encoder.Encode(filing)
}

func BenchmarkSourceFingerprint(b *testing.B) {
	// The fingerprint is the warm-path guard that lets the pipeline avoid
	// loading filing.json altogether. Use a temporary source and sidecar so
	// this benchmark does not depend on checkout timestamps.
	directory := b.TempDir()
	source := filepath.Join(directory, "filing.txt")
	if err := os.WriteFile(source, []byte("raw filing fixture\n"), 0o600); err != nil {
		b.Fatal(err)
	}
	if err := parser.WriteSourceFingerprint(source); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	for b.Loop() {
		matches, err := parser.SourceFingerprintMatches(context.Background(), source)
		if err != nil {
			b.Fatal(err)
		}
		if !matches {
			b.Fatal("source fingerprint did not match")
		}
	}
}

// loadCurrent loads a ParsedFiling from the given path using the current
// JSON decoding implementation. It does not perform schema validation or
// check for trailing input.
// This is the current implementation's basic decode operation. The
// production loader also validates schema and trailing input afterward;
// those checks are intentionally outside this focused comparison.
func loadCurrent(path string) (*parser.ParsedFiling, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var filing parser.ParsedFiling
	if err := json.NewDecoder(file).Decode(&filing); err != nil {
		return nil, err
	}

	return &filing, nil
}

// loadCurrentBuffered loads a ParsedFiling from the given path using the current
func loadCurrentBuffered(path string) (*parser.ParsedFiling, error) {
	// This case tests whether adding an explicit large buffer improves the
	// current decoder. The standard decoder already buffers the file itself.
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var filing parser.ParsedFiling
	reader := bufio.NewReaderSize(file, 256*1024)
	if err := json.NewDecoder(reader).Decode(&filing); err != nil {
		return nil, err
	}

	return &filing, nil
}

func loadV2(path string) (*parser.ParsedFiling, error) {
	// json/v2 is tested without changing production code. If this starts
	// failing on a real filing, that identifies a compatibility issue to
	// investigate before considering a parser migration.
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	var filing parser.ParsedFiling
	if err := jsonv2.UnmarshalRead(file, &filing); err != nil {
		return nil, err
	}

	return &filing, nil
}
