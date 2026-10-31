package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"wingman.com/chopstick/internal/ctxlog"
	"wingman.com/chopstick/internal/marketstore"
	"wingman.com/chopstick/internal/marketworker"
)

var errSyncIncomplete = errors.New("yahoo synchronization is incomplete")

func executeSync(
	ctx context.Context,
	output io.Writer,
	logger *slog.Logger,
	store workRepository,
	batches batchStore,
	client *yahooClient,
	securities []marketstore.Security,
	cfg config,
) error {
	ctx = ctxlog.WithLogger(ctx, logger)

	if cfg.syncTimeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, cfg.syncTimeout)
		defer cancel()
	}

	runner := marketworker.Runner{
		Repository: store,
		Fetcher:    yahooFetcher{client: client},
		Batches:    batches,
		Now:        time.Now,
	}

	summary, err := runner.Sync(ctx, marketWorkerConfig(cfg, securities))
	writeSyncSummary(output, summary, cfg)

	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf(
			"%w: %d symbols still require work",
			errSyncIncomplete,
			summary.Pending,
		)
	}

	return err
}

func executeDailyService(
	ctx context.Context,
	output io.Writer,
	logger *slog.Logger,
	store *marketstore.Store,
	batches batchStore,
	client *yahooClient,
	cfg config,
	now func() time.Time,
) error {
	location, err := time.LoadLocation(cfg.timezone)
	if err != nil {
		return fmt.Errorf("load market schedule timezone: %w", err)
	}

	logger.Info(
		"Yahoo daily synchronization service started",
		"schedule", cfg.schedule,
		"timezone", cfg.timezone,
	)

	for {
		cycleNow := now()
		cycleCfg := cfg
		cycleCfg.to = scheduledThrough(
			cycleNow,
			location,
			cfg.scheduleHour,
			cfg.scheduleMinute,
		)

		securities, loadErr := loadSecurities(cfg)
		if loadErr == nil {
			loadErr = store.RegisterSecurities(
				ctx,
				providerYahoo,
				securities,
				cycleNow.UTC(),
			)
		}

		if loadErr == nil && !cycleCfg.to.Before(cycleCfg.from) {
			loadErr = executeSync(
				ctx,
				output,
				logger,
				store,
				batches,
				client,
				securities,
				cycleCfg,
			)
		}

		if loadErr != nil && !errors.Is(loadErr, context.Canceled) {
			logger.Error(
				"Yahoo daily synchronization cycle failed",
				"through", cycleCfg.to.Format(dateLayout),
				"error", loadErr,
			)
		}

		if err := waitForNextRun(
			ctx,
			now,
			location,
			cfg.scheduleHour,
			cfg.scheduleMinute,
		); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}

			return err
		}
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
	summary marketworker.Summary,
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

func scheduledThrough(
	now time.Time,
	location *time.Location,
	hour int,
	minute int,
) time.Time {
	localNow := now.In(location)
	todayRun := time.Date(
		localNow.Year(),
		localNow.Month(),
		localNow.Day(),
		hour,
		minute,
		0,
		0,
		location,
	)

	if localNow.Before(todayRun) {
		localNow = localNow.AddDate(0, 0, -1)
	}

	return time.Date(
		localNow.Year(),
		localNow.Month(),
		localNow.Day(),
		0,
		0,
		0,
		0,
		time.UTC,
	)
}

func nextScheduledRun(
	now time.Time,
	location *time.Location,
	hour int,
	minute int,
) time.Time {
	localNow := now.In(location)

	next := time.Date(
		localNow.Year(),
		localNow.Month(),
		localNow.Day(),
		hour,
		minute,
		0,
		0,
		location,
	)
	if !next.After(localNow) {
		next = time.Date(
			localNow.Year(),
			localNow.Month(),
			localNow.Day()+1,
			hour,
			minute,
			0,
			0,
			location,
		)
	}

	return next
}

func waitForNextRun(
	ctx context.Context,
	now func() time.Time,
	location *time.Location,
	hour int,
	minute int,
) error {
	delay := max(
		nextScheduledRun(now(), location, hour, minute).Sub(now()),
		time.Duration(0),
	)

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
