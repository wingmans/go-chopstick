package filingworkflow

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "wingman.com/fetch-ecb/internal/edgar"
)

func workflowConfig(t *testing.T) (FilingDownloadConfig, ProcessingConfig) {
	t.Helper()
	root := t.TempDir()

	return FilingDownloadConfig{
			Directory: filepath.Join(root, "filings"),
			UserAgent: "Test test@example.com", BaseURL: "https://example.test", Noop: false,
		},
		ProcessingConfig{Directory: filepath.Join(root, "parsed"), FilingsDirectory: filepath.Join(root, "filings"), Reprocess: false, Noop: false}
}

func TestFilingWorkflowDownloadsFiltersAndReuses(t *testing.T) {
	const annualForm = "10-K"

	download, processing := workflowConfig(t)
	master := writeSubmission(t, "123|Example|10-K|2024-01-01|edgar/data/123/submission.txt|edgar/data/123/index.html\n"+
		"123|Example|8-K|2024-01-01|skip-form.txt\n123|Example|10-K|2023-01-01|skip-year.txt\n"+
		"456|Other|10-K|2024-01-01|skip-cik.txt\n")
	filter := IndexFilter{CIK: "123", FormTypes: []string{annualForm}, Year: 2024}
	requests := 0

	var client http.Client

	client.Transport = roundTripper(func(request *http.Request) (*http.Response, error) {
		requests++

		if strings.Contains(request.URL.Path, "skip") {
			t.Fatal("downloaded a filtered record")
		}

		payload := "<html/>"
		if strings.HasSuffix(request.URL.Path, ".txt") {
			payload = submissionText(testInstance, testInstanceFilename)
		}

		var response http.Response

		response.StatusCode = http.StatusOK
		response.Body = io.NopCloser(strings.NewReader(payload))
		response.Header = make(http.Header)

		return &response, nil
	})

	first, err := ProcessFilings(t.Context(), &client, master, download, processing, filter)
	if err != nil || first.Processed != 1 || first.Selected != 1 || requests != 2 {
		t.Fatalf("first=%+v requests=%d error=%v", first, requests, err)
	}

	second, err := ProcessFilings(t.Context(), &client, master, download, processing, filter)
	if err != nil || second.Reused != 1 || requests != 2 {
		t.Fatalf("second=%+v requests=%d error=%v", second, requests, err)
	}

	processing.Reprocess = true

	third, err := ProcessFilings(t.Context(), &client, master, download, processing, filter)
	if err != nil || third.Processed != 1 || requests != 2 {
		t.Fatalf("forced=%+v requests=%d error=%v", third, requests, err)
	}
}

func TestFilingWorkflowNoopLeavesMissingAndCompressedSourcesUntouched(t *testing.T) {
	download, processing := workflowConfig(t)
	download.Noop = true

	master := writeSubmission(t, "123|Example|10-K|2024-01-01|missing.txt\n123|Example|10-K|2024-01-01|compressed.txt\n")
	if err := os.MkdirAll(download.Directory, 0o750); err != nil {
		t.Fatal(err)
	}

	var buffer bytes.Buffer

	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write([]byte(submissionText(testInstance, testInstanceFilename))); err != nil {
		t.Fatal(err)
	}

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	compressed := filepath.Join(download.Directory, "compressed.txt")
	if err := os.WriteFile(compressed, buffer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	var client http.Client

	client.Transport = roundTripper(func(*http.Request) (*http.Response, error) {
		t.Fatal("network in noop")

		return nil, errors.New("unexpected network")
	})

	var filter IndexFilter

	summary, err := ProcessFilings(t.Context(), &client, master, download, processing, filter)
	if err != nil || summary.Planned != 2 {
		t.Fatalf("summary=%+v error=%v", summary, err)
	}

	data, err := os.ReadFile(compressed)
	if err != nil || !bytes.Equal(data, buffer.Bytes()) {
		t.Fatal("noop normalized cached source")
	}

	for _, path := range []string{filepath.Join(download.Directory, "missing.txt"), processing.Directory} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("noop created %s", path)
		}
	}
}

func TestFilingWorkflowContinuesAfterDownloadFailure(t *testing.T) {
	download, processing := workflowConfig(t)
	master := writeSubmission(t, "123|Example|10-K|2024-01-01|bad.txt\n123|Example|10-K|2024-01-01|good.txt\n")

	var client http.Client

	client.Transport = roundTripper(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/bad.txt" {
			return nil, errors.New("download failed")
		}

		var response http.Response

		response.StatusCode = http.StatusOK
		response.Header = make(http.Header)
		response.Body = io.NopCloser(strings.NewReader(submissionText(testInstance, testInstanceFilename)))

		return &response, nil
	})

	var filter IndexFilter

	summary, err := ProcessFilings(t.Context(), &client, master, download, processing, filter)
	if err == nil || summary.Selected != 2 || summary.Failed != 1 || summary.Processed != 1 {
		t.Fatalf("summary=%+v error=%v", summary, err)
	}
}

func TestProcessLocalFilingsProcessesAvailableUnprocessedRecords(t *testing.T) {
	root := t.TempDir()
	filings := filepath.Join(root, "filings")
	parsed := filepath.Join(root, "parsed")

	localPath, err := LocalFilingPath(filings, "edgar/data/123/submission.txt")
	if err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Dir(localPath), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(localPath, []byte(submissionText(testInstance, testInstanceFilename)), 0o600); err != nil {
		t.Fatal(err)
	}

	master := writeSubmission(t, "123|Example|10-K|2024-01-01|edgar/data/123/submission.txt\n"+
		"123|Example|10-Q|2024-01-01|missing.txt\n")
	processing := ProcessingConfig{Directory: parsed, FilingsDirectory: filings, Reprocess: false, Noop: false}
	filter := IndexFilter{CIK: "123", FormTypes: nil, Year: 2024}

	first, err := ProcessLocalFilings(t.Context(), master, filings, processing, filter)
	if err != nil || first.Selected != 1 || first.Processed != 1 {
		t.Fatalf("first=%+v error=%v", first, err)
	}

	second, err := ProcessLocalFilings(t.Context(), master, filings, processing, filter)
	if err != nil || second.Selected != 1 || second.Reused != 1 {
		t.Fatalf("second=%+v error=%v", second, err)
	}
}
