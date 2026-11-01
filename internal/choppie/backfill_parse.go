package choppie

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"wingman.com/chopstick/internal/edgar"
	"wingman.com/chopstick/internal/eventbus"
)

type backfillConfig struct {
	setName  string
	fromYear int
	natsURL  string
}

// parseBackfillConfig parses the command-line arguments and environment variables.
func parseBackfillConfig(args []string) (appConfig, error) {
	fromYear, err := environmentFromYear()
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
