// Package main downloads experimental Yahoo Finance daily price data.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	defaultManifest = "golden/us-gaap.json"
	dateLayout      = "2006-01-02"
)

type config struct {
	manifestPath string
	outputDir    string
	from         time.Time
	to           time.Time
	tickers      []string
}

type tickerFlags []string

func (values *tickerFlags) String() string {
	return strings.Join(*values, ",")
}

func (values *tickerFlags) Set(value string) error {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" {
		return errors.New("ticker must not be empty")
	}

	*values = append(*values, value)

	return nil
}

func main() {
	os.Exit(realMain())
}

func realMain() int {
	ctx, stop := signal.NotifyContext(
		context.Background(), os.Interrupt, syscall.SIGTERM,
	)
	defer stop()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	if err := run(ctx, os.Args[1:], os.Stdout, logger, time.Now); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}

		logger.Error("Yahoo price download failed", "error", err)

		return 1
	}

	return 0
}

func run(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	logger *slog.Logger,
	now func() time.Time,
) error {
	cfg, err := parseConfig(args, stdout, now().UTC())
	if err != nil {
		return err
	}

	manifest, err := readManifest(cfg.manifestPath)
	if err != nil {
		return err
	}

	if cfg.outputDir == "" {
		cfg.outputDir = defaultOutputDir(manifest.Name)
	}

	members, err := selectMembers(manifest.Members, cfg.tickers)
	if err != nil {
		return err
	}

	client := newYahooClient(
		&http.Client{Timeout: 30 * time.Second}, yahooChartURL, logger,
	)

	store, err := newCSVPriceStore(cfg.outputDir)
	if err != nil {
		return err
	}

	result, err := download(
		ctx, client, cfg, members, store, store, logger,
	)
	if err != nil {
		return err
	}

	fmt.Fprintf(
		stdout,
		"stored %d downloaded prices in %d yearly partitions at %s\n",
		result.observations,
		result.partitions,
		filepath.Join(cfg.outputDir, pricesDirectory),
	)

	return nil
}

func parseConfig(args []string, output io.Writer, now time.Time) (config, error) {
	if len(args) == 0 || args[0] != "fetch" {
		return config{}, usageError(args, output)
	}

	var fromValue, toValue, outputDir string

	var tickers tickerFlags

	flags := flag.NewFlagSet("yahoo-prices fetch", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.Usage = func() { writeUsage(output, flags) }
	flags.StringVar(
		&fromValue, "from", "1900-01-01", "first date to retrieve (YYYY-MM-DD)",
	)
	flags.StringVar(
		&toValue, "to", now.UTC().Format(dateLayout),
		"last date to retrieve, inclusive (YYYY-MM-DD)",
	)
	flags.StringVar(
		&outputDir, "output-dir", "", "market-data storage directory",
	)
	flags.Var(
		&tickers, "ticker", "manifest ticker to fetch; may be repeated",
	)

	manifestPath := defaultManifest
	flags.StringVar(
		&manifestPath, "manifest", manifestPath, "constituent manifest path",
	)

	if err := flags.Parse(args[1:]); err != nil {
		return config{}, err
	}

	if flags.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected arguments: %s", flags.Arg(0))
	}

	from, err := parseDate("from", fromValue)
	if err != nil {
		return config{}, err
	}

	to, err := parseDate("to", toValue)
	if err != nil {
		return config{}, err
	}

	if to.Before(from) {
		return config{}, errors.New("to date must not be before from date")
	}

	return config{
		manifestPath: manifestPath,
		outputDir:    cleanOptionalPath(outputDir),
		from:         from,
		to:           to,
		tickers:      tickers,
	}, nil
}

func usageError(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("yahoo-prices fetch", flag.ContinueOnError)
	flags.SetOutput(output)
	writeUsage(output, flags)

	if len(args) == 0 {
		return errors.New("missing command: expected fetch")
	}

	return fmt.Errorf("unknown command %q: expected fetch", args[0])
}

func writeUsage(output io.Writer, flags *flag.FlagSet) {
	fmt.Fprintln(output, "Usage: yahoo-prices fetch [options]")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Download Yahoo daily open, close, and adjusted close data.")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Prices are stored in yearly CSV partitions below:")
	fmt.Fprintln(output, "  data/cache/market/yahoo/<manifest-name>/prices/")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Existing tickers resume after their latest stored date.")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Options:")
	flags.PrintDefaults()
}

func parseDate(name, value string) (time.Time, error) {
	parsed, err := time.Parse(dateLayout, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid %s date %q: %w", name, value, err)
	}

	return parsed, nil
}

func cleanOptionalPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}

	return filepath.Clean(path)
}

func defaultOutputDir(manifestName string) string {
	dataDir := strings.TrimSpace(os.Getenv("CHOPSTICK_DATA_DIR"))
	if dataDir == "" {
		dataDir = "data"
	}

	return filepath.Join(
		dataDir, "cache", "market", "yahoo", safePathComponent(manifestName),
	)
}
