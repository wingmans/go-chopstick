package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"

	"wingman.com/chopstick/internal/appdata"
	"wingman.com/chopstick/internal/eventbus"
	"wingman.com/chopstick/internal/indeximport"
)

type indexImportConfig struct {
	value indeximport.Config
}

func parseIndexImportConfig(args []string) (appConfig, error) {
	config := appConfig{command: "index-import"}
	config.indexImport.value = indeximport.Config{
		IndexesDirectory: appdata.Path("cache", "indexes", "quarterly"),
		PollingDB:        appdata.Path("polling.db"),
		NATSURL:          environmentValue(edgarNATSURLEnv, eventbus.DefaultURL),
	}

	flags := flag.NewFlagSet("index import", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.StringVar(&config.indexImport.value.IndexesDirectory,
		"indexes-dir", config.indexImport.value.IndexesDirectory,
		"quarterly index directory")
	flags.StringVar(&config.indexImport.value.PollingDB, "polling-db",
		config.indexImport.value.PollingDB, "pipeline state database")
	flags.StringVar(&config.indexImport.value.NATSURL, "nats-url",
		config.indexImport.value.NATSURL, "NATS server URL")
	flags.StringVar(&config.indexImport.value.File, "file", "",
		"import one quarterly TSV file")
	flags.BoolVar(&config.indexImport.value.Latest, "latest", false,
		"import the newest local quarterly TSV")
	flags.IntVar(&config.indexImport.value.Year, "year", 0,
		"import all quarters for one year")
	flags.IntVar(&config.indexImport.value.FromYear, "from-year", 0,
		"first year to import")
	flags.IntVar(&config.indexImport.value.ToYear, "to-year", 0,
		"last year to import")
	flags.BoolVar(&config.indexImport.value.DryRun, "dry-run", false,
		"read and report without writing")
	flags.Usage = func() {
		printSubcommandHelp("index import",
			"Import local quarterly indexes into the event pipeline.",
			[]helpOption{
				{"--file <path>", "Import one quarterly TSV file."},
				{"--latest", "Import the newest local quarterly TSV."},
				{"--year <year>", "Import one year."},
				{"--from-year <year>", "First year to import."},
				{"--to-year <year>", "Last year to import."},
				{"--dry-run", "Read and report without writing."},
			})
	}

	if err := flags.Parse(args); err != nil {
		return appConfig{}, flag.ErrHelp
	}

	if flags.NArg() != 0 {
		return subcommandError(flags,
			errors.New("unexpected positional argument"))
	}

	if err := config.indexImport.value.Validate(); err != nil {
		return subcommandError(flags, err)
	}

	return config, nil
}

func runIndexImport(ctx context.Context, config indexImportConfig,
	logger *slog.Logger,
) error {
	return indeximport.Run(ctx, config.value, logger)
}
