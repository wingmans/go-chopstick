// Package marketworker runs bounded and continuous market-data synchronization.
package marketworker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"wingman.com/fetch-ecb/internal/ctxlog"
	"wingman.com/fetch-ecb/internal/marketstore"
)

const defaultLeaseDuration = 15 * time.Minute

// Repository stores work state and downloaded observations.
type Repository interface {
	PendingWork(
		ctx context.Context,
		provider string,
		securities []marketstore.Security,
		bootstrapFrom time.Time,
		through time.Time,
	) ([]marketstore.WorkItem, error)
	Claim(
		ctx context.Context,
		item marketstore.WorkItem,
		now time.Time,
		leaseUntil time.Time,
	) (bool, error)
	Save(
		ctx context.Context,
		item marketstore.WorkItem,
		prices []marketstore.Price,
		actions []marketstore.CorporateAction,
		now time.Time,
	) (bool, error)
	Fail(
		ctx context.Context,
		item marketstore.WorkItem,
		workErr error,
		now time.Time,
	) error
}

// FetchResult is one normalized provider response.
type FetchResult struct {
	Prices  []marketstore.Price
	Actions []marketstore.CorporateAction
}

// Fetcher retrieves one provider work range.
type Fetcher interface {
	Fetch(
		ctx context.Context,
		ticker string,
		from time.Time,
		through time.Time,
	) (FetchResult, error)
}

// Recovery contains the usable prefix reconstructed from retained batches.
type Recovery struct {
	From    time.Time
	Through time.Time
	Prices  []marketstore.Price
	Actions []marketstore.CorporateAction
	Paths   []string
}

// BatchStore stages normalized observations and can recover a lost database.
type BatchStore interface {
	Write(
		ctx context.Context,
		item marketstore.WorkItem,
		prices []marketstore.Price,
		actions []marketstore.CorporateAction,
	) (string, error)
	Read(
		ctx context.Context,
		item marketstore.WorkItem,
		path string,
	) ([]marketstore.Price, []marketstore.CorporateAction, error)
	Recover(
		ctx context.Context,
		item marketstore.WorkItem,
	) (Recovery, error)
}

// Config describes one desired synchronization boundary.
type Config struct {
	Provider      string
	Securities    []marketstore.Security
	BootstrapFrom time.Time
	Through       time.Time
	MaxItems      int
	MaxDuration   time.Duration
	LeaseDuration time.Duration
}

// Summary reports the work performed by one bounded run.
type Summary struct {
	Pending      int
	Attempted    int
	Completed    int
	Failed       int
	Recovered    int
	Prices       int
	Actions      int
	Deferred     int
	NextEligible time.Time
}

// Runner coordinates provider fetches, CSV batches, and durable work state.
type Runner struct {
	Repository Repository
	Fetcher    Fetcher
	Batches    BatchStore
	Now        func() time.Time
}

// RunBatch performs one bounded pass over the currently pending work.
func (runner Runner) RunBatch(
	ctx context.Context,
	cfg Config,
) (Summary, error) {
	if err := runner.validate(cfg); err != nil {
		return Summary{}, err
	}

	startedAt := runner.now()

	items, err := runner.Repository.PendingWork(
		ctx,
		cfg.Provider,
		cfg.Securities,
		cfg.BootstrapFrom,
		cfg.Through,
	)
	if err != nil {
		return Summary{}, err
	}

	summary := Summary{Pending: len(items)}
	leaseDuration := cfg.LeaseDuration

	if leaseDuration == 0 {
		leaseDuration = defaultLeaseDuration
	}

	for _, item := range items {
		now := runner.now()
		if limitReached(summary.Attempted, cfg.MaxItems) ||
			durationReached(startedAt, now, cfg.MaxDuration) {
			break
		}

		if item.EligibleAt.After(now) || item.LeaseUntil.After(now) {
			summary.Deferred++
			summary.NextEligible = earlier(
				summary.NextEligible,
				later(item.EligibleAt, item.LeaseUntil),
			)

			continue
		}

		claimed, claimErr := runner.Repository.Claim(
			ctx,
			item,
			now,
			now.Add(leaseDuration),
		)
		if claimErr != nil {
			return summary, claimErr
		}

		if !claimed {
			continue
		}

		if item.Kind == marketstore.WorkBootstrap {
			recovered, recoveredAny, recoveryErr := runner.recover(
				ctx, item, now,
			)
			if recoveryErr != nil {
				ctxlog.FromContext(ctx).Warn(
					"retained Yahoo CSV batches could not be recovered",
					"ticker", item.Security.Symbol,
					"error", recoveryErr,
				)
			} else if recoveredAny {
				summary.Completed++
				summary.Recovered++
				summary.Prices += len(recovered.Prices)
				summary.Actions += len(recovered.Actions)

				ctxlog.FromContext(ctx).Info(
					"recovered market data from retained CSV batches",
					"ticker", item.Security.Symbol,
					"through", recovered.Through.Format(time.DateOnly),
					"batch_files", len(recovered.Paths),
					"observations", len(recovered.Prices),
					"corporate_actions", len(recovered.Actions),
				)

				continue
			}
		}

		summary.Attempted++

		result, fetchErr := runner.fetch(ctx, item)
		if fetchErr != nil {
			summary.Failed++
			if err := runner.fail(ctx, item, fetchErr); err != nil {
				return summary, errors.Join(fetchErr, err)
			}

			continue
		}

		path, batchErr := runner.Batches.Write(
			ctx,
			item,
			result.Prices,
			result.Actions,
		)
		if batchErr == nil {
			result.Prices, result.Actions, batchErr = runner.Batches.Read(
				ctx,
				item,
				path,
			)
		}

		if batchErr != nil {
			summary.Failed++
			if err := runner.fail(ctx, item, batchErr); err != nil {
				return summary, errors.Join(batchErr, err)
			}

			continue
		}

		actionsChanged, saveErr := runner.Repository.Save(
			ctx,
			item,
			result.Prices,
			result.Actions,
			runner.now(),
		)
		if saveErr != nil {
			summary.Failed++
			if err := runner.fail(ctx, item, saveErr); err != nil {
				return summary, errors.Join(saveErr, err)
			}

			continue
		}

		summary.Completed++
		summary.Prices += len(result.Prices)
		summary.Actions += len(result.Actions)

		ctxlog.FromContext(ctx).Info(
			"stored Yahoo market data",
			"ticker", item.Security.Symbol,
			"work_type", item.Kind,
			"observations", len(result.Prices),
			"corporate_actions", len(result.Actions),
			"batch_file", path,
			"reconciliation_queued", actionsChanged &&
				item.Kind != marketstore.WorkBootstrap &&
				item.Kind != marketstore.WorkReconcile,
		)
	}

	return summary, nil
}

// Sync runs bounded passes until the requested universe is current.
func (runner Runner) Sync(ctx context.Context, cfg Config) (Summary, error) {
	var total Summary

	for {
		summary, err := runner.RunBatch(ctx, cfg)
		addSummary(&total, summary)

		if err != nil {
			return total, err
		}

		items, err := runner.Repository.PendingWork(
			ctx,
			cfg.Provider,
			cfg.Securities,
			cfg.BootstrapFrom,
			cfg.Through,
		)
		if err != nil {
			return total, err
		}

		total.Pending = len(items)
		if len(items) == 0 {
			return total, nil
		}

		if summary.Attempted > 0 || summary.Recovered > 0 {
			continue
		}

		wakeAt := nextEligible(items, runner.now())
		if err := waitUntil(ctx, wakeAt); err != nil {
			return total, err
		}
	}
}

func (runner Runner) validate(cfg Config) error {
	switch {
	case runner.Repository == nil:
		return errors.New("market work repository is required")
	case runner.Fetcher == nil:
		return errors.New("market fetcher is required")
	case runner.Batches == nil:
		return errors.New("market CSV batch store is required")
	case runner.Now == nil:
		return errors.New("market clock is required")
	case cfg.Provider == "":
		return errors.New("market provider is required")
	case cfg.Through.Before(cfg.BootstrapFrom):
		return errors.New("market target precedes bootstrap start")
	case cfg.MaxItems < 0:
		return errors.New("market item limit must not be negative")
	case cfg.MaxDuration < 0:
		return errors.New("market duration limit must not be negative")
	case cfg.LeaseDuration < 0:
		return errors.New("market lease duration must not be negative")
	default:
		return nil
	}
}

func (runner Runner) now() time.Time {
	return runner.Now().UTC()
}

func (runner Runner) recover(
	ctx context.Context,
	item marketstore.WorkItem,
	now time.Time,
) (Recovery, bool, error) {
	recovery, err := runner.Batches.Recover(ctx, item)
	if err != nil {
		return Recovery{}, false, err
	}

	if recovery.Through.IsZero() {
		return Recovery{}, false, nil
	}

	recoveredItem := item
	recoveredItem.From = recovery.From
	recoveredItem.Through = recovery.Through

	if _, err := runner.Repository.Save(
		ctx,
		recoveredItem,
		recovery.Prices,
		recovery.Actions,
		now,
	); err != nil {
		return Recovery{}, false, fmt.Errorf(
			"restore retained market CSV batches: %w", err,
		)
	}

	return recovery, true, nil
}

func (runner Runner) fetch(
	ctx context.Context,
	item marketstore.WorkItem,
) (FetchResult, error) {
	ctxlog.FromContext(ctx).Info(
		"fetching Yahoo market data",
		"ticker", item.Security.Symbol,
		"work_type", item.Kind,
		"from", item.From.Format(time.DateOnly),
		"to", item.Through.Format(time.DateOnly),
	)

	result, err := runner.Fetcher.Fetch(
		ctx,
		item.Security.Symbol,
		item.From,
		item.Through,
	)
	if err != nil {
		ctxlog.FromContext(ctx).Error(
			"Yahoo market-data work failed",
			"ticker", item.Security.Symbol,
			"work_type", item.Kind,
			"error", err,
		)
	}

	return result, err
}

func (runner Runner) fail(
	ctx context.Context,
	item marketstore.WorkItem,
	workErr error,
) error {
	if err := runner.Repository.Fail(
		ctx,
		item,
		workErr,
		runner.now(),
	); err != nil {
		return err
	}

	ctxlog.FromContext(ctx).Error(
		"Yahoo market-data work remains pending",
		"ticker", item.Security.Symbol,
		"work_type", item.Kind,
		"error", workErr,
	)

	return nil
}

func nextEligible(items []marketstore.WorkItem, now time.Time) time.Time {
	wakeAt := time.Time{}

	for _, item := range items {
		eligibleAt := later(item.EligibleAt, item.LeaseUntil)
		if !eligibleAt.After(now) {
			return now
		}

		wakeAt = earlier(wakeAt, eligibleAt)
	}

	return wakeAt
}

func waitUntil(ctx context.Context, wakeAt time.Time) error {
	delay := time.Until(wakeAt)
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func addSummary(total *Summary, next Summary) {
	total.Attempted += next.Attempted
	total.Completed += next.Completed
	total.Failed += next.Failed
	total.Recovered += next.Recovered
	total.Prices += next.Prices
	total.Actions += next.Actions
	total.Deferred = next.Deferred
	total.NextEligible = next.NextEligible
}

func durationReached(startedAt, now time.Time, limit time.Duration) bool {
	return limit > 0 && !now.Before(startedAt.Add(limit))
}

func limitReached(attempted, limit int) bool {
	return limit > 0 && attempted >= limit
}

func earlier(left, right time.Time) time.Time {
	if left.IsZero() || (!right.IsZero() && right.Before(left)) {
		return right
	}

	return left
}

func later(left, right time.Time) time.Time {
	if right.After(left) {
		return right
	}

	return left
}
