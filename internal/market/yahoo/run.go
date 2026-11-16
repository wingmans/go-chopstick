// Package yahoo downloads Yahoo Finance prices and corporate actions into the
// local market-data store.
package yahoo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	marketstore "wingman.com/chopstick/internal/market/store"
	"wingman.com/chopstick/internal/outbound"
)

const (
	DefaultManifest    = "golden/us-gaap.json"
	DateLayout         = "2006-01-02"
	Provider           = "yahoo"
	DefaultMaxSymbols  = 25
	DefaultSchedule    = "21:00"
	DefaultTimezone    = "America/New_York"
	DefaultSyncTimeout = 2 * time.Hour

	dateLayout    = DateLayout
	providerYahoo = Provider
)

// Config describes one finite Yahoo market-data operation or the legacy daily
// service retained by the standalone yahoo-prices command.
type Config struct {
	Command        string
	ManifestPath   string
	DatabasePath   string
	BatchDir       string
	From           time.Time
	Through        time.Time
	Tickers        []string
	MaxSymbols     int
	MaxDuration    time.Duration
	SyncTimeout    time.Duration
	Schedule       string
	ScheduleHour   int
	ScheduleMinute int
	Timezone       string
}

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

// Run executes one Yahoo market-data operation.
func Run(ctx context.Context, cfg Config, output io.Writer,
	logger *slog.Logger, now func() time.Time,
) error {
	if err := validateConfig(cfg); err != nil {
		return err
	}

	internal := config{
		command: cfg.Command, manifestPath: cfg.ManifestPath,
		databasePath: cfg.DatabasePath, batchDir: cfg.BatchDir,
		from: cfg.From, to: cfg.Through,
		tickers:    append([]string(nil), cfg.Tickers...),
		maxSymbols: cfg.MaxSymbols, maxDuration: cfg.MaxDuration,
		syncTimeout: cfg.SyncTimeout, schedule: cfg.Schedule,
		scheduleHour: cfg.ScheduleHour, scheduleMinute: cfg.ScheduleMinute,
		timezone: cfg.Timezone,
	}

	store, err := marketstore.Open(ctx, internal.databasePath)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	client := newYahooClient(
		outbound.NewClient(30*time.Second), yahooChartURL, logger,
	)

	batches, err := newCSVBatchStore(internal.batchDir)
	if err != nil {
		return err
	}

	if internal.command == "serve" {
		return executeDailyService(
			ctx, output, logger, store, batches, client, internal, now,
		)
	}

	securities, err := loadSecurities(internal)
	if err != nil {
		return err
	}

	currentTime := now().UTC()
	if err := store.RegisterSecurities(
		ctx, providerYahoo, securities, currentTime,
	); err != nil {
		return err
	}

	switch internal.command {
	case "repair":
		return queueRepairs(ctx, output, store, securities, internal, currentTime)
	case "status":
		return reportStatus(ctx, output, store, securities, internal, currentTime)
	case "fetch":
		return executeFetch(
			ctx, output, logger, store, batches, client, securities, internal,
		)
	case "sync":
		return executeSync(
			ctx, output, logger, store, batches, client, securities, internal,
		)
	default:
		return fmt.Errorf("unsupported Yahoo command %q", internal.command)
	}
}

// DefaultTargetDate returns the latest complete market date for the default
// New York close schedule.
func DefaultTargetDate(now time.Time) time.Time {
	location, err := time.LoadLocation(DefaultTimezone)
	if err != nil {
		return now.UTC().AddDate(0, 0, -1)
	}

	return scheduledThrough(now, location, 21, 0)
}

func validateConfig(cfg Config) error {
	switch cfg.Command {
	case "fetch", "sync", "serve", "status", "repair":
	default:
		return fmt.Errorf("unsupported Yahoo command %q", cfg.Command)
	}

	if strings.TrimSpace(cfg.ManifestPath) == "" {
		return errors.New("yahoo constituent manifest path is required")
	}

	if strings.TrimSpace(cfg.DatabasePath) == "" {
		return errors.New("market database path is required")
	}

	if strings.TrimSpace(cfg.BatchDir) == "" {
		return errors.New("yahoo CSV batch directory is required")
	}

	if cfg.From.IsZero() || cfg.Through.IsZero() {
		return errors.New("yahoo date range is required")
	}

	if cfg.Through.Before(cfg.From) {
		return errors.New("to date must not be before from date")
	}

	if cfg.MaxSymbols < 0 {
		return errors.New("max-symbols must not be negative")
	}

	if cfg.MaxDuration < 0 {
		return errors.New("max-duration must not be negative")
	}

	if cfg.SyncTimeout < 0 {
		return errors.New("sync-timeout must not be negative")
	}

	if cfg.Command == "serve" {
		if _, err := time.LoadLocation(cfg.Timezone); err != nil {
			return fmt.Errorf("invalid timezone %q: %w", cfg.Timezone, err)
		}
	}

	return nil
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
