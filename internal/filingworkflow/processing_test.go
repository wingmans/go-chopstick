package filingworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func processingConfig(t *testing.T) ProcessingConfig {
	t.Helper()

	return ProcessingConfig{Directory: filepath.Join(t.TempDir(), "parsed"), FilingsDirectory: "", Reprocess: false, Noop: false}
}

func TestProcessingReuseAndForce(t *testing.T) {
	path := writeSubmission(t, submissionText(testInstance, testInstanceFilename))
	cfg := processingConfig(t)

	first, err := ProcessSubmission(t.Context(), path, cfg)
	if err != nil || first.Action != ActionProcessed {
		t.Fatalf("first result=%+v error=%v", first, err)
	}

	stamp := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(first.Path, stamp, stamp); err != nil {
		t.Fatal(err)
	}

	second, err := ProcessSubmission(t.Context(), path, cfg)
	if err != nil || second.Action != ActionReused {
		t.Fatalf("second action=%s error=%v", second.Action, err)
	}

	info, err := os.Stat(first.Path)
	if err != nil || !info.ModTime().Equal(stamp) {
		t.Fatal("reuse rewrote parsed result")
	}

	cfg.Reprocess = true

	third, err := ProcessSubmission(t.Context(), path, cfg)
	if err != nil || third.Action != ActionProcessed {
		t.Fatalf("forced action=%s error=%v", third.Action, err)
	}

	if third.Filing.SourceBase != "absolute" || third.Filing.SourcePath != path {
		t.Fatal("external source reference is not absolute")
	}
}

func TestProcessingInvalidatesStaleResults(t *testing.T) {
	for _, reason := range []string{"source", "parser", "schema", "corrupt", "identity", "status", "location"} {
		t.Run(reason, func(t *testing.T) {
			text := submissionText(testInstance, testInstanceFilename)
			path := writeSubmission(t, text)
			cfg := processingConfig(t)

			first, err := ProcessSubmission(t.Context(), path, cfg)
			if err != nil {
				t.Fatal(err)
			}

			switch reason {
			case "source":
				if err := os.WriteFile(path, []byte(text+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "parser":
				first.Filing.ParserVersion = "old"
			case "schema":
				first.Filing.SchemaVersion--
			case "identity":
				first.Filing.Metadata.Accession = "0000000123-24-000099"
			case "status":
				first.Filing.Status = "unknown"
			case "location":
				first.Filing.SourcePath = "stale-path"
			}

			data, err := json.Marshal(first.Filing)
			if err != nil {
				t.Fatal(err)
			}

			if reason == "corrupt" {
				data = []byte("{")
			}

			if err := os.WriteFile(first.Path, data, 0o600); err != nil {
				t.Fatal(err)
			}

			result, err := ProcessSubmission(t.Context(), path, cfg)
			if err != nil || result.Action != ActionProcessed {
				t.Fatalf("action=%s error=%v", result.Action, err)
			}
		})
	}
}

func TestProcessingUsesCanonicalFilerAndRelativeSource(t *testing.T) {
	source := submissionText(testInstance, testInstanceFilename)
	header := "FILER:\n\tCENTRAL INDEX KEY: 456\nFILER:\n\tCENTRAL INDEX KEY: 123\n"
	source = strings.ReplaceAll(source, "</SEC-HEADER>", header+"</SEC-HEADER>")
	path := writeSubmission(t, source)
	cfg := processingConfig(t)
	cfg.FilingsDirectory = filepath.Dir(path)

	first, err := ProcessSubmission(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}

	expected := filepath.Join(cfg.Directory, "0000000456", "0000000123-24-000001", "filing.json")
	if first.Path != expected {
		t.Fatalf("path=%s want=%s", first.Path, expected)
	}

	if first.Filing.SourceBase != "filings" || first.Filing.SourcePath != "submission.txt" {
		t.Fatal("incorrect relative source reference")
	}

	second, err := ProcessSubmission(t.Context(), path, cfg)
	if err != nil || second.Action != ActionReused {
		t.Fatalf("multi-filer lookup diverged: %s %v", second.Action, err)
	}
}

func TestProcessingNoop(t *testing.T) {
	path := writeSubmission(t, submissionText(testInstance, testInstanceFilename))
	cfg := processingConfig(t)
	cfg.Noop = true

	result, err := ProcessSubmission(t.Context(), path, cfg)
	if err != nil || result.Action != ActionPlanned || result.Filing != nil {
		t.Fatalf("dry run=%+v error=%v", result, err)
	}

	if _, err := os.Stat(cfg.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("noop created parsed directory")
	}

	cfg.Noop = false
	if _, err := ProcessSubmission(t.Context(), path, cfg); err != nil {
		t.Fatal(err)
	}

	cfg.Noop = true

	result, err = ProcessSubmission(t.Context(), path, cfg)
	if err != nil || result.Action != ActionReused {
		t.Fatalf("dry reuse=%s error=%v", result.Action, err)
	}

	cfg.Reprocess = true

	result, err = ProcessSubmission(t.Context(), path, cfg)
	if err != nil || result.Action != ActionPlanned {
		t.Fatalf("dry force=%s error=%v", result.Action, err)
	}
}

func TestProcessingRetainsDiagnosticResults(t *testing.T) {
	const htmlFilename = "report.htm"
	for _, test := range []struct {
		name, payload, filename string
		failure                 bool
	}{
		{name: "no xbrl", payload: "plain text", filename: htmlFilename, failure: false},
		{name: "inline", payload: `<html xmlns:ix="http://www.xbrl.org/2013/inlineXBRL"/>`, filename: htmlFilename, failure: false},
		{name: "malformed", payload: strings.TrimSuffix(testInstance, "</xbrl>"), filename: testInstanceFilename, failure: true},
		{
			name: "unresolved", payload: strings.ReplaceAll(testInstance, `contextRef="c"`, `contextRef="missing"`),
			filename: testInstanceFilename, failure: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := writeSubmission(t, submissionText(test.payload, test.filename))
			cfg := processingConfig(t)

			first, err := ProcessSubmission(t.Context(), path, cfg)
			if (err != nil) != test.failure || first.Action != ActionProcessed {
				t.Fatalf("processed=%s error=%v", first.Action, err)
			}

			second, err := ProcessSubmission(t.Context(), path, cfg)
			if (err != nil) != test.failure || second.Action != ActionReused {
				t.Fatalf("reused=%s error=%v", second.Action, err)
			}
		})
	}
}

func TestLocalProcessingContinuesAfterFailure(t *testing.T) {
	bad := writeSubmission(t, "not a submission")
	good := writeSubmission(t, submissionText(testInstance, testInstanceFilename))

	summary, err := ProcessLocalSubmissions(t.Context(), []string{bad, good}, processingConfig(t))
	if err == nil || summary.Selected != 2 || summary.Failed != 1 || summary.Processed != 1 {
		t.Fatalf("summary=%+v error=%v", summary, err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := ProcessLocalSubmissions(ctx, []string{good}, processingConfig(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
