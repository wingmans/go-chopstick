package choppie

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"wingman.com/chopstick/internal/appdata"
	"wingman.com/chopstick/internal/constituents"
	"wingman.com/chopstick/internal/ctxlog"
	"wingman.com/chopstick/internal/edgar"
	"wingman.com/chopstick/internal/ownership"
	"wingman.com/chopstick/internal/transport"
	"wingman.com/chopstick/internal/xerr"
)

// ArtifactsConfig identifies one filing and an artifact operation.
type ArtifactsConfig struct {
	Operation    string
	CIK          string
	Accession    string
	ParsedDir    string
	FilingsDir   string
	ArtifactsDir string
	Address      string
	Types        []string
	Filenames    []string
	All          bool
}

// Artifacts inspects or serves disposable source-document artifacts.
func Artifacts(ctx context.Context, cfg ArtifactsConfig) error {
	normalizedCIK, err := constituents.NormalizeCIK(cfg.CIK)
	if err != nil {
		return fmt.Errorf("--cik: %w", err)
	}

	cfg.CIK = normalizedCIK
	cfg.Accession = strings.TrimSpace(cfg.Accession)

	if cfg.Accession == "" || filepath.Base(cfg.Accession) != cfg.Accession {
		return errors.New("--accession must be a safe accession number")
	}

	if cfg.Operation == "extract" && !cfg.All && len(cfg.Types) == 0 &&
		len(cfg.Filenames) == 0 {
		return errors.New("extract requires --type, --document, or --all")
	}

	if cfg.Operation == "serve" {
		if err := validateViewerAddress(cfg.Address); err != nil {
			return err
		}
	}

	return runArtifacts(ctx, artifactCommandConfig{
		operation: cfg.Operation, cik: cfg.CIK, accession: cfg.Accession,
		parsedDir: cfg.ParsedDir, filingsDir: cfg.FilingsDir,
		artifactsDir: cfg.ArtifactsDir, address: cfg.Address,
		types: cfg.Types, filenames: cfg.Filenames, all: cfg.All,
	})
}

// DoctorConfig contains paths and dependencies checked by Doctor.
type DoctorConfig struct {
	NATSURL     string
	PollingDB   string
	NewsDB      string
	MasterPath  string
	HTTPAddress string
	Verbose     bool
}

// Doctor validates local configuration and dependencies.
func Doctor(ctx context.Context, cfg DoctorConfig) error {
	return runDoctor(ctx, operationsConfig{
		natsURL: cfg.NATSURL, pollingDB: cfg.PollingDB,
		newsDB: cfg.NewsDB, masterPath: cfg.MasterPath,
		httpAddress: cfg.HTTPAddress, verbose: cfg.Verbose,
	})
}

// StatusConfig identifies the running application command endpoint.
type StatusConfig struct {
	Address string
}

// PrintStatus requests and renders live application status.
func PrintStatus(ctx context.Context, cfg StatusConfig) error {
	return runStatus(ctx, operationsConfig{commandAddress: cfg.Address})
}

// FailureListConfig selects unresolved pipeline failures to render.
type FailureListConfig struct {
	Address string
	Stage   string
	Limit   int
}

// PrintFailures requests and renders unresolved pipeline failures.
func PrintFailures(ctx context.Context, cfg FailureListConfig) error {
	if cfg.Limit <= 0 {
		return errors.New("--limit must be positive")
	}

	if err := validateFailureStage(cfg.Stage, false); err != nil {
		return err
	}

	return runListFailures(ctx, operationsConfig{
		commandAddress: cfg.Address, stage: cfg.Stage, limit: cfg.Limit,
	})
}

// FailureRetryConfig selects one pipeline stage to requeue.
type FailureRetryConfig struct {
	Address string
	Stage   string
}

// RetryFailureStage requeues unresolved failures for one stage.
func RetryFailureStage(ctx context.Context, cfg FailureRetryConfig) error {
	if err := validateFailureStage(cfg.Stage, true); err != nil {
		return err
	}

	return runRetryFailures(ctx, operationsConfig{
		commandAddress: cfg.Address, stage: cfg.Stage,
	})
}

func validateFailureStage(stage string, required bool) error {
	if required && stage == "" {
		return errors.New("--stage is required")
	}

	if stage != "" && stage != "download" && stage != "process" {
		return errors.New("--stage must be download or process")
	}

	return nil
}

// OwnershipConfig selects an ownership import or status operation.
type OwnershipConfig struct {
	Operation    string
	MasterPath   string
	DatabasePath string
	CacheDir     string
	UserAgent    string
	BaseURL      string
	FormTypes    []string
	FromYear     int
	ToYear       int
	Reprocess    bool
	Noop         bool
}

// Ownership runs an ownership import or prints ownership status.
func Ownership(ctx context.Context, client *http.Client,
	cfg OwnershipConfig,
) error {
	if cfg.Operation == "status" {
		if strings.TrimSpace(cfg.DatabasePath) == "" {
			return errors.New("--database cannot be empty")
		}

		return printOwnershipStatus(ctx, cfg.DatabasePath)
	}

	if cfg.FromYear < edgar.EarliestPipelineYear {
		return fmt.Errorf("--from-year must be %d or later",
			edgar.EarliestPipelineYear)
	}

	if cfg.ToYear != 0 && cfg.ToYear < cfg.FromYear {
		return errors.New("--to-year cannot be before --from-year")
	}

	if strings.TrimSpace(cfg.MasterPath) == "" ||
		strings.TrimSpace(cfg.DatabasePath) == "" ||
		strings.TrimSpace(cfg.CacheDir) == "" {
		return errors.New("ownership paths cannot be empty")
	}

	if !cfg.Noop && strings.TrimSpace(cfg.UserAgent) == "" {
		return errors.New("EDGAR_USER_AGENT cannot be empty")
	}

	forms, err := normalizeOwnershipForms(cfg.FormTypes)
	if err != nil {
		return err
	}

	return runOwnership(ctx, client, ownershipCommandConfig{
		operation: cfg.Operation, masterPath: cfg.MasterPath,
		databasePath: cfg.DatabasePath,
		download: edgar.FilingDownloadConfig{
			Directory: cfg.CacheDir, UserAgent: cfg.UserAgent,
			BaseURL: cfg.BaseURL, Noop: cfg.Noop,
		},
		formTypes: forms, fromYear: cfg.FromYear, toYear: cfg.ToYear,
		reprocess: cfg.Reprocess, noop: cfg.Noop,
	})
}

func normalizeOwnershipForms(values []string) ([]string, error) {
	seen := make(map[string]bool)
	result := make([]string, 0, len(values))

	for _, raw := range values {
		form := ownership.NormalizeFormType(raw)
		if !ownership.RecognizesForm(form) {
			return nil, fmt.Errorf("unrecognized ownership form %q", raw)
		}

		if !seen[form] {
			seen[form] = true
			result = append(result, form)
		}
	}

	return result, nil
}

// TaxonomyConfig selects local filing views for taxonomy inspection.
type TaxonomyConfig struct {
	Operation    string
	File         string
	TaxonomyPath string
	ParsedDir    string
	Filter       edgar.IndexFilter
	SetName      string
	FromYear     int
	ToYear       int
	Format       string
	OutputPath   string
}

// Taxonomy runs a filing taxonomy lint or coverage report.
func Taxonomy(ctx context.Context, cfg TaxonomyConfig) error {
	return runTaxonomyCommand(ctx, cfg.Operation, cfg.File,
		cfg.TaxonomyPath, cfg.ParsedDir, cfg.Filter, cfg.SetName,
		cfg.FromYear, cfg.ToYear, cfg.Format, cfg.OutputPath)
}

// DownloadIndex downloads and stitches SEC quarterly indexes.
func DownloadIndex(ctx context.Context, client *http.Client,
	cfg edgar.Config,
) error {
	logger := ctxlog.FromContext(ctx)
	logger.Info("starting EDGAR index download",
		"from_year", cfg.SinceYear, "directory", cfg.Directory,
		"stitch", cfg.Stitch, "noop", cfg.Noop)

	return edgar.DownloadIndex(ctx, client, cfg)
}

// DownloadFilingsConfig selects a finite filing download and parse workflow.
type DownloadFilingsConfig struct {
	MasterPath string
	AnalysisDB string
	Download   edgar.FilingDownloadConfig
	Filter     edgar.IndexFilter
	SetName    string
	Reprocess  bool
}

// DownloadFilings runs the finite filing workflow.
func DownloadFilings(ctx context.Context, client *http.Client,
	cfg DownloadFilingsConfig,
) error {
	logger := ctxlog.FromContext(ctx)
	logger.Info("starting EDGAR filing workflow",
		"master", cfg.MasterPath, "directory", cfg.Download.Directory,
		"cik", cfg.Filter.CIK, "set", cfg.SetName,
		"set_members", len(cfg.Filter.CIKs),
		"form_types", cfg.Filter.FormTypes, "year", cfg.Filter.Year,
		"from_year", cfg.Filter.FromYear, "to_year", cfg.Filter.ToYear,
		"noop", cfg.Download.Noop, "reprocess", cfg.Reprocess)

	return ProcessFilings(ctx, client, cfg.MasterPath, cfg.AnalysisDB,
		cfg.Download, cfg.Filter, cfg.Reprocess)
}

// ProcessSubmissions runs the finite local parsing workflow.
func ProcessSubmissions(ctx context.Context, cfg ParseConfig) error {
	logger := ctxlog.FromContext(ctx)
	logger.Info("starting EDGAR parse workflow",
		"master", cfg.MasterPath, "cik", cfg.Filter.CIK,
		"set_members", len(cfg.Filter.CIKs),
		"form_types", cfg.Filter.FormTypes, "year", cfg.Filter.Year,
		"from_year", cfg.Filter.FromYear, "to_year", cfg.Filter.ToYear,
		"noop", cfg.Noop, "reprocess", cfg.Reprocess)

	return Parse(ctx, cfg)
}

// BuildFilter resolves constituent and year selections into an index filter.
func BuildFilter(cik, setName string, formTypes []string, year string,
	fromYear, toYear int,
) (edgar.IndexFilter, error) {
	forms, err := edgar.ValidateSupportedFormTypes(formTypes)
	if err != nil {
		return edgar.IndexFilter{}, err
	}

	filter := edgar.IndexFilter{CIK: cik, FormTypes: forms}
	if err := applySet(&filter, setName); err != nil {
		return edgar.IndexFilter{}, err
	}

	if err := applyYears(&filter, year, fromYear, toYear); err != nil {
		return edgar.IndexFilter{}, err
	}

	return filter, nil
}

func applySet(filter *edgar.IndexFilter, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}

	if filepath.Base(name) != name || filepath.Ext(name) != "" {
		return errors.New("--set must be a set name, not a path")
	}

	set, err := constituents.LoadJSON(appdata.Path("sets", name+".json"))
	if err != nil {
		return err
	}

	filter.CIKs = set.CIKs()

	return nil
}

func applyYears(filter *edgar.IndexFilter, year string,
	fromYear, toYear int,
) error {
	if strings.TrimSpace(year) != "" {
		if fromYear != 0 || toYear != 0 {
			return errors.New(
				"--year cannot be combined with --from-year or --to-year")
		}

		parsed, err := strconv.Atoi(strings.TrimSpace(year))
		if err != nil || parsed < 1000 || parsed > 9999 {
			return errors.New("--year must be a four-digit year")
		}

		filter.Year = parsed

		return nil
	}

	if fromYear != 0 && (fromYear < edgar.EarliestYear || fromYear > 9999) {
		return errors.New("--from-year must be a valid EDGAR year")
	}

	if toYear != 0 && (toYear < edgar.EarliestYear || toYear > 9999) {
		return errors.New("--to-year must be a valid EDGAR year")
	}

	if fromYear != 0 && toYear != 0 && fromYear > toYear {
		return errors.New("--from-year must not be after --to-year")
	}

	filter.FromYear = fromYear
	filter.ToYear = toYear

	return nil
}

// IndexImportConfig selects an index import handled by chopd.
type IndexImportConfig struct {
	Address  string
	File     string
	Latest   bool
	Year     int
	FromYear int
	ToYear   int
	DryRun   bool
}

// SubmitIndexImport asks chopd to ingest local quarterly indexes.
func SubmitIndexImport(ctx context.Context, cfg IndexImportConfig) error {
	response, err := transport.Send(ctx, cfg.Address, transport.Request{
		Command: transport.CommandImportIndex,
		IndexImport: &transport.IndexImportRequest{
			File: cfg.File, Latest: cfg.Latest, Year: cfg.Year,
			FromYear: cfg.FromYear, ToYear: cfg.ToYear, DryRun: cfg.DryRun,
		},
	})
	if err != nil {
		return err
	}

	if response.IndexImport == nil || !response.IndexImport.Accepted {
		return errors.New("chopd did not accept the index import")
	}

	fmt.Println("Index import accepted by chopd.")
	fmt.Println("Use 'chop status' to inspect pipeline progress.")

	return nil
}

// IndexStatusConfig identifies the running application's command endpoint.
type IndexStatusConfig struct {
	Address string
}

// PrintIndexStatus prints local index inventory reported by chopd.
func PrintIndexStatus(ctx context.Context, cfg IndexStatusConfig) error {
	response, err := transport.Send(ctx, cfg.Address, transport.Request{
		Command: transport.CommandIndexStatus,
	})
	if err != nil {
		return err
	}

	if response.IndexStatus == nil {
		return errors.New("chopd returned an empty index status")
	}

	renderIndexStatus(*response.IndexStatus)

	return nil
}

// ClassifyError maps command failures onto the stable CLI error vocabulary.
func ClassifyError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}

	logger := ctxlog.FromContext(ctx)
	logger.Debug("Chop command error detail", "error", err.Error())

	if _, ok := errors.AsType[*xerr.Error](err); ok {
		return err
	}

	switch {
	case errors.Is(err, context.Canceled) ||
		errors.Is(ctx.Err(), context.Canceled):
		return xerr.Wrap(xerr.Canceled, "COMMAND_CANCELED",
			"command was canceled", err)
	case errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(ctx.Err(), context.DeadlineExceeded):
		return xerr.Wrap(xerr.DeadlineExceeded, "COMMAND_TIMEOUT",
			"command timed out", err)
	case errors.Is(err, os.ErrNotExist):
		return xerr.Wrap(xerr.NotFound, "LOCAL_DATA_NOT_FOUND",
			"required local data was not found", err)
	default:
		if httpError, ok := errors.AsType[*edgar.HTTPError](err); ok {
			return xerr.Wrap(xerr.Unavailable, "EDGAR_REJECTED_REQUEST",
				"EDGAR rejected the request", err).WithDetails(map[string]any{
				"url": httpError.URL, "status_code": httpError.StatusCode,
				"status": httpError.Status,
			})
		}

		if _, ok := errors.AsType[net.Error](err); ok {
			return xerr.Wrap(xerr.Unavailable, "EDGAR_UNAVAILABLE",
				"EDGAR service is unavailable", err)
		}

		return xerr.Wrap(xerr.Internal, "CHOP_COMMAND_FAILED",
			"Chop command failed", err)
	}
}
