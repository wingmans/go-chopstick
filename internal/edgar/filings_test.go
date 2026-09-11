package edgar

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUniqueFormTypes(t *testing.T) {
	directory := t.TempDir()
	masterPath := filepath.Join(directory, "master.tsv")

	contents := strings.Join([]string{
		"1|One|10-Q|2024-01-01|edgar/data/1/q.txt",
		"2|Two|10-K|2024-01-02|edgar/data/2/k.txt",
		"3|Three|10-Q|2024-01-03|edgar/data/3/q.txt",
	}, "\n")
	if err := os.WriteFile(masterPath, []byte(contents), 0o640); err != nil {
		t.Fatal(err)
	}

	formTypes, err := UniqueFormTypes(masterPath)
	if err != nil {
		t.Fatalf("UniqueFormTypes returned error: %v", err)
	}

	if got, want := strings.Join(formTypes, ","), "10-K,10-Q"; got != want {
		t.Fatalf("unexpected form types %q, want %q", got, want)
	}
}

func TestDownloadFilingSkipsExistingFiles(t *testing.T) {
	directory := t.TempDir()
	record := EdgarIndex{
		CIK:        "1",
		FormType:   "10-K",
		FilingPath: "edgar/data/1/filing.txt",
		IndexPath:  "edgar/data/1/filing-index.html",
	}

	filingPath, err := localFilingPath(directory, record.FilingPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Dir(filingPath), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filingPath, []byte("already downloaded"), 0o640); err != nil {
		t.Fatal(err)
	}

	indexPath, err := localFilingPath(directory, record.IndexPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(indexPath, []byte("already downloaded"), 0o640); err != nil {
		t.Fatal(err)
	}

	client := &http.Client{Transport: roundTripper(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected network request for existing file")

		return nil, nil
	})}

	if err := DownloadFiling(context.Background(), client, FilingDownloadConfig{
		Directory: directory,
		UserAgent: "Example contact@example.test",
	}, record); err != nil {
		t.Fatalf("DownloadFiling returned error: %v", err)
	}

	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("expected existing index file, stat error: %v", err)
	}
}

func TestDownloadFilingNormalizesExistingGzipFile(t *testing.T) {
	directory := t.TempDir()
	record := EdgarIndex{
		CIK:        "1",
		FormType:   "10-K",
		FilingPath: "edgar/data/1/filing.txt",
	}

	filingPath, err := localFilingPath(directory, record.FilingPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Dir(filingPath), 0o750); err != nil {
		t.Fatal(err)
	}

	file, err := os.Create(filingPath)
	if err != nil {
		t.Fatal(err)
	}

	writer := gzip.NewWriter(file)
	if _, err := writer.Write([]byte("readable filing")); err != nil {
		t.Fatal(err)
	}

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	client := &http.Client{Transport: roundTripper(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected network request for existing compressed file")

		return nil, nil
	})}

	if err := DownloadFiling(context.Background(), client, FilingDownloadConfig{
		Directory: directory,
		UserAgent: "Example contact@example.test",
	}, record); err != nil {
		t.Fatalf("DownloadFiling returned error: %v", err)
	}

	data, err := os.ReadFile(filingPath)
	if err != nil {
		t.Fatal(err)
	}

	if string(data) != "readable filing" {
		t.Fatalf("unexpected normalized content %q", data)
	}
}

func TestDownloadFilingWritesBothReferencedFiles(t *testing.T) {
	directory := t.TempDir()
	record := EdgarIndex{
		CIK:        "1",
		FormType:   "10-K",
		FilingPath: "edgar/data/1/filing.txt",
		IndexPath:  "edgar/data/1/filing-index.html",
	}

	requests := 0
	client := &http.Client{Transport: roundTripper(func(req *http.Request) (*http.Response, error) {
		requests++

		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(req.URL.Path)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	if err := DownloadFiling(context.Background(), client, FilingDownloadConfig{
		Directory: directory,
		UserAgent: "Example contact@example.test",
		BaseURL:   "https://example.test/Archives",
	}, record); err != nil {
		t.Fatalf("DownloadFiling returned error: %v", err)
	}

	if requests != 2 {
		t.Fatalf("expected 2 requests, got %d", requests)
	}

	for _, relativePath := range []string{record.FilingPath, record.IndexPath} {
		path, err := localFilingPath(directory, relativePath)
		if err != nil {
			t.Fatal(err)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		if string(data) != "/Archives/"+relativePath {
			t.Fatalf("unexpected content for %s: %q", relativePath, data)
		}
	}
}
