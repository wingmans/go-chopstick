// Package main downloads SEC EDGAR data and parses local filing submissions.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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
		noop       bool
		reprocess  bool
	}
	filings struct {
		masterPath string
		config     edgar.FilingDownloadConfig
		filter     edgar.IndexFilter
		setName    string
		reprocess  bool
	}
	serve struct {
		address   string
		parsedDir string
	}
	taxonomy struct {
		file     string
		taxonomy string
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
		logger.Info("starting EDGAR local dashboard", "address", cfg.serve.address, "parsed_directory", cfg.serve.parsedDir)

		commandErr = runServe(ctx, logger, cfg.serve.address, cfg.serve.parsedDir)
	case "taxonomy":
		commandErr = runTaxonomyLint(cfg.taxonomy.file, cfg.taxonomy.taxonomy)
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
		if _, ok := errors.AsType[*edgar.HTTPError](err); ok {
			return xerr.Wrap(xerr.Unavailable, "EDGAR_REJECTED_REQUEST", "EDGAR rejected the request", err)
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
	default:
		printUsage()

		return appConfig{}, flag.ErrHelp
	}
}

//nolint:wsl_v5 // Command parsing is kept in one readable validation block.
func parseTaxonomyConfig(args []string) (appConfig, error) {
	var cfg appConfig
	cfg.command = "taxonomy"
	flags := flag.NewFlagSet("taxonomy", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.Usage = func() {
		printSubcommandHelp("taxonomy lint", "Inspect a compact filing view against the taxonomy.", []helpOption{
			{"-f, --file <path>", "Compact filing-view.json to inspect."},
			{"-t, --taxonomy <path>", "Use a taxonomy JSON file instead of the embedded default."},
			{"-h, --help", "Show command help."},
		})
	}
	flags.StringVar(&cfg.taxonomy.file, "f", "", "compact filing-view.json to inspect")
	flags.StringVar(&cfg.taxonomy.file, "file", "", "compact filing-view.json to inspect")
	flags.StringVar(&cfg.taxonomy.taxonomy, "t", "", "taxonomy JSON file")
	flags.StringVar(&cfg.taxonomy.taxonomy, "taxonomy", "", "taxonomy JSON file")
	if err := flags.Parse(args); err != nil {
		return appConfig{}, flag.ErrHelp
	}
	if flags.NArg() != 1 || flags.Arg(0) != "lint" {
		return subcommandError(flags, errors.New("use 'edgar taxonomy lint --file <path>'"))
	}
	if strings.TrimSpace(cfg.taxonomy.file) == "" {
		return subcommandError(flags, errors.New("--file is required"))
	}

	return cfg, nil
}

//nolint:wsl_v5 // Loading, linting, and reporting form one boundary operation.
func runTaxonomyLint(path, taxonomyPath string) error {
	view, err := filingview.Load(path)
	if err != nil {
		return fmt.Errorf("load filing view: %w", err)
	}
	taxonomy, err := filingview.LoadTaxonomy(taxonomyPath)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(os.Stdout, filingview.LintView(view, taxonomy))

	return err
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
	year := ""

	flags := flag.NewFlagSet("filings", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.Usage = func() {
		printSubcommandHelp("filings", "Download filings selected from master.tsv and reuse or rebuild parsed results.", []helpOption{
			{"-c, --cik <cik>", "Select filings for this CIK."},
			{"    --set <name>", "Select current members from data/sets/<name>.json."},
			{"-f, --form-type <type>", "Select this form type; may be repeated."},
			{"-y, --year <year>", "Select filings filed in this year; default is all years."},
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
	flags.StringVar(&year, "y", "", "only download filings filed in this year")
	flags.StringVar(&year, "year", "", "only download filings filed in this year")
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

	if strings.TrimSpace(year) != "" {
		parsedYear, err := strconv.Atoi(strings.TrimSpace(year))
		if err != nil || parsedYear < 1000 || parsedYear > 9999 {
			return subcommandError(flags, errors.New("--year must be a four-digit year"))
		}

		cfg.filings.filter.Year = parsedYear
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
	fmt.Fprintln(os.Stdout, "  taxonomy          lint compact filing views")
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
