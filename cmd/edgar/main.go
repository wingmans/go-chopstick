// Package main downloads SEC EDGAR data and parses local filing submissions.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"wingman.com/fetch-ecb/internal/constituents"
	"wingman.com/fetch-ecb/internal/ctxlog"
	"wingman.com/fetch-ecb/internal/edgar"
	"wingman.com/fetch-ecb/internal/filingview"
	"wingman.com/fetch-ecb/internal/filingworkflow"
	"wingman.com/fetch-ecb/internal/xerr"
)

const defaultUserAgent = "wingman paul@wingmen.io"

const (
	edgarUserAgentEnv = "EDGAR_USER_AGENT"
	edgarBaseURLEnv   = "EDGAR_BASE_URL"
	parseCommand      = "parse"
	noopHelpFlags     = "-n, --noop"
	defaultSetDir     = "./data/sets"
)

type appConfig struct {
	command string
	index   edgar.Config
	parse   struct {
		files      stringList
		formTypes  stringList
		masterPath string
		filter     edgar.IndexFilter
		setName    string
		year       string
		fromYear   int
		toYear     int
		noop       bool
		reprocess  bool
	}
	filings struct {
		masterPath string
		config     edgar.FilingDownloadConfig
		filter     edgar.IndexFilter
		setName    string
		year       string
		fromYear   int
		toYear     int
		reprocess  bool
	}
	serve struct {
		address   string
		parsedDir string
		setName   string
	}
	taxonomy struct {
		operation string
		file      string
		taxonomy  string
		parsedDir string
		format    string
		out       string
		filter    edgar.IndexFilter
		formTypes stringList
		setName   string
		year      string
		fromYear  int
		toYear    int
		limit     int
	}
	coverage struct {
		masterPath string
		indexesDir string
		filingsDir string
		parsedDir  string
		out        string
		filter     edgar.IndexFilter
		formTypes  stringList
		setName    string
		fromYear   int
		toYear     int
	}
}

type stringList []string

type helpOption struct {
	flags       string
	description string
}

func (s *stringList) String() string {
	return strings.Join(*s, ", ")
}

func (s *stringList) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("value cannot be empty")
	}

	*s = append(*s, value)

	return nil
}

func main() {
	if err := runMain(); err != nil {
		os.Exit(1)
	}
}

func runMain() error {
	logger := ctxlog.New()
	ctx := ctxlog.WithLogger(context.Background(), logger)

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})

	go func() {
		select {
		case <-ctx.Done():
			stop()
		case <-done:
		}
	}()

	defer close(done)

	defer stop()

	if err := run(ctx, os.Args[1:]); err != nil {
		if errors.Is(err, context.Canceled) {
			logger.Info("shutdown requested")

			return nil
		}

		logger.Error("command failed", "error", err)

		return err
	}

	return nil
}

func run(ctx context.Context, args []string) error {
	cfg, err := parseConfig(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return xerr.Wrap(xerr.InvalidInput, "INVALID_CONFIGURATION", "invalid command-line configuration", err)
	}

	client := &http.Client{
		Transport:     nil,
		CheckRedirect: nil,
		Jar:           nil,
		Timeout:       30 * time.Second,
	}
	logger := ctxlog.FromContext(ctx)

	started := time.Now()
	defer func() {
		logger.Info("finished EDGAR command", "command", cfg.command, "duration_ms", time.Since(started).Milliseconds())
	}()

	var commandErr error

	switch cfg.command {
	case parseCommand:
		commandErr = runParse(ctx, cfg)
	case "index":
		logger.Info("starting EDGAR index download",
			"from_year", cfg.index.SinceYear,
			"directory", cfg.index.Directory,
			"stitch", cfg.index.Stitch,
			"noop", cfg.index.Noop,
		)

		commandErr = edgar.DownloadIndex(ctx, client, cfg.index)
	case "filings":
		logger.Info("starting EDGAR filing workflow",
			"master", cfg.filings.masterPath,
			"directory", cfg.filings.config.Directory,
			"cik", cfg.filings.filter.CIK,
			"set", cfg.filings.setName,
			"set_members", len(cfg.filings.filter.CIKs),
			"form_types", cfg.filings.filter.FormTypes,
			"year", cfg.filings.filter.Year,
			"from_year", cfg.filings.filter.FromYear,
			"to_year", cfg.filings.filter.ToYear,
			"noop", cfg.filings.config.Noop,
			"reprocess", cfg.filings.reprocess,
		)

		_, err := filingworkflow.ProcessFilings(ctx, client, cfg.filings.masterPath, cfg.filings.config, filingworkflow.ProcessingConfig{
			Directory:        edgar.DefaultParsedDirectory,
			FilingsDirectory: cfg.filings.config.Directory,
			FormType:         "",
			Reprocess:        cfg.filings.reprocess,
			Noop:             cfg.filings.config.Noop,
		}, cfg.filings.filter)

		commandErr = err
	case "serve":
		logger.Info("starting EDGAR local dashboard", "address", cfg.serve.address,
			"parsed_directory", cfg.serve.parsedDir, "set", cfg.serve.setName)

		commandErr = runServe(ctx, logger, cfg.serve.address, cfg.serve.parsedDir,
			cfg.serve.setName)
	case "taxonomy":
		commandErr = runTaxonomyCommandWithLimit(ctx, cfg.taxonomy.operation, cfg.taxonomy.file,
			cfg.taxonomy.taxonomy, cfg.taxonomy.parsedDir, cfg.taxonomy.filter,
			cfg.taxonomy.setName, cfg.taxonomy.fromYear, cfg.taxonomy.toYear,
			cfg.taxonomy.limit, cfg.taxonomy.format, cfg.taxonomy.out)
	case "coverage":
		commandErr = runCoverageCommand(ctx, cfg)
	default:
		printUsage()

		return flag.ErrHelp
	}

	return classifyCLIError(ctx, commandErr)
}

func classifyCLIError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}

	logger := ctxlog.FromContext(ctx)
	logger.Debug("EDGAR command error detail", "error", err.Error())

	if _, ok := errors.AsType[*xerr.Error](err); ok {
		return err
	}

	switch {
	case errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled):
		return xerr.Wrap(xerr.Canceled, "COMMAND_CANCELED", "command was canceled", err)
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
		return xerr.Wrap(xerr.DeadlineExceeded, "COMMAND_TIMEOUT", "command timed out", err)
	case errors.Is(err, os.ErrNotExist):
		return xerr.Wrap(xerr.NotFound, "LOCAL_DATA_NOT_FOUND", "required local data was not found", err)
	default:
		if httpError, ok := errors.AsType[*edgar.HTTPError](err); ok {
			logger.Debug("EDGAR HTTP error detail",
				"url", httpError.URL,
				"status_code", httpError.StatusCode,
				"status", httpError.Status,
			)

			return xerr.Wrap(xerr.Unavailable, "EDGAR_REJECTED_REQUEST", "EDGAR rejected the request", err).
				WithDetails(map[string]any{
					"url":         httpError.URL,
					"status_code": httpError.StatusCode,
					"status":      httpError.Status,
				})
		}

		if _, ok := errors.AsType[net.Error](err); ok {
			return xerr.Wrap(xerr.Unavailable, "EDGAR_UNAVAILABLE", "EDGAR service is unavailable", err)
		}

		return xerr.Wrap(xerr.Internal, "EDGAR_COMMAND_FAILED", "EDGAR command failed", err)
	}
}

func parseConfig(args []string) (appConfig, error) {
	if len(args) == 0 {
		printUsage()

		return appConfig{}, flag.ErrHelp
	}

	if args[0] == "-h" || args[0] == "--help" {
		printUsage()

		return appConfig{}, flag.ErrHelp
	}

	switch args[0] {
	case parseCommand:
		return parseSubmissionConfig(args[1:])
	case "index":
		return parseDownloadIndexConfig(args[1:])
	case "filings":
		return parseDownloadFilingsConfig(args[1:])
	case "serve":
		return parseServeConfig(args[1:])
	case "taxonomy":
		return parseTaxonomyConfig(args[1:])
	case "coverage":
		return parseCoverageConfig(args[1:])
	default:
		printUsage()

		return appConfig{}, flag.ErrHelp
	}
}

func parseTaxonomyConfig(args []string) (appConfig, error) {
	var cfg appConfig

	cfg.command = "taxonomy"
	flags := flag.NewFlagSet("taxonomy", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.Usage = func() {
		printSubcommandHelp("taxonomy <lint|coverage|tune>", "Inspect local filing data against the taxonomy.", []helpOption{
			{"-i, --file <path>", "Inspect one filing-view.json or filing.json."},
			{"-c, --cik <cik>", "Select locally parsed filings for this CIK."},
			{"-s, --set <name>", "Select current members from data/sets/<name>.json."},
			{"-f, --form-type <type>", "Select this form type; may be repeated."},
			{"-y, --year <year>", "Select filings filed in this year."},
			{"    --from-year <year>", "First filing year to include."},
			{"    --to-year <year>", "Last filing year to include."},
			{"-d, --parsed-dir <path>", "Parsed filing directory; default ./data/parsed."},
			{"-t, --taxonomy <path>", "Use a taxonomy JSON file instead of the embedded default."},
			{"    --format <text|json>", "Output format; default text."},
			{"    --limit <n>", "Maximum tuning candidates; default 25."},
			{"-o, --out <path>", "Write output to this file instead of stdout."},
			{"-h, --help", "Show command help."},
		})
	}
	cfg.taxonomy.parsedDir = edgar.DefaultParsedDirectory
	cfg.taxonomy.format = "text"
	cfg.taxonomy.limit = 25
	flags.StringVar(&cfg.taxonomy.file, "i", "", "lint one compact filing-view.json")
	flags.StringVar(&cfg.taxonomy.file, "file", "", "lint one compact filing-view.json")
	flags.StringVar(&cfg.taxonomy.filter.CIK, "c", "", "select locally parsed filings for this CIK")
	flags.StringVar(&cfg.taxonomy.filter.CIK, "cik", "", "select locally parsed filings for this CIK")
	flags.StringVar(&cfg.taxonomy.setName, "s", "", "select current members from a constituent set")
	flags.StringVar(&cfg.taxonomy.setName, "set", "", "select current members from a constituent set")
	flags.Var(&cfg.taxonomy.formTypes, "f", "select this form type; may be repeated")
	flags.Var(&cfg.taxonomy.formTypes, "form-type", "select this form type; may be repeated")
	flags.StringVar(&cfg.taxonomy.year, "y", "", "select filings filed in this year")
	flags.StringVar(&cfg.taxonomy.year, "year", "", "select filings filed in this year")
	flags.IntVar(&cfg.taxonomy.fromYear, "from-year", 0, "first filing year to include")
	flags.IntVar(&cfg.taxonomy.toYear, "to-year", 0, "last filing year to include")
	flags.StringVar(&cfg.taxonomy.parsedDir, "d", cfg.taxonomy.parsedDir, "parsed filing directory")
	flags.StringVar(&cfg.taxonomy.parsedDir, "parsed-dir", cfg.taxonomy.parsedDir, "parsed filing directory")
	flags.StringVar(&cfg.taxonomy.taxonomy, "t", "", "taxonomy JSON file")
	flags.StringVar(&cfg.taxonomy.taxonomy, "taxonomy", "", "taxonomy JSON file")
	flags.StringVar(&cfg.taxonomy.format, "format", cfg.taxonomy.format, "output format")
	flags.IntVar(&cfg.taxonomy.limit, "limit", cfg.taxonomy.limit, "maximum tuning candidates")
	flags.StringVar(&cfg.taxonomy.out, "o", "", "output path")
	flags.StringVar(&cfg.taxonomy.out, "out", "", "output path")

	if len(args) == 0 || (args[0] != "lint" && args[0] != "coverage" && args[0] != "tune") {
		return subcommandError(flags, errors.New("use 'edgar taxonomy <lint|coverage|tune>'"))
	}

	cfg.taxonomy.operation = args[0]
	if err := flags.Parse(args[1:]); err != nil {
		return appConfig{}, flag.ErrHelp
	}

	if flags.NArg() != 0 {
		return subcommandError(flags, errors.New("use 'edgar taxonomy <lint|coverage|tune>'"))
	}

	if strings.TrimSpace(cfg.taxonomy.file) != "" &&
		(cfg.taxonomy.filter.CIK != "" || cfg.taxonomy.setName != "" ||
			len(cfg.taxonomy.formTypes) > 0 || strings.TrimSpace(cfg.taxonomy.year) != "" ||
			cfg.taxonomy.fromYear != 0 || cfg.taxonomy.toYear != 0) {
		return subcommandError(flags, errors.New("--file cannot be combined with filters"))
	}

	if cfg.taxonomy.parsedDir == "" {
		return subcommandError(flags, errors.New("--parsed-dir cannot be empty"))
	}

	if cfg.taxonomy.format != "text" && cfg.taxonomy.format != "json" {
		return subcommandError(flags, errors.New("--format must be text or json"))
	}

	if cfg.taxonomy.limit <= 0 {
		return subcommandError(flags, errors.New("--limit must be positive"))
	}

	cfg.taxonomy.filter.FormTypes = cfg.taxonomy.formTypes
	if err := applyConstituentSet(&cfg.taxonomy.filter, cfg.taxonomy.setName); err != nil {
		return subcommandError(flags, err)
	}

	if err := applyYearSelection(&cfg.taxonomy.filter, cfg.taxonomy.year,
		cfg.taxonomy.fromYear, cfg.taxonomy.toYear); err != nil {
		return subcommandError(flags, err)
	}

	return cfg, nil
}

type taxonomyBatchReport struct {
	SchemaVersion   int                    `json:"schema_version"`
	Operation       string                 `json:"operation"`
	TaxonomyVersion string                 `json:"taxonomy_version"`
	GeneratedAt     string                 `json:"generated_at"`
	Selection       taxonomySelection      `json:"selection"`
	Summary         taxonomyBatchSummary   `json:"summary"`
	Filings         []taxonomyFilingReport `json:"filings"`
}

type taxonomySelection struct {
	File      string   `json:"file,omitempty"`
	Set       string   `json:"set,omitempty"`
	CIK       string   `json:"cik,omitempty"`
	CIKs      []string `json:"ciks,omitempty"`
	FormTypes []string `json:"form_types,omitempty"`
	Year      int      `json:"year,omitempty"`
	FromYear  int      `json:"from_year,omitempty"`
	ToYear    int      `json:"to_year,omitempty"`
}

type taxonomyBatchSummary struct {
	Selected      int `json:"selected"`
	Failed        int `json:"failed"`
	Findings      int `json:"findings"`
	QualityIssues int `json:"quality_issues,omitempty"`
}

type taxonomyFilingReport struct {
	Path       string                     `json:"path"`
	CIK        string                     `json:"cik,omitempty"`
	Accession  string                     `json:"accession,omitempty"`
	FormType   string                     `json:"form_type,omitempty"`
	FilingDate string                     `json:"filing_date,omitempty"`
	Lint       *filingview.LintReport     `json:"lint,omitempty"`
	Coverage   *filingview.CoverageReport `json:"coverage,omitempty"`
	Error      string                     `json:"error,omitempty"`
}

func runTaxonomyCommand(ctx context.Context, operation, path, taxonomyPath,
	parsedDir string, filter edgar.IndexFilter, setName string, fromYear,
	toYear int, format, out string,
) error {
	return runTaxonomyCommandWithLimit(ctx, operation, path, taxonomyPath, parsedDir,
		filter, setName, fromYear, toYear, 25, format, out)
}

func runTaxonomyCommandWithLimit(ctx context.Context, operation, path, taxonomyPath,
	parsedDir string, filter edgar.IndexFilter, setName string, fromYear,
	toYear, limit int, format, out string,
) error {
	taxonomy, err := filingview.LoadTaxonomy(taxonomyPath)
	if err != nil {
		return err
	}

	if operation == "tune" {
		return runTaxonomyTuneCommand(ctx, path, parsedDir, filter, setName,
			fromYear, toYear, taxonomy, limit, format, out)
	}

	paths := []string{path}
	if path == "" {
		if operation == "coverage" {
			paths, err = parsedFilingPaths(ctx, parsedDir, filter, fromYear, toYear)
		} else {
			paths, err = filingViewPaths(ctx, parsedDir, filter, fromYear, toYear)
		}

		if err != nil {
			return err
		}
	}

	failed := 0
	findings := 0
	qualityIssues := 0
	batch := taxonomyBatchReport{
		SchemaVersion:   1,
		Operation:       operation,
		TaxonomyVersion: taxonomy.TaxonomyVersion,
		GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
		Selection: taxonomySelection{
			File: path, Set: setName, CIK: filter.CIK, CIKs: filter.CIKs,
			FormTypes: filter.FormTypes, Year: filter.Year,
			FromYear: fromYear, ToYear: toYear,
		},
		Summary: taxonomyBatchSummary{
			Selected: 0, Failed: 0, Findings: 0, QualityIssues: 0,
		},
		Filings: nil,
	}

	var text strings.Builder

	for _, filingPath := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}

		entry := taxonomyFilingReport{
			Path: filingPath, CIK: "", Accession: "", FormType: "",
			FilingDate: "", Lint: nil, Coverage: nil, Error: "",
		}

		if operation == "coverage" {
			filing, loadErr := edgar.LoadParsedFiling(filingPath)
			if loadErr != nil {
				fmt.Fprintf(&text, "%s error=%v\n", filingPath, loadErr)
				entry.Error = loadErr.Error()
				batch.Filings = append(batch.Filings, entry)

				failed++

				continue
			}

			report := filingview.Coverage(filing, taxonomy)
			findings += len(report.Unmapped)
			entry.CIK = filing.Metadata.CIK
			entry.Accession = filing.Metadata.Accession
			entry.FormType = filing.Metadata.FormType
			entry.FilingDate = filing.Metadata.FilingDate
			entry.Coverage = &report
			batch.Filings = append(batch.Filings, entry)

			fmt.Fprintf(&text, "%s cik=%s form=%s %s\n", filingPath,
				filing.Metadata.CIK, filing.Metadata.FormType, report)

			continue
		}

		view, loadErr := filingview.Load(filingPath)
		if loadErr != nil {
			fmt.Fprintf(&text, "%s error=%v\n", filingPath, loadErr)
			entry.Error = loadErr.Error()
			batch.Filings = append(batch.Filings, entry)

			failed++

			continue
		}

		report := filingview.LintView(view, taxonomy)
		findings += len(report.Unmapped)
		qualityIssues += len(report.QualityIssues)
		entry.CIK = view.Metadata.CIK
		entry.Accession = view.Metadata.Accession
		entry.FormType = view.Metadata.FormType
		entry.FilingDate = view.Metadata.FilingDate
		entry.Lint = &report
		batch.Filings = append(batch.Filings, entry)

		fmt.Fprintf(&text, "%s cik=%s form=%s %s\n", filingPath,
			view.Metadata.CIK, view.Metadata.FormType, report)
	}

	// Lint findings are review evidence for taxonomy work, not operational
	// failures. Only unreadable or malformed inputs increment failed and
	// produce a non-zero exit.
	batch.Summary = taxonomyBatchSummary{
		Selected: len(paths), Failed: failed, Findings: findings,
		QualityIssues: qualityIssues,
	}

	fmt.Fprintf(&text, "taxonomy %s summary selected=%d findings=%d failed=%d\n",
		operation, len(paths), findings, failed)

	if format == "json" {
		if err := writeTaxonomyJSON(out, batch); err != nil {
			return err
		}
	} else if err := writeTaxonomyText(out, text.String()); err != nil {
		return err
	}

	if failed > 0 {
		return fmt.Errorf("%d filing view(s) failed linting", failed)
	}

	return nil
}

type taxonomyTuneReport struct {
	SchemaVersion   int                     `json:"schema_version"`
	Operation       string                  `json:"operation"`
	TaxonomyVersion string                  `json:"taxonomy_version"`
	GeneratedAt     string                  `json:"generated_at"`
	Selection       taxonomySelection       `json:"selection"`
	Summary         taxonomyTuneSummary     `json:"summary"`
	Candidates      []taxonomyTuneCandidate `json:"candidates"`
}

type taxonomyTuneSummary struct {
	SelectedFilings int `json:"selected_filings"`
	ScannedFacts    int `json:"scanned_facts"`
	EligibleFacts   int `json:"eligible_facts"`
	Candidates      int `json:"candidates"`
}

type taxonomyTuneCandidate struct {
	Metric       string   `json:"metric"`
	Namespace    string   `json:"namespace"`
	Concept      string   `json:"concept"`
	Occurrences  int      `json:"occurrences"`
	Filings      int      `json:"filings"`
	Companies    int      `json:"companies"`
	Years        []int    `json:"years"`
	Units        []string `json:"units"`
	Examples     []string `json:"examples"`
	Confidence   string   `json:"confidence"`
	ReviewReason string   `json:"review_reason"`
}

type taxonomyTuneAccumulator struct {
	candidate taxonomyTuneCandidate

	filingKeys  map[string]struct{}
	companyKeys map[string]struct{}
	years       map[int]struct{}
	units       map[string]struct{}
	examples    map[string]struct{}
}

// runTaxonomyTuneCommand is intentionally a candidate screen, not an
// automatic taxonomy learner. It limits evidence to numeric, non-dimensional
// standard US-GAAP facts and emits suggestions for human review only.
func runTaxonomyTuneCommand(ctx context.Context, path, parsedDir string,
	filter edgar.IndexFilter, setName string, fromYear, toYear int,
	taxonomy filingview.Taxonomy, limit int, format, out string,
) error {
	paths := []string{path}

	var err error
	if path == "" {
		paths, err = parsedFilingPaths(ctx, parsedDir, filter, fromYear, toYear)
		if err != nil {
			return err
		}
	}

	coreMetrics := make(map[string]string)
	mappedConcepts := make(map[string]struct{})

	for _, metric := range taxonomy.Metrics {
		for _, concept := range metric.Concepts {
			if concept.NamespaceFamily == "us-gaap" {
				mappedConcepts[concept.Name] = struct{}{}
			}
		}

		if metric.CoverageTier == "" {
			coreMetrics[metric.Key] = metric.Label
		}
	}

	accumulators := make(map[string]*taxonomyTuneAccumulator)
	scannedFacts, eligibleFacts := 0, 0

	for _, filingPath := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}

		filing, loadErr := edgar.LoadParsedFiling(filingPath)
		if loadErr != nil {
			return fmt.Errorf("load %s: %w", filingPath, loadErr)
		}

		for _, instance := range filing.Instances {
			contexts := make(map[string]edgar.FactContext, len(instance.Contexts))
			for _, context := range instance.Contexts {
				contexts[context.ID] = context
			}

			for _, fact := range instance.Facts {
				scannedFacts++

				if fact.Nil || fact.UnitRef == "" || !isUSGAAPNamespace(fact.Concept.Namespace) {
					continue
				}

				context, ok := contexts[fact.ContextRef]
				if !ok || len(context.Dimensions) > 0 {
					continue
				}

				metric, ok := tuneMetricForConcept(fact.Concept.Local, coreMetrics)
				if !ok {
					continue
				}

				if _, mapped := mappedConcepts[fact.Concept.Local]; mapped {
					continue
				}

				eligibleFacts++
				key := metric + "\x00" + fact.Concept.Namespace + "\x00" + fact.Concept.Local

				candidate := accumulators[key]
				if candidate == nil {
					candidate = &taxonomyTuneAccumulator{
						candidate: taxonomyTuneCandidate{
							Metric: metric, Namespace: fact.Concept.Namespace,
							Concept: fact.Concept.Local, Confidence: "review",
							ReviewReason: "non-dimensional standard US-GAAP fact with a core-metric-like name; inspect presentation role and scope",
							Occurrences:  0, Filings: 0, Companies: 0, Years: nil, Units: nil, Examples: nil,
						},
						filingKeys: map[string]struct{}{}, companyKeys: map[string]struct{}{},
						years: map[int]struct{}{}, units: map[string]struct{}{}, examples: map[string]struct{}{},
					}
					accumulators[key] = candidate
				}

				candidate.candidate.Occurrences++
				candidate.filingKeys[filing.Metadata.Accession] = struct{}{}
				candidate.companyKeys[filing.Metadata.CIK] = struct{}{}

				candidate.units[fact.UnitRef] = struct{}{}
				if year := tuneFactYear(context); year != 0 {
					candidate.years[year] = struct{}{}
				}

				if len(candidate.examples) < 5 {
					candidate.examples[filing.Metadata.Accession] = struct{}{}
				}
			}
		}
	}

	candidates := make([]taxonomyTuneCandidate, 0, len(accumulators))
	for _, accumulator := range accumulators {
		candidate := accumulator.candidate
		candidate.Filings = len(accumulator.filingKeys)
		candidate.Companies = len(accumulator.companyKeys)
		candidate.Years = sortedTuneInts(accumulator.years)
		candidate.Units = sortedTuneStrings(accumulator.units)
		candidate.Examples = sortedTuneStrings(accumulator.examples)
		candidates = append(candidates, candidate)
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Occurrences != candidates[j].Occurrences {
			return candidates[i].Occurrences > candidates[j].Occurrences
		}

		if candidates[i].Companies != candidates[j].Companies {
			return candidates[i].Companies > candidates[j].Companies
		}

		return candidates[i].Concept < candidates[j].Concept
	})

	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	report := taxonomyTuneReport{
		SchemaVersion: 1, Operation: "tune", TaxonomyVersion: taxonomy.TaxonomyVersion,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Selection: taxonomySelection{
			File: path, Set: setName, CIK: filter.CIK,
			CIKs: filter.CIKs, FormTypes: filter.FormTypes, Year: filter.Year,
			FromYear: fromYear, ToYear: toYear,
		},
		Summary: taxonomyTuneSummary{
			SelectedFilings: len(paths), ScannedFacts: scannedFacts,
			EligibleFacts: eligibleFacts, Candidates: len(candidates),
		},
		Candidates: candidates,
	}

	if format == "json" {
		return writeTaxonomyJSON(out, report)
	}

	var text strings.Builder
	fmt.Fprintf(&text, "taxonomy tune summary selected=%d scanned_facts=%d eligible_facts=%d candidates=%d\n",
		report.Summary.SelectedFilings, scannedFacts, eligibleFacts, len(candidates))

	for _, candidate := range candidates {
		fmt.Fprintf(&text, "%s metric=%s occurrences=%d filings=%d companies=%d units=%s examples=%s\n",
			candidate.Concept, candidate.Metric, candidate.Occurrences, candidate.Filings,
			candidate.Companies, strings.Join(candidate.Units, ","), strings.Join(candidate.Examples, ","))
	}

	return writeTaxonomyText(out, text.String())
}

func isUSGAAPNamespace(namespace string) bool {
	return strings.Contains(namespace, "/us-gaap/") || strings.Contains(namespace, "/us-gaap:")
}

func tuneMetricForConcept(concept string, metrics map[string]string) (string, bool) {
	concept = strings.ToLower(concept)
	// These facts contain metric words but are not the consolidated dashboard
	// metric itself. Keep them out of the first-pass review queue.
	for _, excluded := range []string{
		"incomelossfromcontinuingoperationsbefore",
		"incomelossfromequitymethod",
		"attributabletononcontrollinginterest",
		"periodincrease",
		"dividend",
		"effectofexchange",
		"taxbenefits",
		"fairvalue",
		"deferredtaxassets",
		"deferredtaxliabilities",
		"deferredincometaxliabilities",
		"amortizationofintangibleassets",
		"otherliabilities",
		"otherassets",
		"availabletocommonstockholders",
		"noncash",
		"payments",
		"proceeds",
		"othercomprehensive",
		"hedge",
		"equitysecurities",
	} {
		if strings.Contains(concept, excluded) {
			return "", false
		}
	}

	keywords := []struct {
		metric string
		terms  []string
	}{
		{metric: "revenue", terms: []string{"revenue", "revenues", "salesrevenue"}},
		{metric: "gross_profit", terms: []string{"grossprofit"}},
		{metric: "operating_income", terms: []string{"operatingincome"}},
		{metric: "net_income", terms: []string{"netincome", "profitloss"}},
		{metric: "eps_diluted", terms: []string{"earningspersharediluted"}},
		{metric: "assets", terms: []string{"assets"}},
		{metric: "cash", terms: []string{"cash"}},
		{metric: "liabilities_and_equity", terms: []string{"liabilitiesandstockholdersequity"}},
		{metric: "liabilities", terms: []string{"liabilities"}},
		{metric: "equity", terms: []string{"equity", "stockholdersequity"}},
		{metric: "operating_cash_flow", terms: []string{"netcashprovidedbyusedinoperatingactivities"}},
		{metric: "investing_cash_flow", terms: []string{"netcashprovidedbyusedininvestingactivities"}},
		{metric: "financing_cash_flow", terms: []string{"netcashprovidedbyusedinfinancingactivities"}},
	}
	for _, keyword := range keywords {
		if _, ok := metrics[keyword.metric]; !ok {
			continue
		}

		for _, term := range keyword.terms {
			if strings.Contains(concept, term) {
				return keyword.metric, true
			}
		}
	}

	return "", false
}

func tuneFactYear(context edgar.FactContext) int {
	date := context.Instant
	if date == "" {
		date = context.EndDate
	}

	if len(date) < 4 {
		return 0
	}

	year, err := strconv.Atoi(date[:4])
	if err != nil {
		return 0
	}

	return year
}

func sortedTuneInts(values map[int]struct{}) []int {
	result := make([]int, 0, len(values))
	for value := range values {
		result = append(result, value)
	}

	sort.Ints(result)

	return result
}

func sortedTuneStrings(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}

	sort.Strings(result)

	return result
}

// filingViewPaths returns the paths to all filing-view.json files in the given directory that match the filter.
// The returned paths are sorted in lexicographical order.
func filingViewPaths(ctx context.Context, directory string, filter edgar.IndexFilter, fromYear, toYear int) ([]string, error) {
	paths := []string{}

	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		if walkErr != nil {
			return walkErr
		}

		if entry.IsDir() || entry.Name() != "filing-view.json" {
			return nil
		}

		view, err := filingview.Load(path)
		if err != nil {
			return fmt.Errorf("inspect %s: %w", path, err)
		}

		dateFiled, _ := parseFilingDate(view.Metadata.FilingDate)
		if !inYearRange(dateFiled.Year(), fromYear, toYear) {
			return nil
		}

		if !edgar.MatchesIndexFilter(edgar.EdgarIndex{
			CIK: view.Metadata.CIK, CompanyName: view.Metadata.Company,
			FormType: view.Metadata.FormType, DateFiled: dateFiled,
			FilingPath: "local", IndexPath: "",
		}, filter) {
			return nil
		}

		paths = append(paths, path)

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan parsed directory: %w", err)
	}

	sort.Strings(paths)

	return paths, nil
}

// parsedFilingPaths returns the paths to all filing.json files in the given directory that match the filter.
// The returned paths are sorted in lexicographical order.
func parsedFilingPaths(ctx context.Context, directory string, filter edgar.IndexFilter, fromYear, toYear int) ([]string, error) {
	paths := []string{}

	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		if walkErr != nil {
			return walkErr
		}

		if entry.IsDir() || entry.Name() != "filing.json" {
			return nil
		}

		filing, err := edgar.LoadParsedFiling(path)
		if err != nil {
			return fmt.Errorf("inspect %s: %w", path, err)
		}

		dateFiled, _ := parseFilingDate(filing.Metadata.FilingDate)
		if !inYearRange(dateFiled.Year(), fromYear, toYear) {
			return nil
		}

		if !edgar.MatchesIndexFilter(edgar.EdgarIndex{
			CIK: filing.Metadata.CIK, CompanyName: "",
			FormType: filing.Metadata.FormType, DateFiled: dateFiled,
			FilingPath: "local", IndexPath: "",
		}, filter) {
			return nil
		}

		paths = append(paths, path)

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan parsed directory: %w", err)
	}

	sort.Strings(paths)

	return paths, nil
}

func writeTaxonomyJSON(path string, report any) error {
	var (
		writer io.Writer = os.Stdout
		file   *os.File
	)

	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return fmt.Errorf("create taxonomy report directory: %w", err)
		}

		created, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("create taxonomy report: %w", err)
		}

		file = created
		writer = file
	}

	if file != nil {
		defer func() { _ = file.Close() }()
	}

	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("write taxonomy report: %w", err)
	}

	return nil
}

func writeTaxonomyText(path, text string) error {
	if path == "" {
		fmt.Fprint(os.Stdout, text)

		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create taxonomy report directory: %w", err)
	}

	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return fmt.Errorf("write taxonomy report: %w", err)
	}

	return nil
}

func inYearRange(year, fromYear, toYear int) bool {
	return (fromYear == 0 || year >= fromYear) && (toYear == 0 || year <= toYear)
}

func parseFilingDate(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	for _, layout := range []string{"2006-01-02", "20060102"} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed, nil
		}
	}

	return time.Time{}, fmt.Errorf("invalid filing date %q", value)
}

func applyYearSelection(filter *edgar.IndexFilter, year string, fromYear, toYear int) error {
	if fromYear != 0 && (fromYear < edgar.EarliestYear || fromYear > 9999) {
		return errors.New("--from-year must be a valid EDGAR year")
	}

	if toYear != 0 && (toYear < edgar.EarliestYear || toYear > 9999) {
		return errors.New("--to-year must be a valid EDGAR year")
	}

	if fromYear != 0 && toYear != 0 && fromYear > toYear {
		return errors.New("--from-year must not be after --to-year")
	}

	if strings.TrimSpace(year) != "" {
		if fromYear != 0 || toYear != 0 {
			return errors.New("--year cannot be combined with --from-year or --to-year")
		}

		parsedYear, err := strconv.Atoi(strings.TrimSpace(year))
		if err != nil || parsedYear < 1000 || parsedYear > 9999 {
			return errors.New("--year must be a four-digit year")
		}

		filter.Year = parsedYear

		return nil
	}

	filter.FromYear = fromYear
	filter.ToYear = toYear

	return nil
}

func parseDownloadIndexConfig(args []string) (appConfig, error) {
	var cfg appConfig

	cfg.command = "index"
	cfg.index = edgar.Config{
		Directory:     "./data/indexes/quarterly",
		ZipDirectory:  "./data/cache/index-zips",
		MasterPath:    "./data/indexes/master.tsv",
		SinceYear:     edgar.EarliestYear,
		UserAgent:     environmentValue(edgarUserAgentEnv, defaultUserAgent),
		RefreshLatest: false,
		Stitch:        true,
		BaseURL:       environmentValue(edgarBaseURLEnv, ""),
		Noop:          false,
	}

	flags := flag.NewFlagSet("index", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.Usage = func() {
		printSubcommandHelp("index", "Download SEC quarterly filing indexes.", []helpOption{
			{"-y, --from-year <year>", "First year to download."},
			{"-r, --refresh-latest", "Refresh the latest quarter and reuse older files."},
			{"-s, --stitch", "Concatenate quarterly TSV files into master.tsv."},
			{noopHelpFlags, "Show actions without downloading, extracting, or writing files."},
		})
	}
	flags.IntVar(&cfg.index.SinceYear, "y", cfg.index.SinceYear, "first year to download")
	flags.IntVar(&cfg.index.SinceYear, "from-year", cfg.index.SinceYear, "first year to download")
	flags.BoolVar(&cfg.index.RefreshLatest, "r", false, "refresh the latest quarter and reuse older files")
	flags.BoolVar(&cfg.index.RefreshLatest, "refresh-latest", false, "refresh the latest quarter and reuse older files")
	flags.BoolVar(&cfg.index.Stitch, "s", cfg.index.Stitch, "concatenate quarterly TSV files into master.tsv")
	flags.BoolVar(&cfg.index.Stitch, "stitch", cfg.index.Stitch, "concatenate quarterly TSV files into master.tsv")
	flags.BoolVar(&cfg.index.Noop, "n", false, "show actions without downloading, extracting, or writing files")
	flags.BoolVar(&cfg.index.Noop, "noop", false, "show actions without downloading, extracting, or writing files")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return appConfig{}, err
		}

		return appConfig{}, flag.ErrHelp
	}

	if cfg.index.SinceYear < edgar.EarliestYear {
		return subcommandError(flags, fmt.Errorf("--from-year must be %d or later", edgar.EarliestYear))
	}

	return cfg, nil
}

func parseDownloadFilingsConfig(args []string) (appConfig, error) {
	formTypes := stringList{}

	var cfg appConfig

	cfg.command = "filings"
	cfg.filings.masterPath = "./data/indexes/master.tsv"
	cfg.filings.config = edgar.FilingDownloadConfig{
		Directory: edgar.DefaultFilingsDirectory,
		UserAgent: environmentValue(edgarUserAgentEnv, defaultUserAgent),
		BaseURL:   environmentValue(edgarBaseURLEnv, ""),
		Noop:      false,
	}
	flags := flag.NewFlagSet("filings", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.Usage = func() {
		printSubcommandHelp("filings", "Download filings selected from master.tsv and reuse or rebuild parsed results.", []helpOption{
			{"-c, --cik <cik>", "Select filings for this CIK."},
			{"    --set <name>", "Select current members from data/sets/<name>.json."},
			{"-f, --form-type <type>", "Select this form type; may be repeated."},
			{"-y, --year <year>", "Select filings filed in this year; default is all years."},
			{"    --from-year <year>", "First filing year to include."},
			{"    --to-year <year>", "Last filing year to include."},
			{"-r, --reprocess", "Rebuild parsed results without forcing a new download."},
			{noopHelpFlags, "Show planned downloads and processing without changing files."},
		})
	}
	flags.StringVar(&cfg.filings.filter.CIK, "c", "", "only download filings for this CIK")
	flags.StringVar(&cfg.filings.filter.CIK, "cik", "", "only download filings for this CIK")
	flags.StringVar(&cfg.filings.setName, "set", "", "only download filings for this constituent set")
	flags.Var(&formTypes, "form-type", "only download this form type; may be repeated")
	flags.Var(&formTypes, "f", "only download this form type; may be repeated")
	flags.BoolVar(&cfg.filings.config.Noop, "noop", false, "show actions without downloading or writing files")
	flags.BoolVar(&cfg.filings.config.Noop, "n", false, "show actions without downloading or writing files")
	flags.StringVar(&cfg.filings.year, "y", "", "only download filings filed in this year")
	flags.StringVar(&cfg.filings.year, "year", "", "only download filings filed in this year")
	flags.IntVar(&cfg.filings.fromYear, "from-year", 0, "first filing year to include")
	flags.IntVar(&cfg.filings.toYear, "to-year", 0, "last filing year to include")
	flags.BoolVar(&cfg.filings.reprocess, "r", false, "rebuild parsed results")
	flags.BoolVar(&cfg.filings.reprocess, "reprocess", false, "rebuild parsed results")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return appConfig{}, err
		}

		return appConfig{}, flag.ErrHelp
	}

	if strings.TrimSpace(cfg.filings.config.UserAgent) == "" {
		return subcommandError(flags, fmt.Errorf("%s cannot be empty", edgarUserAgentEnv))
	}

	if flags.NArg() != 0 {
		return subcommandError(flags, errors.New("unexpected positional arguments"))
	}

	cfg.filings.filter.FormTypes = formTypes
	if err := applyConstituentSet(&cfg.filings.filter, cfg.filings.setName); err != nil {
		return subcommandError(flags, err)
	}

	if err := applyYearSelection(&cfg.filings.filter, cfg.filings.year,
		cfg.filings.fromYear, cfg.filings.toYear); err != nil {
		return subcommandError(flags, err)
	}

	return cfg, nil
}

func applyConstituentSet(filter *edgar.IndexFilter, name string) error {
	name = strings.TrimSpace(name)

	if name == "" {
		return nil
	}

	if filepath.Base(name) != name || filepath.Ext(name) != "" {
		return errors.New("--set must be a set name, not a path")
	}

	set, err := constituents.LoadJSON(filepath.Join(defaultSetDir, name+".json"))
	if err != nil {
		return err
	}

	filter.CIKs = set.CIKs()

	return nil
}

func subcommandError(flags *flag.FlagSet, err error) (appConfig, error) {
	fmt.Fprintf(os.Stdout, "Error: %v\n\n", err)
	flags.Usage()

	return appConfig{}, flag.ErrHelp
}

func environmentValue(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}

	return fallback
}

func printUsage() {
	fmt.Fprintln(os.Stdout, "Usage: edgar <command> [options]")
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "Commands:")
	fmt.Fprintln(os.Stdout, "  index             download quarterly SEC filing indexes")
	fmt.Fprintln(os.Stdout, "  filings           download and process filings selected from master.tsv")
	fmt.Fprintln(os.Stdout, "  parse             read local submissions and persist XBRL data")
	fmt.Fprintln(os.Stdout, "  serve             browse locally parsed filing data")
	fmt.Fprintln(os.Stdout, "  taxonomy          lint, cover, or tune local filing taxonomy")
	fmt.Fprintln(os.Stdout, "  coverage          report expected local filing coverage")
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "Use 'edgar <command> --help' for command-specific options.")
}

func printSubcommandHelp(command, description string, options []helpOption) {
	fmt.Fprintf(os.Stdout, "Usage: edgar %s [options]\n\n", command)
	fmt.Fprintln(os.Stdout, description)
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "Options:")
	fmt.Fprintln(os.Stdout)

	for _, option := range options {
		fmt.Fprintf(os.Stdout, "  %s\n", option.flags)
		fmt.Fprintf(os.Stdout, "      %s\n\n", option.description)
	}
}
