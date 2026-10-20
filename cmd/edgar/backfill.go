package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"

	"wingman.com/fetch-ecb/internal/edgar"
	"wingman.com/fetch-ecb/internal/eventbus"
	"wingman.com/fetch-ecb/internal/pollingworker"
)

type backfillConfig struct {
	setName  string
	fromYear int
	natsURL  string
}

// parseBackfillConfig parses the command-line arguments and environment variables
func parseBackfillConfig(args []string) (appConfig, error) {
	fromYear, err := environmentFromYear(edgar.EarliestPipelineYear)
	if err != nil {
		return appConfig{}, err
	}

	cfg := appConfig{command: "backfill"}
	cfg.backfill = backfillConfig{
		setName:  environmentValue(edgarSetEnv, ""),
		fromYear: fromYear,
		natsURL:  environmentValue(edgarNATSURLEnv, eventbus.DefaultURL),
	}

	flags := flag.NewFlagSet("backfill", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.StringVar(&cfg.backfill.setName, "set", cfg.backfill.setName,
		"constituent set to discover")
	flags.IntVar(&cfg.backfill.fromYear, "from-year", cfg.backfill.fromYear,
		"first filing year to include")
	flags.StringVar(&cfg.backfill.natsURL, "nats-url", cfg.backfill.natsURL,
		"NATS server URL")
	flags.Usage = func() {
		printSubcommandHelp("backfill",
			"Index, discover, download, and process a finite historical scope.",
			[]helpOption{
				{"--set <name>", "Required set from the configured data directory."},
				{"--from-year <year>", "First year; continues through current data."},
				{"--nats-url <url>", "NATS server; default nats://127.0.0.1:4222."},
			})
	}

	if err := flags.Parse(args); err != nil {
		return appConfig{}, flag.ErrHelp
	}

	if flags.NArg() != 0 {
		return subcommandError(flags, errors.New("unexpected positional argument"))
	}

	if cfg.backfill.setName == "" {
		return subcommandError(flags, errors.New("--set is required"))
	}

	if cfg.backfill.fromYear < edgar.EarliestPipelineYear {
		return subcommandError(flags, fmt.Errorf("--from-year must be %d or later",
			edgar.EarliestPipelineYear))
	}

	filter := edgar.IndexFilter{}
	if err := applyConstituentSet(&filter, cfg.backfill.setName); err != nil {
		return subcommandError(flags, err)
	}

	return cfg, nil
}

func runBackfill(ctx context.Context, client *http.Client,
	cfg backfillConfig, logger *slog.Logger,
) error {
	logger.Info("starting backfill", "set", cfg.setName,
		"from_year", cfg.fromYear, "through", "current available data")

	indexCfg, err := parseDownloadIndexConfig([]string{
		"--from-year", strconv.Itoa(cfg.fromYear),
	})
	if err != nil {
		return err
	}

	if err := edgar.DownloadIndex(ctx, client, indexCfg.index); err != nil {
		return fmt.Errorf("index stage: %w", err)
	}

	discoverCfg, err := parseEventCommandConfig("discover", []string{
		"--set", cfg.setName,
		"--from-year", strconv.Itoa(cfg.fromYear),
		"--nats-url", cfg.natsURL,
	})
	if err != nil {
		return err
	}

	if err := runEventCommand(ctx, "discover", discoverCfg.events,
		logger); err != nil {
		return fmt.Errorf("discover stage: %w", err)
	}

	for _, stage := range []string{"download", "process"} {
		stageCfg, err := parseEventCommandConfig(stage, []string{
			"--drain", "--nats-url", cfg.natsURL,
		})
		if err != nil {
			return err
		}

		if err := runEventCommand(ctx, stage, stageCfg.events,
			logger); err != nil {
			return fmt.Errorf("%s stage: %w", stage, err)
		}
	}

	store, err := pollingworker.NewDedupeServiceContext(ctx,
		discoverCfg.events.pollingDB)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	failures, err := store.WorkFailures(ctx, "")
	if err != nil {
		return err
	}

	if len(failures) > 0 {
		return fmt.Errorf("backfill completed with %d unresolved failure(s); "+
			"run make errors", len(failures))
	}

	logger.Info("backfill complete", "set", cfg.setName,
		"from_year", cfg.fromYear)

	return nil
}
