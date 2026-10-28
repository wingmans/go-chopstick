// Package ownershippipeline coordinates ownership downloads, parsing, and
// completion accounting.
package ownershippipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wingman.com/fetch-ecb/internal/ctxlog"
	"wingman.com/fetch-ecb/internal/edgar"
	"wingman.com/fetch-ecb/internal/ownership"
	"wingman.com/fetch-ecb/internal/ownershipstore"
)

// Config controls one streaming ownership import and completion pass.
type Config struct {
	MasterPath   string
	DatabasePath string
	Download     edgar.FilingDownloadConfig
	FromYear     int
	ToYear       int
	FormTypes    []string
	Reprocess    bool
	Noop         bool
	Limit        int
}

// Summary reports workflow decisions. Selected counts index rows, while all
// other fields count processing decisions after durable accession deduplication.
type Summary struct {
	Selected    int
	Processed   int
	Reused      int
	Unsupported int
	Failed      int
	Planned     int
}

// BatchError reports a completed batch with one or more filing failures.
type BatchError struct {
	Count int
}

func (failure *BatchError) Error() string {
	return fmt.Sprintf("%d ownership filings failed", failure.Count)
}

// Decision describes the durable result of processing one candidate.
type Decision int

const (
	DecisionProcessed Decision = iota
	DecisionReused
	DecisionUnsupported
	DecisionFailed
)

func (decision Decision) String() string {
	switch decision {
	case DecisionProcessed:
		return "processed"
	case DecisionReused:
		return "reused"
	case DecisionUnsupported:
		return "unsupported"
	case DecisionFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Run processes every recognized ownership form in the selected index range.
func Run(ctx context.Context, client *http.Client,
	config Config,
) (Summary, error) {
	var summary Summary
	if err := validateConfig(config); err != nil {
		return summary, err
	}

	file, err := os.Open(config.MasterPath)
	if err != nil {
		return summary, fmt.Errorf("open ownership master index: %w", err)
	}
	defer func() { _ = file.Close() }()

	if config.Noop {
		return plan(ctx, file, config)
	}

	store, err := ownershipstore.Open(ctx, config.DatabasePath)
	if err != nil {
		return summary, err
	}
	defer func() { _ = store.Close() }()

	downloader, err := edgar.NewFilingDownloader(client, config.Download)
	if err != nil {
		return summary, err
	}

	started := time.Now().UTC()
	reader := edgar.NewIndexReader(file, edgar.IndexFilter{
		FromYear: config.FromYear, ToYear: config.ToYear,
	})
	enabled := enabledForms(config.FormTypes)

	for {
		if err := ctx.Err(); err != nil {
			return summary, err
		}

		record, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return summary, err
		}

		form := ownership.NormalizeFormType(record.FormType)
		if !ownership.RecognizesForm(form) ||
			(len(enabled) > 0 && !enabled[form]) {
			continue
		}

		if config.Limit > 0 && summary.Selected >= config.Limit {
			break
		}

		summary.Selected++

		result, err := process(ctx, store, downloader, config, record)
		if err != nil {
			return summary, err
		}

		incrementSummary(&summary, result)
	}

	if len(config.FormTypes) == 0 && config.Limit == 0 {
		if err := recordCoverage(ctx, store, config, started); err != nil {
			return summary, err
		}
	}

	if summary.Failed > 0 {
		return summary, &BatchError{Count: summary.Failed}
	}

	return summary, nil
}

func process(ctx context.Context, store *ownershipstore.Store,
	downloader *edgar.FilingDownloader, config Config, record edgar.EdgarIndex,
) (Decision, error) {
	accession, err := accessionFromPath(record.FilingPath)
	if err != nil {
		return DecisionFailed, err
	}

	firstSeen, err := store.FirstSeen(ctx, accession)
	if err != nil {
		return DecisionFailed, err
	}

	if !firstSeen {
		return DecisionReused, nil
	}

	return ProcessOne(ctx, store, downloader, config, record)
}

// ProcessOne processes one recognized ownership event without batch-local
// deduplication. Durable accession state still makes the operation idempotent.
func ProcessOne(ctx context.Context, store *ownershipstore.Store,
	downloader *edgar.FilingDownloader, config Config,
	record edgar.EdgarIndex,
) (Decision, error) {
	form := ownership.NormalizeFormType(record.FormType)
	if !ownership.RecognizesForm(form) {
		return DecisionFailed, fmt.Errorf(
			"form %q is not a recognized ownership form", record.FormType)
	}

	accession, err := accessionFromPath(record.FilingPath)
	if err != nil {
		return DecisionFailed, err
	}

	family, _ := ownership.Family(form)

	outcome := ownershipstore.Outcome{
		Accession: accession, FormType: form, Family: family,
		FilingDate:    record.DateFiled.Format("2006-01-02"),
		SourcePath:    record.FilingPath,
		ParserVersion: ownership.ParserVersion,
	}
	if !config.Reprocess {
		needsImport, err := store.NeedsImport(ctx, accession,
			ownership.ParserVersion)
		if err != nil {
			return DecisionFailed, err
		}

		if !needsImport {
			return DecisionReused, nil
		}
	}

	if !ownership.SupportsForm(form) {
		outcome.Status = "unsupported"
		outcome.Diagnostic = "legacy ownership format is recognized but not parsed"

		return DecisionUnsupported, store.RecordOutcome(ctx, outcome)
	}

	if err := downloader.Download(ctx, record); err != nil {
		return recordFailure(ctx, store, record, outcome, err)
	}

	path, err := edgar.LocalFilingPath(config.Download.Directory,
		record.FilingPath)
	if err != nil {
		return DecisionFailed, err
	}

	outcome.SourcePath = path

	filing, err := ownership.Parse(ctx, path)
	outcome.SourceSHA256 = filing.SourceSHA256

	outcome.ReportDate = filing.ReportDate
	if err != nil {
		return recordFailure(ctx, store, record, outcome, err)
	}

	if filing.Accession != accession || filing.FormType != form {
		err := fmt.Errorf(
			"index identity %s %s does not match submission %s %s",
			accession, form, filing.Accession, filing.FormType)

		return recordFailure(ctx, store, record, outcome, err)
	}

	if err := store.Save(ctx, filing); err != nil {
		return DecisionFailed, err
	}

	return DecisionProcessed, nil
}

func recordFailure(ctx context.Context, store *ownershipstore.Store,
	record edgar.EdgarIndex, outcome ownershipstore.Outcome,
	filingErr error,
) (Decision, error) {
	if ctx.Err() != nil {
		return DecisionFailed, ctx.Err()
	}

	outcome.Status, outcome.Diagnostic = "failed", filingErr.Error()
	if err := store.RecordOutcome(ctx, outcome); err != nil {
		return DecisionFailed, errors.Join(filingErr, err)
	}

	logFailure(ctx, record, filingErr)

	return DecisionFailed, nil
}

func incrementSummary(summary *Summary, result Decision) {
	switch result {
	case DecisionProcessed:
		summary.Processed++
	case DecisionReused:
		summary.Reused++
	case DecisionUnsupported:
		summary.Unsupported++
	case DecisionFailed:
		summary.Failed++
	}
}

func plan(ctx context.Context, source io.Reader,
	config Config,
) (Summary, error) {
	var summary Summary

	seen := make(map[string]struct{})
	reader := edgar.NewIndexReader(source, edgar.IndexFilter{
		FromYear: config.FromYear, ToYear: config.ToYear,
	})
	enabled := enabledForms(config.FormTypes)

	for {
		if err := ctx.Err(); err != nil {
			return summary, err
		}

		record, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return summary, nil
		}

		if err != nil {
			return summary, err
		}

		form := ownership.NormalizeFormType(record.FormType)
		if !ownership.RecognizesForm(form) ||
			(len(enabled) > 0 && !enabled[form]) {
			continue
		}

		if config.Limit > 0 && summary.Selected >= config.Limit {
			return summary, nil
		}

		summary.Selected++

		accession, err := accessionFromPath(record.FilingPath)
		if err != nil {
			return summary, err
		}

		if _, duplicate := seen[accession]; duplicate {
			continue
		}

		seen[accession] = struct{}{}
		summary.Planned++
	}
}

func validateConfig(config Config) error {
	if strings.TrimSpace(config.MasterPath) == "" {
		return errors.New("ownership master index path is required")
	}

	if strings.TrimSpace(config.DatabasePath) == "" && !config.Noop {
		return errors.New("ownership database path is required")
	}

	if config.FromYear != 0 && config.FromYear < edgar.EarliestPipelineYear {
		return fmt.Errorf("ownership start year must be %d or later",
			edgar.EarliestPipelineYear)
	}

	if config.ToYear != 0 && config.ToYear < edgar.EarliestPipelineYear {
		return fmt.Errorf("ownership end year must be %d or later",
			edgar.EarliestPipelineYear)
	}

	if config.FromYear != 0 && config.ToYear != 0 &&
		config.FromYear > config.ToYear {
		return errors.New("ownership start year is after end year")
	}

	for _, form := range config.FormTypes {
		if !ownership.RecognizesForm(form) {
			return fmt.Errorf("unrecognized ownership form %q", form)
		}
	}

	return nil
}

func accessionFromPath(path string) (string, error) {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	parts := strings.Split(base, "-")
	if len(parts) != 3 || len(parts[0]) != 10 || len(parts[1]) != 2 ||
		len(parts[2]) != 6 {
		return "", fmt.Errorf("invalid accession path %q", path)
	}

	for _, part := range parts {
		if strings.Trim(part, "0123456789") != "" {
			return "", fmt.Errorf("invalid accession path %q", path)
		}
	}

	return base, nil
}

func enabledForms(forms []string) map[string]bool {
	if len(forms) == 0 {
		return nil
	}

	result := make(map[string]bool, len(forms))
	for _, form := range forms {
		result[ownership.NormalizeFormType(form)] = true
	}

	return result
}

func recordCoverage(ctx context.Context, store *ownershipstore.Store,
	config Config, started time.Time,
) error {
	fromDate := ""
	if config.FromYear != 0 {
		fromDate = fmt.Sprintf("%04d-01-01", config.FromYear)
	}

	throughDate := time.Now().UTC().Format("2006-01-02")
	if config.ToYear != 0 {
		throughDate = fmt.Sprintf("%04d-12-31", config.ToYear)
	}

	counts, err := store.Counts(ctx, fromDate, throughDate)
	if err != nil {
		return err
	}

	byFamily := make(map[string]map[string]int64)
	for _, count := range counts {
		if byFamily[count.Family] == nil {
			byFamily[count.Family] = make(map[string]int64)
		}

		byFamily[count.Family][count.Status] = count.Count
	}

	now := time.Now().UTC()

	for _, family := range []string{
		ownership.FamilyInstitutional, ownership.FamilyInsider,
		ownership.FamilyBeneficial,
	} {
		values := byFamily[family]
		processed := values["processed"]
		failed := values["failed"]
		unsupported := values["unsupported"]

		status := "complete"
		if failed > 0 {
			status = "partial"
		}

		if err := store.SaveCoverage(ctx, ownershipstore.Coverage{
			Family: family, FromDate: fromDate, ThroughDate: throughDate,
			Version:   ownership.ParserVersion,
			Expected:  processed + failed + unsupported,
			Processed: processed, Failed: failed, Unsupported: unsupported,
			Status: status, StartedAt: started, CompletedAt: now,
		}); err != nil {
			return err
		}
	}

	return nil
}

func logFailure(ctx context.Context, record edgar.EdgarIndex, err error) {
	ctxlog.FromContext(ctx).Warn("ownership filing failed",
		"path", record.FilingPath, "form_type", record.FormType,
		"error", err)
}
