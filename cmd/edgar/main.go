// Package main downloads SEC EDGAR data and parses local filing submissions.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"wingman.com/fetch-ecb/internal/ctxlog"
	"wingman.com/fetch-ecb/internal/edgar"
)

const defaultUserAgent = "wingman paul@wingmen.io"

const (
	edgarUserAgentEnv = "EDGAR_USER_AGENT"
	edgarBaseURLEnv   = "EDGAR_BASE_URL"
	parseCommand      = "parse"
	noopHelpFlags     = "-n, --noop"
)

type appConfig struct {
	command string
	index   edgar.Config
	parse   struct {
		files stringList
		noop  bool
	}
	filings struct {
		masterPath string
		config     edgar.FilingDownloadConfig
		filter     edgar.IndexFilter
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

		return err
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

	switch cfg.command {
	case parseCommand:
		return runParse(ctx, cfg)
	case "index":
		logger.Info("starting EDGAR index download",
			"from_year", cfg.index.SinceYear,
			"directory", cfg.index.Directory,
			"stitch", cfg.index.Stitch,
			"noop", cfg.index.Noop,
		)

		return edgar.DownloadIndex(ctx, client, cfg.index)
	case "filings":
		logger.Info("starting EDGAR filing download",
			"master", cfg.filings.masterPath,
			"directory", cfg.filings.config.Directory,
			"cik", cfg.filings.filter.CIK,
			"form_types", cfg.filings.filter.FormTypes,
			"year", cfg.filings.filter.Year,
			"noop", cfg.filings.config.Noop,
		)

		return edgar.DownloadIndexFiles(ctx, client, cfg.filings.masterPath, cfg.filings.config, cfg.filings.filter)
	default:
		printUsage()

		return flag.ErrHelp
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
	default:
		printUsage()

		return appConfig{}, flag.ErrHelp
	}
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
		Directory: "./data/filings",
		UserAgent: environmentValue(edgarUserAgentEnv, defaultUserAgent),
		BaseURL:   environmentValue(edgarBaseURLEnv, ""),
		Noop:      false,
	}
	year := ""

	flags := flag.NewFlagSet("filings", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.Usage = func() {
		printSubcommandHelp("filings", "Download filing submissions and HTML filing indexes from a master TSV.", []helpOption{
			{"-c, --cik <cik>", "Only download filings for this CIK."},
			{"-f, --form-type <type>", "Only download this form type; may be repeated."},
			{"-y, --year <year>", "Only download filings filed in this year."},
			{noopHelpFlags, "Show actions without downloading or writing files."},
		})
	}
	flags.StringVar(&cfg.filings.filter.CIK, "c", "", "only download filings for this CIK")
	flags.StringVar(&cfg.filings.filter.CIK, "cik", "", "only download filings for this CIK")
	flags.Var(&formTypes, "form-type", "only download this form type; may be repeated")
	flags.Var(&formTypes, "f", "only download this form type; may be repeated")
	flags.BoolVar(&cfg.filings.config.Noop, "noop", false, "show actions without downloading or writing files")
	flags.BoolVar(&cfg.filings.config.Noop, "n", false, "show actions without downloading or writing files")
	flags.StringVar(&year, "y", "", "only download filings filed in this year")
	flags.StringVar(&year, "year", "", "only download filings filed in this year")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return appConfig{}, err
		}

		return appConfig{}, flag.ErrHelp
	}

	if strings.TrimSpace(cfg.filings.config.UserAgent) == "" {
		return subcommandError(flags, fmt.Errorf("%s cannot be empty", edgarUserAgentEnv))
	}

	cfg.filings.filter.FormTypes = formTypes

	if strings.TrimSpace(year) != "" {
		parsedYear, err := strconv.Atoi(strings.TrimSpace(year))
		if err != nil || parsedYear < 1000 || parsedYear > 9999 {
			return subcommandError(flags, errors.New("--year must be a four-digit year"))
		}

		cfg.filings.filter.Year = parsedYear
	}

	return cfg, nil
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
	fmt.Fprintln(os.Stdout, "  filings           download filing files referenced by a master TSV")
	fmt.Fprintln(os.Stdout, "  parse             read local submissions and persist XBRL data")
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
