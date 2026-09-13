package filingworkflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"wingman.com/fetch-ecb/internal/ctxlog"
	"wingman.com/fetch-ecb/internal/edgar"
)

const (
	ActionProcessed = "processed"
	ActionReused    = "reused"
	ActionPlanned   = "planned"
)

type ProcessingConfig struct {
	Directory        string
	FilingsDirectory string
	Reprocess        bool
	Noop             bool
}

type ProcessingResult struct {
	Action string
	Path   string
	Filing *edgar.ParsedFiling
}

// ProcessSubmission is shared by the local command and the download workflow.
// Noop checks existing results but never parses or persists a stale result.
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
		cached, reusable, err := reusableFiling(ctx, result.Path, path, identity)
		if err != nil {
			return result, err
		}

		if reusable {
			result.Action, result.Filing = ActionReused, &cached
			logProcessing(ctx, path, result, cfg.Noop, started)

			return result, extractionFailure(&cached)
		}
	}

	if cfg.Noop {
		result.Action = ActionPlanned
		logProcessing(ctx, path, result, true, started)

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

	result.Path, err = edgar.SaveParsedFiling(cfg.Directory, result.Filing)
	if err != nil {
		return result, err
	}

	result.Action = ActionProcessed
	logProcessing(ctx, path, result, false, started)

	return result, extractionFailure(result.Filing)
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
// references are processing failures even when their diagnostics are cached.
func extractionFailure(filing *edgar.ParsedFiling) error {
	for _, diagnostic := range filing.Diagnostics {
		if diagnostic.Code == "xbrl_error" || diagnostic.Code == "xbrl_reference" {
			return fmt.Errorf("filing %s has extraction errors; inspect diagnostics", filing.AccessionNumber())
		}
	}

	return nil
}

func logProcessing(ctx context.Context, source string, result ProcessingResult, noop bool, started time.Time) {
	logger := ctxlog.FromContext(ctx)

	level := slog.LevelDebug
	if noop {
		level = slog.LevelInfo
	}

	location := "local"
	if result.Action == ActionReused {
		location = "cache"
	}

	logger.Log(ctx, level, "EDGAR processing decision", "source", location, "path", source,
		"filename", filepath.Base(source), "parsed_path", result.Path, "action", result.Action,
		"noop", noop, "duration_ms", time.Since(started).Milliseconds())

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
	failures  []error
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
		s.failures = append(s.failures, fmt.Errorf("%s: %w", path, err))

		ctxlog.FromContext(ctx).Error("filing processing failed", "path", path, "error", err)
	}
}

func (s *ProcessingSummary) log(ctx context.Context, noop bool) {
	ctxlog.FromContext(ctx).Info("EDGAR processing summary", "selected", s.Selected, "processed", s.Processed,
		"reused", s.Reused, "planned", s.Planned, "failed", s.Failed, "noop", noop)
}

func (s *ProcessingSummary) failure() error {
	if s.Failed > 0 {
		return fmt.Errorf("%d filing(s) failed processing: %w", s.Failed, errors.Join(s.failures...))
	}

	return nil
}

// ProcessLocalSubmissions never downloads; individual failures do not stop a batch.
func ProcessLocalSubmissions(ctx context.Context, paths []string, cfg ProcessingConfig) (ProcessingSummary, error) {
	var summary ProcessingSummary
	defer func() { summary.log(ctx, cfg.Noop) }()

	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return summary, err
		}

		result, err := ProcessSubmission(ctx, path, cfg)
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
