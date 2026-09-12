// Package main runs the SEC EDGAR index and filing downloader.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"wingman.com/fetch-ecb/internal/ctxlog"
	"wingman.com/fetch-ecb/internal/edgar"
)

const defaultUserAgent = "wingman paul@wingmen.io"

type appConfig struct {
	command string
	index   edgar.Config
	filings struct {
		masterPath string
		config     edgar.FilingDownloadConfig
		filter     edgar.IndexFilter
	}
}

type stringList []string

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
	case "download-index":
		logger.Info("starting EDGAR index download",
			"from_year", cfg.index.SinceYear,
			"directory", cfg.index.Directory,
			"stitch", cfg.index.Stitch,
		)

		return edgar.DownloadIndex(ctx, client, cfg.index)
	case "download-filings":
		logger.Info("starting EDGAR filing download",
			"master", cfg.filings.masterPath,
			"directory", cfg.filings.config.Directory,
			"cik", cfg.filings.filter.CIK,
			"form_types", cfg.filings.filter.FormTypes,
		)

		return edgar.DownloadIndexFiles(ctx, client, cfg.filings.masterPath, cfg.filings.config, cfg.filings.filter)
	default:
		return fmt.Errorf("unsupported command %q", cfg.command)
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
	case "download-index":
		return parseDownloadIndexConfig(args[1:])
	case "download-filings":
		return parseDownloadFilingsConfig(args[1:])
	default:
		return appConfig{}, fmt.Errorf("unknown command %q; use download-index or download-filings", args[0])
	}
}

func parseDownloadIndexConfig(args []string) (appConfig, error) {
	var cfg appConfig

	cfg.command = "download-index"
	cfg.index = edgar.Config{
		Directory:     "./data/indexes/quarterly",
		ZipDirectory:  "./data/raw/index-zips",
		MasterPath:    "./data/indexes/master.tsv",
		SinceYear:     edgar.EarliestYear,
		UserAgent:     defaultUserAgent,
		RefreshLatest: false,
		Stitch:        true,
		BaseURL:       "",
	}

	flags := flag.NewFlagSet("download-index", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.Usage = func() {
		fmt.Fprintln(os.Stdout, "Usage: edgar download-index [options]")
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "Download SEC quarterly filing indexes.")
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "Options:")
		flags.PrintDefaults()
	}
	flags.StringVar(&cfg.index.Directory, "d", cfg.index.Directory, "directory for downloaded index files")
	flags.StringVar(&cfg.index.Directory, "directory", cfg.index.Directory, "directory for downloaded index files")
	flags.StringVar(&cfg.index.ZipDirectory, "zip-directory", cfg.index.ZipDirectory, "directory for downloaded SEC index ZIP files")
	flags.StringVar(&cfg.index.MasterPath, "master", cfg.index.MasterPath, "path for the stitched master TSV")
	flags.IntVar(&cfg.index.SinceYear, "y", cfg.index.SinceYear, "first year to download")
	flags.IntVar(&cfg.index.SinceYear, "from-year", cfg.index.SinceYear, "first year to download")
	flags.StringVar(&cfg.index.UserAgent, "ua", cfg.index.UserAgent, "SEC User-Agent, including a contact email address")
	flags.StringVar(&cfg.index.UserAgent, "user-agent", cfg.index.UserAgent, "SEC User-Agent, including a contact email address")
	flags.BoolVar(&cfg.index.RefreshLatest, "r", false, "refresh the latest quarter and reuse older files")
	flags.BoolVar(&cfg.index.RefreshLatest, "refresh-latest", false, "refresh the latest quarter and reuse older files")
	flags.BoolVar(&cfg.index.Stitch, "s", cfg.index.Stitch, "concatenate quarterly TSV files into master.tsv")
	flags.BoolVar(&cfg.index.Stitch, "stitch", cfg.index.Stitch, "concatenate quarterly TSV files into master.tsv")
	flags.StringVar(&cfg.index.BaseURL, "base-url", "", "SEC Archives base URL")

	if err := flags.Parse(args); err != nil {
		return appConfig{}, err
	}

	if cfg.index.SinceYear < edgar.EarliestYear {
		return appConfig{}, fmt.Errorf("-from-year must be %d or later", edgar.EarliestYear)
	}

	if strings.TrimSpace(cfg.index.UserAgent) == "" {
		return appConfig{}, errors.New("-user-agent cannot be empty")
	}

	return cfg, nil
}

func parseDownloadFilingsConfig(args []string) (appConfig, error) {
	formTypes := stringList{}

	var cfg appConfig

	cfg.command = "download-filings"
	cfg.filings.masterPath = "./data/indexes/master.tsv"
	cfg.filings.config = edgar.FilingDownloadConfig{
		Directory: "./data/filings",
		UserAgent: defaultUserAgent,
		BaseURL:   "",
	}

	flags := flag.NewFlagSet("download-filings", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.Usage = func() {
		fmt.Fprintln(os.Stdout, "Usage: edgar download-filings [options]")
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "Download filing submissions and HTML filing indexes from a master TSV.")
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "Options:")
		flags.PrintDefaults()
	}
	flags.StringVar(&cfg.filings.masterPath, "m", cfg.filings.masterPath, "master TSV to read")
	flags.StringVar(&cfg.filings.masterPath, "master", cfg.filings.masterPath, "master TSV to read")
	flags.StringVar(&cfg.filings.config.Directory, "d", cfg.filings.config.Directory, "directory for downloaded filing files")
	flags.StringVar(&cfg.filings.config.Directory, "directory", cfg.filings.config.Directory, "directory for downloaded filing files")
	flags.StringVar(&cfg.filings.config.UserAgent, "ua", cfg.filings.config.UserAgent, "SEC User-Agent, including a contact email address")
	flags.StringVar(&cfg.filings.config.UserAgent, "user-agent", cfg.filings.config.UserAgent, "SEC User-Agent, including a contact email address")
	flags.StringVar(&cfg.filings.filter.CIK, "cik", "", "only download filings for this CIK")
	flags.Var(&formTypes, "form-type", "only download this form type; may be repeated")
	flags.Var(&formTypes, "f", "only download this form type; may be repeated")
	flags.StringVar(&cfg.filings.config.BaseURL, "base-url", "", "SEC Archives base URL")

	if err := flags.Parse(args); err != nil {
		return appConfig{}, err
	}

	if strings.TrimSpace(cfg.filings.masterPath) == "" {
		return appConfig{}, errors.New("-master cannot be empty")
	}

	if strings.TrimSpace(cfg.filings.config.Directory) == "" {
		return appConfig{}, errors.New("-directory cannot be empty")
	}

	if strings.TrimSpace(cfg.filings.config.UserAgent) == "" {
		return appConfig{}, errors.New("-user-agent cannot be empty")
	}

	cfg.filings.filter.FormTypes = formTypes

	return cfg, nil
}

func printUsage() {
	fmt.Fprintln(os.Stdout, "Usage: edgar <command> [options]")
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "Commands:")
	fmt.Fprintln(os.Stdout, "  download-index    download quarterly SEC filing indexes")
	fmt.Fprintln(os.Stdout, "  download-filings  download filing files referenced by a master TSV")
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "Use 'edgar <command> --help' for command-specific options.")
}
