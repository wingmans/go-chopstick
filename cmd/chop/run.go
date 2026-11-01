package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"wingman.com/chopstick/internal/appdata"
	"wingman.com/chopstick/internal/choppie"
	"wingman.com/chopstick/internal/edgar"
	"wingman.com/chopstick/internal/eventbus"
	"wingman.com/chopstick/internal/indeximport"
	"wingman.com/chopstick/internal/transport"
)

const defaultUserAgent = "wingman paul@wingmen.io"

type commandRuntime struct {
	Client  *http.Client
	Logger  *slog.Logger
	Verbose bool
}

type commandDefaults struct {
	FromYear       int
	Set            string
	NATSURL        string
	UserAgent      string
	BaseURL        string
	CommandAddress string
	HTTPAddress    string
}

func commandDefaultsFromEnvironment() (commandDefaults, error) {
	fromYear := edgar.EarliestPipelineYear

	if value := strings.TrimSpace(os.Getenv("EDGAR_FROM_YEAR")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return commandDefaults{}, fmt.Errorf(
				"EDGAR_FROM_YEAR must be an integer: %w", err)
		}

		fromYear = parsed
	}

	return commandDefaults{
		FromYear:  fromYear,
		Set:       environmentValue("EDGAR_SET", ""),
		NATSURL:   environmentValue("EDGAR_NATS_URL", eventbus.DefaultURL),
		UserAgent: environmentValue("EDGAR_USER_AGENT", defaultUserAgent),
		BaseURL:   environmentValue("EDGAR_BASE_URL", ""),
		CommandAddress: environmentValue(
			"CHOP_COMMAND_ADDRESS", transport.DefaultAddress),
		HTTPAddress: webAddressFromEnvironment(),
	}, nil
}

func (d commandDefaults) vars(version string) map[string]string {
	return map[string]string{
		"version":         version,
		"from_year":       strconv.Itoa(d.FromYear),
		"set":             d.Set,
		"nats_url":        d.NATSURL,
		"user_agent":      d.UserAgent,
		"base_url":        d.BaseURL,
		"command_address": d.CommandAddress,
		"http_address":    d.HTTPAddress,
		"polling_db":      appdata.Path("polling.db"),
		"news_db":         appdata.Path("feed.db"),
		"analysis_db":     appdata.Path("analysis.db"),
		"ownership_db":    appdata.Path("ownership.db"),
		"master":          appdata.Path("cache", "indexes", "master.tsv"),
		"indexes_dir":     appdata.Path("cache", "indexes", "quarterly"),
		"index_zips":      appdata.Path("cache", "index-zips"),
		"filings_dir":     appdata.Path("cache", "filings"),
		"parsed_dir":      appdata.Path("parsed"),
		"artifacts_dir":   appdata.Path("tmp", "artifacts"),
		"coverage_output": appdata.Path(
			"validation", "coverage-report.json"),
	}
}

func environmentValue(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}

	return fallback
}

func webAddressFromEnvironment() string {
	for _, name := range []string{
		"EDGAR_HTTP_ADDRESS", "EDGAR_FILINGS_ADDRESS", "EDGAR_NEWS_ADDRESS",
	} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}

	return "127.0.0.1:8083"
}

func (c *backfillCLI) Run(ctx context.Context, runtime *commandRuntime) error {
	if strings.TrimSpace(c.Set) == "" {
		return errors.New("--set is required")
	}

	if err := validatePipelineYear(c.FromYear); err != nil {
		return err
	}

	return choppie.Backfill(ctx, choppie.BackfillConfig{
		SetName: c.Set, FromYear: c.FromYear, NATSURL: c.NATSURL,
	}, runtime.Logger)
}

func (c *rebuildEdgarCLI) Run(ctx context.Context, runtime *commandRuntime) error {
	if !c.Confirm {
		return errors.New("--confirm is required")
	}

	if strings.TrimSpace(c.Set) == "" {
		return errors.New("--set is required")
	}

	if err := validatePipelineYear(c.FromYear); err != nil {
		return err
	}

	return choppie.Rebuild(ctx, choppie.RebuildConfig{
		SetName: c.Set, FromYear: c.FromYear, NATSURL: c.NATSURL,
	}, runtime.Logger)
}

func validatePipelineYear(year int) error {
	if year < edgar.EarliestPipelineYear {
		return fmt.Errorf("--from-year must be %d or later",
			edgar.EarliestPipelineYear)
	}

	return nil
}

func (c *indexDownloadCLI) Run(ctx context.Context, runtime *commandRuntime) error {
	if err := validatePipelineYear(c.FromYear); err != nil {
		return err
	}

	return choppie.DownloadIndex(ctx, runtime.Client, edgar.Config{
		Directory:     appdata.Path("cache", "indexes", "quarterly"),
		ZipDirectory:  appdata.Path("cache", "index-zips"),
		MasterPath:    appdata.Path("cache", "indexes", "master.tsv"),
		SinceYear:     c.FromYear,
		UserAgent:     environmentValue("EDGAR_USER_AGENT", defaultUserAgent),
		RefreshLatest: c.RefreshLatest, Stitch: c.Stitch,
		BaseURL: environmentValue("EDGAR_BASE_URL", ""), Noop: c.Noop,
	})
}

func (c *indexImportCLI) Run(ctx context.Context, runtime *commandRuntime) error {
	return choppie.ImportIndex(ctx, indeximport.Config{
		IndexesDirectory: c.IndexesDir, PollingDB: c.PollingDB,
		NATSURL: c.NATSURL, File: c.File, Latest: c.Latest,
		Year: c.Year, FromYear: c.FromYear, ToYear: c.ToYear,
		DryRun: c.DryRun,
	}, runtime.Logger)
}

func (s selectionCLI) filter() (edgar.IndexFilter, error) {
	return choppie.BuildFilter(s.CIK, s.Set, s.FormTypes, s.Year,
		s.FromYear, s.ToYear)
}

func (c *filingsCLI) Run(ctx context.Context, runtime *commandRuntime) error {
	filter, err := c.filter()
	if err != nil {
		return err
	}

	userAgent := environmentValue("EDGAR_USER_AGENT", defaultUserAgent)
	if strings.TrimSpace(userAgent) == "" {
		return errors.New("EDGAR_USER_AGENT cannot be empty")
	}

	return choppie.DownloadFilings(ctx, runtime.Client,
		choppie.DownloadFilingsConfig{
			MasterPath: appdata.Path("cache", "indexes", "master.tsv"),
			AnalysisDB: c.AnalysisDB,
			Download: edgar.FilingDownloadConfig{
				Directory: appdata.Path("cache", "filings"),
				UserAgent: userAgent,
				BaseURL:   environmentValue("EDGAR_BASE_URL", ""),
				Noop:      c.Noop,
			},
			Filter: filter, SetName: c.Set, Reprocess: c.Reprocess,
		})
}

func (c *parseCLI) Run(ctx context.Context) error {
	if len(c.Files) > 0 && c.hasSelection() {
		return errors.New("--file cannot be combined with filing filters")
	}

	for _, path := range c.Files {
		if filepath.Ext(path) != ".txt" {
			return errors.New("--file must name a .txt submission")
		}
	}

	filter, err := c.filter()
	if err != nil {
		return err
	}

	return choppie.ProcessSubmissions(ctx, choppie.ParseConfig{
		Files:      c.Files,
		MasterPath: appdata.Path("cache", "indexes", "master.tsv"),
		AnalysisDB: c.AnalysisDB, Filter: filter,
		Noop: c.Noop, Reprocess: c.Reprocess,
	})
}

func (c *parseCLI) hasSelection() bool {
	return c.CIK != "" || c.Set != "" || len(c.FormTypes) > 0 ||
		c.Year != "" || c.FromYear != 0 || c.ToYear != 0
}

func (c *coverageCLI) Run(ctx context.Context) error {
	filter, err := choppie.BuildFilter(c.CIK, c.Set, c.FormTypes, "",
		c.FromYear, c.ToYear)
	if err != nil {
		return err
	}

	return choppie.Coverage(ctx, choppie.CoverageConfig{
		MasterPath: c.Master, IndexesDir: c.IndexesDir,
		FilingsDir: c.FilingsDir, ParsedDir: c.ParsedDir,
		AnalysisDB: c.AnalysisDB, OutputPath: c.Output,
		Filter: filter, SetName: c.Set,
		FromYear: c.FromYear, ToYear: c.ToYear,
	})
}

func (c *ownershipImportCLI) Run(ctx context.Context, runtime *commandRuntime) error {
	return choppie.Ownership(ctx, runtime.Client,
		choppie.OwnershipConfig{
			Operation: "import", MasterPath: c.Master,
			DatabasePath: c.Database, CacheDir: c.CacheDir,
			UserAgent: environmentValue("EDGAR_USER_AGENT", defaultUserAgent),
			BaseURL:   environmentValue("EDGAR_BASE_URL", ""),
			FormTypes: c.FormTypes, FromYear: c.FromYear,
			ToYear: c.ToYear, Reprocess: c.Reprocess, Noop: c.Noop,
		})
}

func (c *ownershipStatusCLI) Run(ctx context.Context, runtime *commandRuntime) error {
	return choppie.Ownership(ctx, runtime.Client,
		choppie.OwnershipConfig{
			Operation: "status", DatabasePath: c.Database,
		})
}

func artifactConfig(operation string, options artifactOptions) choppie.ArtifactsConfig {
	return choppie.ArtifactsConfig{
		Operation: operation, CIK: options.CIK,
		Accession: options.Accession, ParsedDir: options.ParsedDir,
		FilingsDir: options.FilingsDir, ArtifactsDir: options.ArtifactsDir,
	}
}

func (c *artifactListCLI) Run(ctx context.Context) error {
	return choppie.Artifacts(ctx,
		artifactConfig("list", c.artifactOptions))
}

func (c *artifactCleanCLI) Run(ctx context.Context) error {
	return choppie.Artifacts(ctx,
		artifactConfig("clean", c.artifactOptions))
}

func (c *artifactExtractCLI) Run(ctx context.Context) error {
	cfg := artifactConfig("extract", c.artifactOptions)
	cfg.Types = c.Types
	cfg.Filenames = c.Documents
	cfg.All = c.All

	return choppie.Artifacts(ctx, cfg)
}

func (c *artifactServeCLI) Run(ctx context.Context) error {
	cfg := artifactConfig("serve", c.artifactOptions)
	cfg.Address = c.Address

	return choppie.Artifacts(ctx, cfg)
}

func (c taxonomyOptions) run(ctx context.Context, operation string,
) error {
	if strings.TrimSpace(c.File) != "" && c.hasSelection() {
		return errors.New("--file cannot be combined with filters")
	}

	if strings.TrimSpace(c.ParsedDir) == "" {
		return errors.New("--parsed-dir cannot be empty")
	}

	filter, err := choppie.BuildFilter(c.CIK, c.Set, c.FormTypes,
		c.Year, c.FromYear, c.ToYear)
	if err != nil {
		return err
	}

	return choppie.Taxonomy(ctx, choppie.TaxonomyConfig{
		Operation: operation, File: c.File, TaxonomyPath: c.Taxonomy,
		ParsedDir: c.ParsedDir, Filter: filter, SetName: c.Set,
		FromYear: c.FromYear, ToYear: c.ToYear,
		Format: c.Format, OutputPath: c.Output,
	})
}

func (c taxonomyOptions) hasSelection() bool {
	return c.CIK != "" || c.Set != "" || len(c.FormTypes) > 0 ||
		c.Year != "" || c.FromYear != 0 || c.ToYear != 0
}

func (c *taxonomyLintCLI) Run(ctx context.Context) error {
	return c.run(ctx, "lint")
}

func (c *taxonomyCoverageCLI) Run(ctx context.Context) error {
	return c.run(ctx, "coverage")
}

func (c *doctorCLI) Run(ctx context.Context, runtime *commandRuntime) error {
	return choppie.Doctor(ctx, choppie.DoctorConfig{
		NATSURL: c.NATSURL, PollingDB: c.PollingDB,
		NewsDB: c.NewsDB, MasterPath: c.Master,
		HTTPAddress: webAddressFromEnvironment(), Verbose: runtime.Verbose,
	})
}

func (c *statusCLI) Run(ctx context.Context) error {
	return choppie.PrintStatus(ctx, choppie.StatusConfig{
		Address: c.CommandAddress,
	})
}

func (c *failuresListCLI) Run(ctx context.Context) error {
	return choppie.PrintFailures(ctx, choppie.FailureListConfig{
		Address: c.CommandAddress, Stage: c.Stage, Limit: c.Limit,
	})
}

func (c *failuresRetryCLI) Run(ctx context.Context) error {
	return choppie.RetryFailureStage(ctx, choppie.FailureRetryConfig{
		Address: c.CommandAddress, Stage: c.Stage,
	})
}
