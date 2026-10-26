package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"wingman.com/fetch-ecb/internal/ctxlog"
	"wingman.com/fetch-ecb/internal/marketstore"
	"wingman.com/fetch-ecb/internal/marketworker"
)

// workReader and workWriter keep the downloader independent of SQLite. A
// different storage backend can implement the same work and observation
// contract without changing the Yahoo client.
type workReader interface {
	PendingWork(
		ctx context.Context,
		provider string,
		securities []marketstore.Security,
		bootstrapFrom time.Time,
		through time.Time,
	) ([]marketstore.WorkItem, error)
}

type workWriter interface {
	Claim(ctx context.Context, item marketstore.WorkItem, now time.Time, leaseUntil time.Time) (bool, error)
	Save(ctx context.Context, item marketstore.WorkItem, prices []marketstore.Price, actions []marketstore.CorporateAction, now time.Time) (bool, error)
	Fail(ctx context.Context, item marketstore.WorkItem, workErr error, now time.Time) error
}

type repairWriter interface {
	QueueRepair(ctx context.Context, provider string, security marketstore.Security, from time.Time, through time.Time, now time.Time) error
}

type workRepository interface {
	workReader
	workWriter
}

func executeFetch(
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
	runner := marketworker.Runner{
		Repository: store,
		Fetcher:    yahooFetcher{client: client},
		Batches:    batches,
		Now:        time.Now,
	}

	summary, err := runner.RunBatch(ctx, marketWorkerConfig(cfg, securities))
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

type yahooFetcher struct {
	client *yahooClient
}

func (fetcher yahooFetcher) Fetch(
	ctx context.Context,
	ticker string,
	from time.Time,
	through time.Time,
) (marketworker.FetchResult, error) {
	result, err := fetcher.client.fetch(ctx, ticker, from, through)
	if err != nil {
		return marketworker.FetchResult{}, err
	}

	return marketworker.FetchResult{
		Prices:  marketPrices(result.Prices),
		Actions: marketActions(result.Actions),
	}, nil
}

func marketWorkerConfig(
	cfg config,
	securities []marketstore.Security,
) marketworker.Config {
	return marketworker.Config{
		Provider:      providerYahoo,
		Securities:    securities,
		BootstrapFrom: cfg.from,
		Through:       cfg.to,
		MaxItems:      cfg.maxSymbols,
		MaxDuration:   cfg.maxDuration,
	}
}

func reportStatus(ctx context.Context, output io.Writer, store workReader, securities []marketstore.Security, cfg config, now time.Time) error {
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

func queueRepairs(ctx context.Context, output io.Writer, store repairWriter, securities []marketstore.Security, cfg config, now time.Time) error {
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

func marketPrices(prices []dailyPrice) []marketstore.Price {
	result := make([]marketstore.Price, 0, len(prices))
	for _, price := range prices {
		result = append(result, marketstore.Price{
			Date:          price.Date,
			Open:          price.Open,
			Close:         price.Close,
			AdjustedClose: price.AdjClose,
		})
	}

	return result
}

func marketActions(actions []corporateAction) []marketstore.CorporateAction {
	result := make([]marketstore.CorporateAction, 0, len(actions))
	for _, action := range actions {
		result = append(result, marketstore.CorporateAction{
			SourceID:    action.SourceID,
			Date:        action.Date,
			Type:        action.Type,
			Amount:      action.Amount,
			Numerator:   action.Numerator,
			Denominator: action.Denominator,
		})
	}

	return result
}
