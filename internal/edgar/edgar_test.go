package edgar

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQuarterlyArchives(t *testing.T) {
	archives, err := QuarterlyArchives(2025, time.Date(2026, time.May, 1, 0, 0, 0, 0, time.UTC), "https://example.test/Archives/")
	if err != nil {
		t.Fatalf("QuarterlyArchives returned error: %v", err)
	}

	if len(archives) != 6 {
		t.Fatalf("expected 6 archives, got %d", len(archives))
	}

	if archives[0].FileName != "2026-QTR2.tsv" || archives[len(archives)-1].FileName != "2025-QTR1.tsv" {
		t.Fatalf("unexpected archive range: %q to %q", archives[0].FileName, archives[len(archives)-1].FileName)
	}
}

func TestDownloadIndexExtractsAndAppendsHTMLURL(t *testing.T) {
	archive := testArchive(t)
	client := &http.Client{
		Transport: roundTripper(func(req *http.Request) (*http.Response, error) {
			if got := req.Header.Get("User-Agent"); got != "Example contact@example.test" {
				t.Errorf("unexpected user agent %q", got)
			}

			response := new(http.Response)
			response.StatusCode = http.StatusOK
			response.Status = "200 OK"
			response.Body = io.NopCloser(bytes.NewReader(archive))
			response.Header = make(http.Header)
			response.Request = req

			return response, nil
		}),
		CheckRedirect: nil,
		Jar:           nil,
		Timeout:       0,
	}

	directory := t.TempDir()
	// Use a fixed time-independent archive list by testing the extraction path directly.
	indexPath := filepath.Join(directory, "2026-QTR1.tsv")

	zipPath := filepath.Join(directory, "2026-QTR1.zip")
	if err := downloadZip(context.Background(), client, "https://example.test/master.zip", zipPath, "Example contact@example.test"); err != nil {
		t.Fatalf("downloadZip returned error: %v", err)
	}

	if err := extractIndex(zipPath, indexPath); err != nil {
		t.Fatalf("extractIndex returned error: %v", err)
	}

	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}

	want := "1000045|NICHOLAS FINANCIAL INC|10-K|2018-06-27|" +
		"edgar/data/1000045/0001193125-18-205637.txt|" +
		"edgar/data/1000045/0001193125-18-205637-index.html\n"
	if got := string(data); got != want {
		t.Fatalf("unexpected extracted index:\n%s", got)
	}
}

func TestStitch(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "2025-QTR2.tsv"), []byte("second\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(directory, "2025-QTR1.tsv"), []byte("first\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := Stitch(directory); err != nil {
		t.Fatalf("Stitch returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(directory, "master.tsv"))
	if err != nil {
		t.Fatal(err)
	}

	if got, want := string(data), "first\nsecond\n"; got != want {
		t.Fatalf("unexpected stitched index %q, want %q", got, want)
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func testArchive(t *testing.T) []byte {
	t.Helper()

	var buffer bytes.Buffer

	writer := zip.NewWriter(&buffer)

	file, err := writer.Create("master.idx")
	if err != nil {
		t.Fatal(err)
	}

	for i := range headerLines {
		_, _ = fmt.Fprintf(file, "header %d\n", i)
	}

	_, _ = strings.NewReader("1000045|NICHOLAS FINANCIAL INC|10-K|2018-06-27|edgar/data/1000045/0001193125-18-205637.txt\n").WriteTo(file)

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	return buffer.Bytes()
}
