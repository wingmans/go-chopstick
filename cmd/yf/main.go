// Package main downloads experimental Yahoo Finance market data.
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
	"strings"
	"syscall"
	"time"

	"wingman.com/fetch-ecb/internal/appdata"
	"wingman.com/fetch-ecb/internal/ctxlog"
	"wingman.com/fetch-ecb/internal/marketstore"
)

const (
	defaultManifest    = "golden/us-gaap.json"
	dateLayout         = "2006-01-02"
	providerYahoo      = "yahoo"
	defaultMaxSymbols  = 25
	defaultSchedule    = "21:00"
	defaultTimezone    = "America/New_York"
	defaultSyncTimeout = 2 * time.Hour
)

type config struct {
	command        string
	manifestPath   string
	databasePath   string
	batchDir       string
	from           time.Time
	to             time.Time
	tickers        []string
	maxSymbols     int
	maxDuration    time.Duration
	syncTimeout    time.Duration
	schedule       string
	scheduleHour   int
	scheduleMinute int
	timezone       string
}

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

		logger.Error("Yahoo market-data command failed", "error", err)

		if errors.Is(err, errSyncIncomplete) {
			return 2
		}

		return 1
	}

	return 0
}

func run(ctx context.Context, args []string, stdout io.Writer, logger *slog.Logger, now func() time.Time) error {
	currentTime := now().UTC()

	cfg, err := parseConfig(args, stdout, currentTime)
	if err != nil {
		return err
	}

	store, err := marketstore.Open(ctx, cfg.databasePath)
	if err != nil {
		return err
	}

	defer func() { _ = store.Close() }()

	client := newYahooClient(
		&http.Client{Timeout: 30 * time.Second}, yahooChartURL, logger,
	)

	batches, err := newCSVBatchStore(cfg.batchDir)
	if err != nil {
		return err
	}

	if cfg.command == "serve" {
		return executeDailyService(
			ctx, stdout, logger, store, batches, client, cfg, now,
		)
	}

	securities, err := loadSecurities(cfg)
	if err != nil {
		return err
	}

	if err := store.RegisterSecurities(
		ctx, providerYahoo, securities, currentTime,
	); err != nil {
		return err
	}

	switch cfg.command {
	case "repair":
		return queueRepairs(ctx, stdout, store, securities, cfg, currentTime)
	case "status":
		return reportStatus(ctx, stdout, store, securities, cfg, currentTime)
	case "fetch":
		return executeFetch(
			ctx, stdout, logger, store, batches, client, securities, cfg,
		)
	case "sync":
		return executeSync(
			ctx, stdout, logger, store, batches, client, securities, cfg,
		)
	default:
		return fmt.Errorf("unsupported command %q", cfg.command)
	}
}

func parseConfig(args []string, output io.Writer, now time.Time) (config, error) {
	if len(args) == 0 {
		writeUsage(output)

		return config{}, errors.New(
			"missing command: expected fetch, sync, serve, status, or repair",
		)
	}

	command := args[0]
	if command != "fetch" && command != "sync" && command != "serve" &&
		command != "status" && command != "repair" {
		writeUsage(output)

		return config{}, fmt.Errorf(
			"unknown command %q: expected fetch, sync, serve, status, or repair",
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
		schedule     string
		syncTimeout  time.Duration
		timezone     string
	)

	flags := flag.NewFlagSet("yahoo-prices "+command, flag.ContinueOnError)
	flags.SetOutput(output)
	flags.Usage = func() { writeCommandUsage(output, command, flags) }
	flags.StringVar(
		&fromValue, "from", "1900-01-01",
		"bootstrap or repair start date (YYYY-MM-DD)",
	)
	flags.StringVar(
		&toValue, "to", defaultTargetDate(now).Format(dateLayout),
		"last covered calendar date (YYYY-MM-DD)",
	)
	flags.StringVar(
		&databasePath, "database", appdata.Path("market.db"),
		"SQLite database path",
	)
	flags.StringVar(
		&batchDir, "batch-dir", appdata.Path("market", "yahoo", "batches"),
		"directory for normalized CSV landing batches",
	)
	flags.StringVar(
		&manifestPath, "manifest", defaultManifest,
		"constituent manifest path",
	)
	flags.Var(&tickers, "ticker", "manifest ticker; may be repeated")
	flags.IntVar(
		&maxSymbols, "max-symbols", defaultMaxSymbols,
		"maximum symbols attempted by fetch; zero disables the limit",
	)
	flags.DurationVar(
		&maxDuration, "max-duration", 0,
		"stop starting work after this duration; zero disables the limit",
	)
	flags.DurationVar(
		&syncTimeout, "sync-timeout", defaultSyncTimeout,
		"maximum duration of one sync cycle; zero disables the timeout",
	)
	flags.StringVar(
		&schedule, "schedule", defaultSchedule,
		"daily service start time in HH:MM",
	)
	flags.StringVar(
		&timezone, "timezone", defaultTimezone,
		"IANA timezone for the daily service",
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

	if maxSymbols < 0 {
		return config{}, errors.New("max-symbols must not be negative")
	}

	if maxDuration < 0 {
		return config{}, errors.New("max-duration must not be negative")
	}

	if syncTimeout < 0 {
		return config{}, errors.New("sync-timeout must not be negative")
	}

	scheduleHour, scheduleMinute, err := parseSchedule(schedule)
	if err != nil {
		return config{}, err
	}

	if _, err := time.LoadLocation(timezone); err != nil {
		return config{}, fmt.Errorf("invalid timezone %q: %w", timezone, err)
	}

	visited := make(map[string]bool)

	flags.Visit(func(item *flag.Flag) { visited[item.Name] = true })

	if command == "repair" {
		if len(tickers) == 0 || !visited["from"] || !visited["to"] {
			return config{}, errors.New(
				"repair requires --ticker, --from, and --to",
			)
		}
	}

	if command == "serve" && visited["to"] {
		return config{}, errors.New(
			"serve derives --to from its daily schedule; do not set --to",
		)
	}

	return config{
		command:        command,
		manifestPath:   manifestPath,
		databasePath:   databasePath,
		batchDir:       batchDir,
		from:           from,
		to:             to,
		tickers:        tickers,
		maxSymbols:     maxSymbols,
		maxDuration:    maxDuration,
		syncTimeout:    syncTimeout,
		schedule:       schedule,
		scheduleHour:   scheduleHour,
		scheduleMinute: scheduleMinute,
		timezone:       timezone,
	}, nil
}

func writeUsage(output io.Writer) {
	fmt.Fprintln(
		output,
		"Usage: yahoo-prices <fetch|sync|serve|status|repair> [options]",
	)
	fmt.Fprintln(output)
	fmt.Fprintln(
		output,
		"Land Yahoo prices and corporate actions in CSV, then import SQLite.",
	)
}

func writeCommandUsage(output io.Writer, command string, flags *flag.FlagSet) {
	fmt.Fprintf(output, "Usage: yahoo-prices %s [options]\n\n", command)
	flags.PrintDefaults()
}

func parseDate(name, value string) (time.Time, error) {
	parsed, err := time.Parse(dateLayout, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid %s date %q: %w", name, value, err)
	}

	return parsed, nil
}

func parseSchedule(value string) (int, int, error) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(value))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid schedule %q: %w", value, err)
	}

	return parsed.Hour(), parsed.Minute(), nil
}

func defaultTargetDate(now time.Time) time.Time {
	location, err := time.LoadLocation(defaultTimezone)
	if err != nil {
		return now.UTC().AddDate(0, 0, -1)
	}

	return scheduledThrough(now, location, 21, 0)
}

func marketSecurities(members []member) []marketstore.Security {
	securities := make([]marketstore.Security, 0, len(members))
	for _, item := range members {
		securities = append(securities, marketstore.Security{
			ID: item.SecurityID, Symbol: item.Ticker, Name: item.Name,
		})
	}

	return securities
}
