// Package pipeline coordinates local filing processing and writes both
// parsed filing artifacts and normalized filing read models.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"wingman.com/fetch-ecb/internal/analysisstore"
	"wingman.com/fetch-ecb/internal/ctxlog"
	dividendview "wingman.com/fetch-ecb/internal/dividend"
	"wingman.com/fetch-ecb/internal/edgar"
	filingview "wingman.com/fetch-ecb/internal/filing"
)

const (
	ActionProcessed = "processed"
	ActionReused    = "reused"
	ActionPlanned   = "planned"

	maxFailureClasses  = 10
	maxFailureExamples = 5
)

type ProcessingConfig struct {
	Directory        string
	FilingsDirectory string
	AnalysisDBPath   string
	AnalysisStore    *analysisstore.Store
	FormType         string
	Reprocess        bool
	Noop             bool
	SkipDividendView bool
}

type ProcessingResult struct {
	Action string
	Path   string
	Filing *edgar.ParsedFiling
}

// ProcessSubmission parses one SEC submission and writes filing.json plus
// filing-view.json. Noop checks existing results but never parses or persists a
// stale result.
//
//nolint:nestif // Reuse validation and no-op handling are one workflow boundary.
func ProcessSubmission(ctx context.Context, path string, cfg ProcessingConfig) (ProcessingResult, error) {
	var result ProcessingResult

	started := time.Now()

	metadata, err := edgar.ReadSubmissionMetadata(ctx, path)
	if err != nil {
		return result, err
	}

	var identity edgar.ParsedFiling

	identity.Metadata = metadata
	if err := setSourceReference(&identity, path, cfg.FilingsDirectory); err != nil {
		return result, err
	}

	result.Path, err = identity.ParsedPath(cfg.Directory)
	if err != nil {
		return result, err
	}

	if !cfg.Reprocess {
		stored, reusable, err := reusableFiling(ctx, result.Path, path, identity)
		if err != nil {
			return result, err
		}

		if reusable {
			result.Action, result.Filing = ActionReused, &stored

			if !cfg.Noop {
				if err := saveParsedFiling(ctx, cfg, &stored); err != nil {
					return result, err
				}

				if _, err := filingview.Save(cfg.Directory, &stored); err != nil {
					return result, err
				}

				if err := saveFilingView(ctx, cfg, &stored); err != nil {
					return result, err
				}

				if !cfg.SkipDividendView {
					if err := updateDividendView(ctx, cfg, &stored); err != nil {
						return result, err
					}
				}
			}

			logProcessing(ctx, path, result, cfg, started)

			return result, extractionFailure(&stored)
		}
	}

	if cfg.Noop {
		result.Action = ActionPlanned
		logProcessing(ctx, path, result, cfg, started)

		return result, nil
	}

	result.Filing, err = edgar.ParseSubmission(ctx, path)
	if err != nil {
		return result, err
	}

	if err := setSourceReference(result.Filing, path, cfg.FilingsDirectory); err != nil {
		return result, err
	}

	if err := ctx.Err(); err != nil {
		return result, err
	}

	if err := saveParsedFiling(ctx, cfg, result.Filing); err != nil {
		return result, err
	}

	result.Path, err = edgar.SaveParsedFiling(cfg.Directory, result.Filing)
	if err != nil {
		return result, err
	}

	if _, err := filingview.Save(cfg.Directory, result.Filing); err != nil {
		return result, err
	}

	if err := saveFilingView(ctx, cfg, result.Filing); err != nil {
		return result, err
	}

	if !cfg.SkipDividendView {
		if err := updateDividendView(ctx, cfg, result.Filing); err != nil {
			return result, err
		}
	}

	result.Action = ActionProcessed
	logProcessing(ctx, path, result, cfg, started)

	return result, extractionFailure(result.Filing)
}

func saveParsedFiling(ctx context.Context, cfg ProcessingConfig, filing *edgar.ParsedFiling) error {
	store, closeStore, err := storeForConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeStore()

	if err := store.SaveParsedFiling(ctx, filing); err != nil {
		return fmt.Errorf("save parsed filing: %w", err)
	}

	return nil
}

func updateDividendView(ctx context.Context, cfg ProcessingConfig, filing *edgar.ParsedFiling) error {
	store, closeStore, err := storeForConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeStore()

	view, err := store.LoadDividendView(ctx, filing.Metadata.CIK)
	if errors.Is(err, os.ErrNotExist) {
		view = dividendview.View{}
	} else if err != nil {
		return fmt.Errorf("load dividend view for %s: %w", filing.Metadata.CIK, err)
	}

	view, err = dividendview.Merge(ctx, view, cfg.Directory,
		cfg.FilingsDirectory, filing)
	if err != nil {
		return fmt.Errorf("build dividend view for %s: %w", filing.Metadata.CIK, err)
	}

	if err := store.SaveDividendView(ctx, view); err != nil {
		return fmt.Errorf("save dividend view for %s: %w", filing.Metadata.CIK, err)
	}

	if _, err := dividendview.Save(cfg.Directory, view); err != nil {
		return fmt.Errorf("write dividend compatibility view for %s: %w", filing.Metadata.CIK, err)
	}

	return nil
}

func saveFilingView(ctx context.Context, cfg ProcessingConfig, filing *edgar.ParsedFiling) error {
	view, err := filingview.Build(filing)
	if err != nil {
		return fmt.Errorf("build filing view: %w", err)
	}

	store, closeStore, err := storeForConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer closeStore()

	if err := store.SaveFilingView(ctx, view); err != nil {
		return fmt.Errorf("save filing view: %w", err)
	}

	return nil
}

func storeForConfig(ctx context.Context, cfg ProcessingConfig) (*analysisstore.Store, func(), error) {
	if cfg.AnalysisStore != nil {
		return cfg.AnalysisStore, func() {}, nil
	}

	path := cfg.AnalysisDBPath
	if path == "" {
		path = filepath.Join(filepath.Dir(cfg.Directory), "analysis.db")
	}

	store, err := analysisstore.Open(ctx, path)
	if err != nil {
		return nil, nil, fmt.Errorf("open analysis database: %w", err)
	}

	return store, func() { _ = store.Close() }, nil
}

func attachAnalysisStore(ctx context.Context, cfg ProcessingConfig) (ProcessingConfig, func(), error) {
	if cfg.AnalysisStore != nil {
		return cfg, func() {}, nil
	}

	store, closeStore, err := storeForConfig(ctx, cfg)
	if err != nil {
		return cfg, nil, err
	}

	cfg.AnalysisStore = store

	return cfg, closeStore, nil
}

func reusableFiling(ctx context.Context, destination, source string, identity edgar.ParsedFiling) (edgar.ParsedFiling, bool, error) {
	var missing edgar.ParsedFiling

	filing, err := edgar.LoadParsedFiling(destination)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return missing, false, err
		}

		return missing, false, nil // Missing, damaged, and old-schema results are rebuilt.
	}

	metadata := identity.Metadata
	if filing.Metadata.Accession != metadata.Accession || filing.Metadata.CIK != metadata.CIK || filing.Metadata.FormType != metadata.FormType {
		return missing, false, nil
	}

	if filing.SourceBase != identity.SourceBase || filing.SourcePath != identity.SourcePath {
		return missing, false, nil
	}

	switch filing.Status {
	case edgar.ParseComplete, edgar.ParseNoXBRL, edgar.ParsePartial, edgar.ParseUnsupported:
	default:
		return missing, false, nil
	}

	matches, err := filing.MatchesSource(ctx, source)
	if err != nil {
		return missing, false, err
	}

	if !matches {
		return missing, false, nil
	}

	return *filing, true, nil
}

func setSourceReference(filing *edgar.ParsedFiling, path, root string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	filing.SourceBase, filing.SourcePath = "absolute", absolute

	if root == "" {
		return nil
	}

	base, err := filepath.Abs(root)
	if err != nil {
		return err
	}

	relative, err := filepath.Rel(base, absolute)
	if err != nil {
		return err
	}

	if filepath.IsLocal(relative) {
		filing.SourceBase, filing.SourcePath = "filings", filepath.ToSlash(relative)
	}

	return nil
}

// Unsupported features are limitations, while malformed XML and broken
// references are processing failures even when their diagnostics are persisted.
func extractionFailure(filing *edgar.ParsedFiling) error {
	for _, diagnostic := range filing.Diagnostics {
		if diagnostic.Code == "xbrl_error" || diagnostic.Code == "xbrl_reference" {
			return fmt.Errorf("filing %s has extraction errors; inspect diagnostics", filing.AccessionNumber())
		}
	}

	return nil
}

func logProcessing(ctx context.Context, source string, result ProcessingResult, cfg ProcessingConfig, started time.Time) {
	logger := ctxlog.FromContext(ctx)

	level := slog.LevelDebug
	if cfg.Noop {
		level = slog.LevelInfo
	}

	location := "local"
	if result.Action == ActionReused {
		location = "cache"
	}

	logger.Log(ctx, level, "EDGAR processing decision", "source", location, "path", source,
		"filename", filepath.Base(source), "parsed_path", result.Path, "action", result.Action,
		"form_type", cfg.FormType,
		"noop", cfg.Noop, "duration_ms", time.Since(started).Milliseconds())

	if result.Filing == nil {
		return
	}

	for _, diagnostic := range result.Filing.Diagnostics {
		logger.Warn("submission diagnostic", "accession", result.Filing.AccessionNumber(),
			"code", diagnostic.Code, "document", diagnostic.Document, "detail", diagnostic.Message)
	}
}

type ProcessingSummary struct {
	Selected  int
	Processed int
	Reused    int
	Planned   int
	Failed    int
	failures  map[string]int
	examples  []string
	other     int
}

func (s *ProcessingSummary) record(ctx context.Context, path string, result ProcessingResult, err error) {
	s.Selected++

	switch result.Action {
	case ActionProcessed:
		s.Processed++
	case ActionReused:
		s.Reused++
	case ActionPlanned:
		s.Planned++
	}

	if err != nil {
		s.Failed++
		if s.failures == nil {
			s.failures = make(map[string]int)
		}

		failure := boundedFailure(err.Error())
		if _, ok := s.failures[failure]; ok {
			s.failures[failure]++
		} else if len(s.failures) < maxFailureClasses {
			s.failures[failure] = 1
		} else {
			s.other++
		}

		if len(s.examples) < maxFailureExamples {
			s.examples = append(s.examples, fmt.Sprintf("%s: %s", path, failure))
		}

		ctxlog.FromContext(ctx).Error("filing processing failed", "path", path, "error", err)
	}
}

func boundedFailure(message string) string {
	const maxFailureText = 512

	if len(message) <= maxFailureText {
		return message
	}

	return message[:maxFailureText] + "..."
}

func (s *ProcessingSummary) log(ctx context.Context, noop bool) {
	ctxlog.FromContext(ctx).Info("EDGAR processing summary", "selected", s.Selected, "processed", s.Processed,
		"reused", s.Reused, "planned", s.Planned, "failed", s.Failed, "noop", noop)
}

func (s *ProcessingSummary) failure() error {
	if s.Failed > 0 {
		classes := make([]string, 0, len(s.failures))
		for failure, count := range s.failures {
			classes = append(classes, fmt.Sprintf("%d x %s", count, failure))
		}
		sort.Strings(classes)
		if s.other > 0 {
			classes = append(classes, fmt.Sprintf("%d x other failures", s.other))
		}

		message := fmt.Sprintf("%d filing(s) failed processing; %d failure class(es): %s",
			s.Failed, len(classes), strings.Join(classes, "; "))
		if len(s.examples) > 0 {
			message += "; examples: " + strings.Join(s.examples, " | ")
		}

		return errors.New(message)
	}

	return nil
}

// ProcessLocalSubmissions never downloads; individual failures do not stop a batch.
func ProcessLocalSubmissions(ctx context.Context, paths []string, cfg ProcessingConfig) (ProcessingSummary, error) {
	var summary ProcessingSummary
	defer func() { summary.log(ctx, cfg.Noop) }()

	batchConfig := cfg

	var closeStore func()

	var err error
	if !cfg.Noop {
		batchConfig, closeStore, err = attachAnalysisStore(ctx, cfg)
		if err != nil {
			return summary, err
		}
		defer closeStore()
	}

	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return summary, err
		}

		result, err := ProcessSubmission(ctx, path, batchConfig)
		summary.record(ctx, path, result, err)

		if err := ctx.Err(); err != nil {
			return summary, err
		}
	}

	return summary, summary.failure()
}

// sourceNeedsPreparation also identifies legacy compressed cache files. They cannot
// be parsed before normalization, and normalization must not occur in a dry run.
func sourceNeedsPreparation(path string) (bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}

	if err != nil {
		return false, err
	}

	defer func() { _ = file.Close() }()

	var header [2]byte

	n, err := io.ReadFull(file, header[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false, err
	}

	return n == 2 && header[0] == 0x1f && header[1] == 0x8b, nil
}
