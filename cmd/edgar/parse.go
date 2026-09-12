package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"

	"wingman.com/fetch-ecb/internal/edgar"
)

func parseSubmissionConfig(args []string) (appConfig, error) {
	var cfg appConfig

	cfg.command = parseCommand
	flags := flag.NewFlagSet(parseCommand, flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.Usage = func() {
		printSubcommandHelp(parseCommand, "Read local .txt submissions and store results in data/parsed.", []helpOption{
			{flags: "-f, --file <path>", description: "Local submission file; required and may be repeated."},
			{flags: "-r, --reprocess", description: "Rebuild parsed results even when they are current."},
			{flags: noopHelpFlags, description: "Show processing decisions without changing files."},
			{flags: "-h, --help", description: "Show command help."},
		})
	}
	flags.Var(&cfg.parse.files, "f", "local submission file")
	flags.Var(&cfg.parse.files, "file", "local submission file")
	flags.BoolVar(&cfg.parse.noop, "n", false, "parse without writing")
	flags.BoolVar(&cfg.parse.noop, "noop", false, "parse without writing")
	flags.BoolVar(&cfg.parse.reprocess, "r", false, "rebuild parsed results")
	flags.BoolVar(&cfg.parse.reprocess, "reprocess", false, "rebuild parsed results")

	if err := flags.Parse(args); err != nil {
		return appConfig{}, flag.ErrHelp
	}

	if flags.NArg() != 0 {
		return subcommandError(flags, errors.New("unexpected positional arguments; use --file for each submission"))
	}

	if len(cfg.parse.files) == 0 {
		return subcommandError(flags, errors.New("at least one --file is required"))
	}

	for _, path := range cfg.parse.files {
		if filepath.Ext(path) != ".txt" {
			return subcommandError(flags, errors.New("--file must name a .txt submission"))
		}
	}

	return cfg, nil
}

func runParse(ctx context.Context, cfg appConfig) error {
	_, err := edgar.ProcessLocalSubmissions(ctx, cfg.parse.files, edgar.ProcessingConfig{
		Directory:        edgar.DefaultParsedDirectory,
		FilingsDirectory: edgar.DefaultFilingsDirectory,
		Reprocess:        cfg.parse.reprocess,
		Noop:             cfg.parse.noop,
	})

	return err
}
