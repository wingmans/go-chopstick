// Command yahoo-prices downloads experimental Yahoo Finance market data.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	marketyahoo "wingman.com/chopstick/internal/market"
	"wingman.com/chopstick/internal/platform/appdata"
	"wingman.com/chopstick/internal/platform/ctxlog"
	"wingman.com/chopstick/internal/platform/xerr"
)

type tickerFlags []string

func (values *tickerFlags) String() string {
	return strings.Join(*values, ",")
}

// Set parses and validates a ticker flag value.
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

	logger := ctxlog.New()
	ctx = ctxlog.WithLogger(ctx, logger)

	if err := run(ctx, os.Args[1:], os.Stdout, logger, time.Now); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}

		if preflight, ok := errors.AsType[*xerr.PreflightError](err); ok {
			fmt.Fprintln(os.Stderr, preflight)

			return 1
		}

		logger.Error("Yahoo market-data command failed", "error", err)

		if errors.Is(err, marketyahoo.ErrSyncIncomplete) {
			return 2
		}

		return 1
	}

	return 0
}

func run(ctx context.Context, args []string, stdout io.Writer,
	logger *slog.Logger, now func() time.Time,
) error {
	cfg, err := parseConfig(args, stdout, now().UTC())
	if err != nil {
		return err
	}

	return marketyahoo.Run(ctx, cfg, stdout, logger, now)
}

func parseConfig(args []string, output io.Writer,
	now time.Time,
) (marketyahoo.Config, error) {
	if len(args) == 0 {
		writeUsage(output)

		return marketyahoo.Config{}, errors.New(
			"missing command: expected fetch, sync, status, or repair",
		)
	}

	command := args[0]
	if command != "fetch" && command != "sync" &&
		command != "status" && command != "repair" {
		writeUsage(output)

		return marketyahoo.Config{}, fmt.Errorf(
			"unknown command %q: expected fetch, sync, status, or repair",
			command,
		)
	}

	var (
		batchDir     string
		databasePath string
		fromValue    string
		manifestPath string
		toValue      string
		tickers      tickerFlags
		maxDuration  time.Duration
		maxSymbols   int
		syncTimeout  time.Duration
	)

	flags := flag.NewFlagSet("yahoo-prices "+command, flag.ContinueOnError)
	flags.SetOutput(output)
	flags.Usage = func() { writeCommandUsage(output, command, flags) }
	flags.StringVar(&fromValue, "from", "1900-01-01", "bootstrap or repair start date (YYYY-MM-DD)")
	flags.StringVar(&toValue, "to", marketyahoo.DefaultTargetDate(now).Format(marketyahoo.DateLayout), "last covered calendar date (YYYY-MM-DD)")
	flags.StringVar(&databasePath, "database", appdata.Path("market.db"), "SQLite database path")
	flags.StringVar(&batchDir, "batch-dir", appdata.Path("market", "yahoo", "batches"), "directory for normalized CSV landing batches")
	flags.StringVar(&manifestPath, "manifest", marketyahoo.DefaultManifest, "constituent manifest path")
	flags.Var(&tickers, "ticker", "manifest ticker; may be repeated")
	flags.IntVar(&maxSymbols, "max-symbols", marketyahoo.DefaultMaxSymbols, "maximum symbols attempted by fetch; zero disables the limit")
	flags.DurationVar(&maxDuration, "max-duration", 0, "stop starting work after this duration; zero disables the limit")
	flags.DurationVar(&syncTimeout, "sync-timeout", marketyahoo.DefaultSyncTimeout, "maximum duration of one sync cycle; zero disables the timeout")

	if err := flags.Parse(args[1:]); err != nil {
		return marketyahoo.Config{}, err
	}

	if flags.NArg() != 0 {
		return marketyahoo.Config{}, fmt.Errorf(
			"unexpected arguments: %s", flags.Arg(0))
	}

	from, err := parseDate("from", fromValue)
	if err != nil {
		return marketyahoo.Config{}, err
	}

	through, err := parseDate("to", toValue)
	if err != nil {
		return marketyahoo.Config{}, err
	}

	visited := make(map[string]bool)

	flags.Visit(func(item *flag.Flag) { visited[item.Name] = true })

	if command == "repair" &&
		(len(tickers) == 0 || !visited["from"] || !visited["to"]) {
		return marketyahoo.Config{}, errors.New(
			"repair requires --ticker, --from, and --to")
	}

	return marketyahoo.Config{
		Command: command, ManifestPath: manifestPath,
		DatabasePath: databasePath, BatchDir: batchDir,
		From: from, Through: through, Tickers: tickers,
		MaxSymbols: maxSymbols, MaxDuration: maxDuration,
		SyncTimeout: syncTimeout,
	}, nil
}

func writeUsage(output io.Writer) {
	fmt.Fprintln(output,
		"Usage: yahoo-prices <fetch|sync|status|repair> [options]")
	fmt.Fprintln(output)
	fmt.Fprintln(output,
		"Land Yahoo prices and corporate actions in CSV, then import SQLite.")
}

func writeCommandUsage(output io.Writer, command string, flags *flag.FlagSet) {
	fmt.Fprintf(output, "Usage: yahoo-prices %s [options]\n\n", command)
	flags.PrintDefaults()
}

func parseDate(name, value string) (time.Time, error) {
	parsed, err := time.Parse(marketyahoo.DateLayout, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, fmt.Errorf(
			"invalid %s date %q: %w", name, value, err)
	}

	return parsed, nil
}
