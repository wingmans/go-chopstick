// Package choppie contains the application workflows invoked by the Chop CLI.
package choppie

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"wingman.com/chopstick/internal/appdata"
	"wingman.com/chopstick/internal/coverage"
	"wingman.com/chopstick/internal/ctxlog"
	"wingman.com/chopstick/internal/edgar"
	"wingman.com/chopstick/internal/edgarapp"
	filingview "wingman.com/chopstick/internal/filing"
	"wingman.com/chopstick/internal/ownership"
	ownershippipeline "wingman.com/chopstick/internal/ownership/pipeline"
	ownershipstore "wingman.com/chopstick/internal/ownership/store"
	filingworkflow "wingman.com/chopstick/internal/pipeline"
	"wingman.com/chopstick/internal/transport"
)

// ErrEmptyStatus indicates that chopd returned no status payload.
var ErrEmptyStatus = errors.New("chopd returned an empty status")

// BackfillConfig selects a finite historical pipeline run.
type BackfillConfig struct {
	SetName  string
	FromYear int
	NATSURL  string
}

// RebuildConfig selects the retained inputs used to rebuild EDGAR state.
type RebuildConfig struct {
	SetName  string
	FromYear int
	NATSURL  string
}

// Backfill runs the finite historical pipeline.
func Backfill(ctx context.Context, cfg BackfillConfig, logger *slog.Logger) error {
	config, err := applicationConfig(cfg.SetName, cfg.FromYear, cfg.NATSURL)
	if err != nil {
		return err
	}

	return edgarapp.Backfill(ctx, config, logger)
}

// Rebuild restores disposable EDGAR state from retained inputs.
func Rebuild(ctx context.Context, cfg RebuildConfig, logger *slog.Logger) error {
	config, err := applicationConfig(cfg.SetName, cfg.FromYear, cfg.NATSURL)
	if err != nil {
		return err
	}

	return edgarapp.Rebuild(ctx, config, logger)
}

func applicationConfig(setName string, fromYear int,
	natsURL string,
) (edgarapp.Config, error) {
	config, err := edgarapp.ConfigFromEnvironment()
	if err != nil {
		return edgarapp.Config{}, err
	}

	config.NATSURL = natsURL
	config.FromYear = fromYear

	if err := config.SetConstituentSet(setName); err != nil {
		return edgarapp.Config{}, err
	}

	return config, nil
}

// ParseConfig selects local filing submissions for parsing.
type ParseConfig struct {
	Files      []string
	MasterPath string
	AnalysisDB string
	Filter     edgar.IndexFilter
	Noop       bool
	Reprocess  bool
}

// Parse processes local filing submissions.
func Parse(ctx context.Context, cfg ParseConfig) error {
	processing := filingworkflow.ProcessingConfig{
		Directory:        appdata.Path("parsed"),
		FilingsDirectory: appdata.Path("cache", "filings"),
		AnalysisDBPath:   cfg.AnalysisDB,
		Reprocess:        cfg.Reprocess,
		Noop:             cfg.Noop,
	}
	if len(cfg.Files) > 0 {
		_, err := filingworkflow.ProcessLocalSubmissions(
			ctx,
			cfg.Files,
			processing,
		)

		return err
	}

	_, err := filingworkflow.ProcessLocalFilings(
		ctx,
		cfg.MasterPath,
		appdata.Path("cache", "filings"),
		processing,
		cfg.Filter,
	)

	return err
}

// CoverageConfig selects inputs for a coverage report.
type CoverageConfig struct {
	MasterPath string
	IndexesDir string
	FilingsDir string
	ParsedDir  string
	AnalysisDB string
	OutputPath string
	Filter     edgar.IndexFilter
	SetName    string
	FromYear   int
	ToYear     int
}

// Coverage builds and writes a filing coverage report.
func Coverage(ctx context.Context, cfg CoverageConfig) error {
	taxonomy, err := filingview.LoadTaxonomy("")
	if err != nil {
		return err
	}

	report, err := coverage.Build(ctx, coverage.Config{
		MasterPath:  cfg.MasterPath,
		IndexesDir:  cfg.IndexesDir,
		FilingsDir:  cfg.FilingsDir,
		ParsedDir:   cfg.ParsedDir,
		AnalysisDB:  cfg.AnalysisDB,
		Filter:      cfg.Filter,
		SetName:     cfg.SetName,
		FromYear:    cfg.FromYear,
		ToYear:      cfg.ToYear,
		Taxonomy:    taxonomy,
		GeneratedAt: time.Now().UTC(),
	})
	if err != nil {
		return err
	}

	if err := coverage.WriteJSON(cfg.OutputPath, report); err != nil {
		return err
	}

	ctxlog.FromContext(ctx).Info("EDGAR coverage report written",
		"path", cfg.OutputPath,
		"expected", report.Summary.Expected,
		"downloaded", report.Summary.Downloaded,
		"parsed", report.Summary.Parsed,
		"missing", report.Summary.Missing,
		"unparsed", report.Summary.Unparsed,
		"missing_metrics", report.Summary.MissingMetrics,
		"acquisition_requests", len(report.Acquisition),
	)

	return nil
}

// ProcessFilings downloads and processes selected filings.
func ProcessFilings(
	ctx context.Context,
	client *http.Client,
	masterPath string,
	analysisDB string,
	config edgar.FilingDownloadConfig,
	filter edgar.IndexFilter,
	reprocess bool,
) error {
	_, err := filingworkflow.ProcessFilings(
		ctx,
		client,
		masterPath,
		config,
		filingworkflow.ProcessingConfig{
			Directory:        appdata.Path("parsed"),
			FilingsDirectory: config.Directory,
			AnalysisDBPath:   analysisDB,
			Reprocess:        reprocess,
			Noop:             config.Noop,
		},
		filter,
	)

	return err
}

// Status requests live application state from chopd.
func Status(ctx context.Context, address string) (transport.Status, error) {
	response, err := transport.Send(ctx, address, transport.Request{
		Command: transport.CommandStatus,
	})
	if err != nil {
		return transport.Status{}, err
	}

	if response.Status == nil {
		return transport.Status{}, ErrEmptyStatus
	}

	return *response.Status, nil
}

// ListFailures requests unresolved pipeline failures from chopd.
func ListFailures(
	ctx context.Context,
	address string,
	stage string,
	limit int,
) ([]transport.Failure, error) {
	response, err := transport.Send(ctx, address, transport.Request{
		Command: transport.CommandListFailures,
		Stage:   stage,
		Limit:   limit,
	})

	return response.Failures, err
}

// RetryFailures asks chopd to requeue unresolved failures.
func RetryFailures(ctx context.Context, address, stage string) (int, error) {
	response, err := transport.Send(ctx, address, transport.Request{
		Command: transport.CommandRetryFailures,
		Stage:   stage,
	})

	return response.Retried, err
}

// ImportOwnership runs the finite ownership import workflow.
func ImportOwnership(
	ctx context.Context,
	client *http.Client,
	config ownershippipeline.Config,
) (ownershippipeline.Summary, error) {
	return ownershippipeline.Run(ctx, client, config)
}

// OwnershipStatus loads durable ownership coverage and outcome counts.
func OwnershipStatus(
	ctx context.Context,
	databasePath string,
) ([]ownershipstore.Coverage, []ownershipstore.Count, error) {
	if _, err := os.Stat(databasePath); err != nil {
		return nil, nil, err
	}

	store, err := ownershipstore.Open(ctx, databasePath)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = store.Close() }()

	coverage, err := store.LatestCoverage(ctx, ownership.ParserVersion)
	if err != nil {
		return nil, nil, err
	}

	counts, err := store.Counts(ctx, "", "")
	if err != nil {
		return nil, nil, err
	}

	return coverage, counts, nil
}
