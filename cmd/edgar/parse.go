package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"time"

	"wingman.com/fetch-ecb/internal/ctxlog"
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
			{flags: noopHelpFlags, description: "Parse and validate without writing results."},
			{flags: "-h, --help", description: "Show command help."},
		})
	}
	flags.Var(&cfg.parse.files, "f", "local submission file")
	flags.Var(&cfg.parse.files, "file", "local submission file")
	flags.BoolVar(&cfg.parse.noop, "n", false, "parse without writing")
	flags.BoolVar(&cfg.parse.noop, "noop", false, "parse without writing")

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
	for _, path := range cfg.parse.files {
		if err := parseLocalFile(ctx, path, cfg.parse.noop); err != nil {
			return err
		}
	}

	return nil
}

func parseLocalFile(ctx context.Context, path string, noop bool) error {
	started := time.Now()

	filing, err := edgar.ParseSubmission(ctx, path)
	if err != nil {
		return err
	}

	logger := ctxlog.FromContext(ctx)
	logger.Debug("read EDGAR submission", "source", "local", "path", path,
		"filename", filepath.Base(path), "duration_ms", time.Since(started).Milliseconds())

	for _, diagnostic := range filing.Diagnostics {
		logger.Warn("submission diagnostic", "code", diagnostic.Code, "document", diagnostic.Document, "detail", diagnostic.Message)
	}

	destination, err := filing.ParsedPath(edgar.DefaultParsedDirectory)
	if err != nil {
		return err
	}

	if !noop {
		if err := ctx.Err(); err != nil {
			return err
		}

		destination, err = edgar.SaveParsedFiling(edgar.DefaultParsedDirectory, filing)
		if err != nil {
			return err
		}
	}

	logger.Info("parsed EDGAR submission", "accession", filing.AccessionNumber(), "form_type", filing.FormType(),
		"status", filing.Status, "documents", len(filing.Documents), "instances", len(filing.Instances),
		"path", destination, "noop", noop, "duration_ms", time.Since(started).Milliseconds())

	return nil
}
