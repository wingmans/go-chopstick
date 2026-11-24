// Package market downloads Yahoo Finance prices and corporate actions into the
// local market-data store.
package market

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	sourceyahoo "wingman.com/chopstick/internal/ingest/yahoo"
	marketstore "wingman.com/chopstick/internal/market/store"
	"wingman.com/chopstick/internal/platform/ctxlog"
	"wingman.com/chopstick/internal/platform/outbound"
)

const (
	DefaultManifest    = "golden/us-gaap.json"
	DateLayout         = "2006-01-02"
	Provider           = "yahoo"
	DefaultMaxSymbols  = 25
	DefaultSyncTimeout = 2 * time.Hour

	dateLayout       = DateLayout
	marketTimezone   = "America/New_York"
	marketCutoffHour = 21
	providerYahoo    = Provider
)

// ErrSyncIncomplete indicates that a bounded synchronization left work pending.
var ErrSyncIncomplete = errors.New("yahoo synchronization is incomplete")

// Config describes one finite Yahoo market-data operation.
type Config struct {
	Command      string
	ManifestPath string
	DatabasePath string
	BatchDir     string
	From         time.Time
	Through      time.Time
	Tickers      []string
	MaxSymbols   int
	MaxDuration  time.Duration
	SyncTimeout  time.Duration
}

type config struct {
	command      string
	manifestPath string
	databasePath string
	batchDir     string
	from         time.Time
	to           time.Time
	tickers      []string
	maxSymbols   int
	maxDuration  time.Duration
	syncTimeout  time.Duration
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
		syncTimeout: cfg.SyncTimeout,
	}

	store, err := marketstore.Open(ctx, internal.databasePath)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	client := sourceyahoo.NewClient(
		outbound.NewClient(30*time.Second), sourceyahoo.DefaultChartURL, logger,
	)

	batches, err := newCSVBatchStore(internal.batchDir)
	if err != nil {
		return err
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

func executeFetch(
	ctx context.Context,
	output io.Writer,
	logger *slog.Logger,
	store *marketstore.Store,
	batches batchStore,
	client *sourceyahoo.Client,
	securities []marketstore.Security,
	cfg config,
) error {
	ctx = ctxlog.WithLogger(ctx, logger)
	runner := syncRunner{
		Store:   store,
		Fetcher: yahooFetcher{client: client},
		Batches: batches,
		Now:     time.Now,
	}

	summary, err := runner.RunBatch(ctx, syncConfigFor(cfg, securities))
	if err != nil {
		return err
	}

	fmt.Fprintf(
		output,
		"attempted %d Yahoo requests: completed %d, recovered %d, failed %d, "+
			"stored %d prices and %d corporate actions in %s; CSV batches in %s\n",
		summary.Attempted,
		summary.Completed,
		summary.Recovered,
		summary.Failed,
		summary.Prices,
		summary.Actions,
		cfg.databasePath,
		cfg.batchDir,
	)

	if summary.Failed > 0 {
		return fmt.Errorf(
			"%d Yahoo work items failed and remain pending", summary.Failed,
		)
	}

	return nil
}

func executeSync(
	ctx context.Context,
	output io.Writer,
	logger *slog.Logger,
	store *marketstore.Store,
	batches batchStore,
	client *sourceyahoo.Client,
	securities []marketstore.Security,
	cfg config,
) error {
	ctx = ctxlog.WithLogger(ctx, logger)

	if cfg.syncTimeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, cfg.syncTimeout)
		defer cancel()
	}

	runner := syncRunner{
		Store:   store,
		Fetcher: yahooFetcher{client: client},
		Batches: batches,
		Now:     time.Now,
	}

	summary, err := runner.Sync(ctx, syncConfigFor(cfg, securities))
	writeSyncSummary(output, summary, cfg)

	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf(
			"%w: %d symbols still require work",
			ErrSyncIncomplete,
			summary.Pending,
		)
	}

	return err
}

func reportStatus(
	ctx context.Context,
	output io.Writer,
	store *marketstore.Store,
	securities []marketstore.Security,
	cfg config,
	now time.Time,
) error {
	items, err := store.PendingWork(
		ctx, providerYahoo, securities, cfg.from, cfg.to,
	)
	if err != nil {
		return err
	}

	for _, item := range items {
		state := "ready"

		switch {
		case item.LeaseUntil.After(now):
			state = "leased until " + item.LeaseUntil.Format(time.RFC3339)
		case item.EligibleAt.After(now):
			state = "retry after " + item.EligibleAt.Format(time.RFC3339)
		}

		fmt.Fprintf(
			output, "%s %s %s through %s: %s",
			item.Security.Symbol, item.Kind, item.From.Format(dateLayout),
			item.Through.Format(dateLayout), state,
		)

		if item.LastError != "" {
			fmt.Fprintf(output, " (%s)", item.LastError)
		}

		fmt.Fprintln(output)
	}

	fmt.Fprintf(
		output, "%d of %d selected symbols require work\n",
		len(items), len(securities),
	)

	return nil
}

func queueRepairs(
	ctx context.Context,
	output io.Writer,
	store *marketstore.Store,
	securities []marketstore.Security,
	cfg config,
	now time.Time,
) error {
	for _, security := range securities {
		if err := store.QueueRepair(
			ctx, providerYahoo, security, cfg.from, cfg.to, now,
		); err != nil {
			return err
		}

		fmt.Fprintf(
			output, "queued %s repair from %s through %s\n",
			security.Symbol, cfg.from.Format(dateLayout), cfg.to.Format(dateLayout),
		)
	}

	return nil
}

func syncConfigFor(
	cfg config,
	securities []marketstore.Security,
) syncConfig {
	return syncConfig{
		Provider:      providerYahoo,
		Securities:    securities,
		BootstrapFrom: cfg.from,
		Through:       cfg.to,
		MaxItems:      cfg.maxSymbols,
		MaxDuration:   cfg.maxDuration,
	}
}

func loadSecurities(cfg config) ([]marketstore.Security, error) {
	manifest, err := readManifest(cfg.manifestPath)
	if err != nil {
		return nil, err
	}

	members, err := selectMembers(manifest.Members, cfg.tickers)
	if err != nil {
		return nil, err
	}

	return marketSecurities(members), nil
}

func writeSyncSummary(
	output io.Writer,
	summary syncSummary,
	cfg config,
) {
	fmt.Fprintf(
		output,
		"synchronization attempted %d Yahoo requests: completed %d, "+
			"recovered %d, failed %d, stored %d prices and %d corporate "+
			"actions; %d symbols remain through %s\n",
		summary.Attempted,
		summary.Completed,
		summary.Recovered,
		summary.Failed,
		summary.Prices,
		summary.Actions,
		summary.Pending,
		cfg.to.Format(dateLayout),
	)
}

// DefaultTargetDate returns the latest complete market date for the default
// New York cutoff.
func DefaultTargetDate(now time.Time) time.Time {
	location, err := time.LoadLocation(marketTimezone)
	if err != nil {
		return now.UTC().AddDate(0, 0, -1)
	}

	localNow := now.In(location)
	if localNow.Hour() < marketCutoffHour {
		localNow = localNow.AddDate(0, 0, -1)
	}

	return time.Date(
		localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0,
		time.UTC,
	)
}

func validateConfig(cfg Config) error {
	switch cfg.Command {
	case "fetch", "sync", "status", "repair":
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

	return nil
}

func marketSecurities(members []member) []marketstore.Security {
	securities := make([]marketstore.Security, 0, len(members))
	for _, item := range members {
		securities = append(securities, marketstore.Security{
			CIK: item.CIK, Symbol: item.Ticker, Name: item.Name,
		})
	}

	return securities
}
