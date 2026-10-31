package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"wingman.com/chopstick/internal/edgar"
	"wingman.com/chopstick/internal/edgarapp"
	"wingman.com/chopstick/internal/eventbus"
)

type rebuildConfig struct {
	backfill backfillConfig
	confirm  bool
}

func parseRebuildConfig(args []string) (appConfig, error) {
	if len(args) == 0 || args[0] != "edgar" {
		fmt.Fprintln(os.Stdout,
			"Usage: edgar rebuild edgar [options]")

		return appConfig{}, flag.ErrHelp
	}

	fromYear, err := environmentFromYear()
	if err != nil {
		return appConfig{}, err
	}

	config := appConfig{command: "rebuild"}
	config.rebuild.backfill = backfillConfig{
		setName:  environmentValue(edgarSetEnv, ""),
		fromYear: fromYear,
		natsURL:  environmentValue(edgarNATSURLEnv, eventbus.DefaultURL),
	}

	flags := flag.NewFlagSet("rebuild edgar", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.StringVar(&config.rebuild.backfill.setName, "set",
		config.rebuild.backfill.setName, "constituent set to rebuild")
	flags.IntVar(&config.rebuild.backfill.fromYear, "from-year",
		config.rebuild.backfill.fromYear, "first filing year to include")
	flags.StringVar(&config.rebuild.backfill.natsURL, "nats-url",
		config.rebuild.backfill.natsURL, "NATS server URL")
	flags.BoolVar(&config.rebuild.confirm, "confirm", false,
		"confirm removal of rebuildable EDGAR state")
	flags.Usage = func() {
		printSubcommandHelp("rebuild edgar",
			"Rebuild EDGAR state while preserving retained inputs.",
			[]helpOption{
				{"--set <name>", "Required constituent set."},
				{"--from-year <year>", "First filing year to restore."},
				{"--nats-url <url>", "NATS server URL."},
				{"--confirm", "Required destructive-operation confirmation."},
			})
	}

	if err := flags.Parse(args[1:]); err != nil {
		return appConfig{}, flag.ErrHelp
	}

	if flags.NArg() != 0 {
		return subcommandError(flags,
			errors.New("unexpected positional argument"))
	}

	if !config.rebuild.confirm {
		return subcommandError(flags, errors.New("--confirm is required"))
	}

	if config.rebuild.backfill.setName == "" {
		return subcommandError(flags, errors.New("--set is required"))
	}

	if config.rebuild.backfill.fromYear < edgar.EarliestPipelineYear {
		return subcommandError(flags, fmt.Errorf(
			"--from-year must be %d or later", edgar.EarliestPipelineYear))
	}

	filter := edgar.IndexFilter{}
	if err := applyConstituentSet(&filter,
		config.rebuild.backfill.setName); err != nil {
		return subcommandError(flags, err)
	}

	return config, nil
}

func runRebuild(ctx context.Context, config rebuildConfig,
	logger *slog.Logger,
) error {
	applicationConfig, err := resolveBackfillConfig(config.backfill)
	if err != nil {
		return err
	}

	return edgarapp.Rebuild(ctx, applicationConfig, logger)
}
