package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"wingman.com/fetch-ecb/internal/ctxlog"
	"wingman.com/fetch-ecb/internal/edgar"
	"wingman.com/fetch-ecb/internal/filingworkflow"
)

func parseSubmissionConfig(args []string) (appConfig, error) {
	var cfg appConfig

	year := ""

	cfg.command = parseCommand
	cfg.parse.masterPath = "./data/indexes/master.tsv"
	flags := flag.NewFlagSet(parseCommand, flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.Usage = func() {
		printSubcommandHelp(parseCommand, "Process downloaded local submissions selected from master.tsv.", []helpOption{
			{flags: "-c, --cik <cik>", description: "Select filings for this CIK."},
			{flags: "    --set <name>", description: "Select current members from data/sets/<name>.json."},
			{flags: "-f, --form-type <type>", description: "Select this form type; may be repeated."},
			{flags: "-y, --year <year>", description: "Select filings filed in this year; default is all years."},
			{flags: "    --file <path>", description: "Process this local submission; may be repeated."},
			{flags: "-r, --reprocess", description: "Rebuild parsed results even when they are current."},
			{flags: noopHelpFlags, description: "Show processing decisions without changing files."},
			{flags: "-h, --help", description: "Show command help."},
		})
	}
	flags.StringVar(&cfg.parse.filter.CIK, "c", "", "select filings for this CIK")
	flags.StringVar(&cfg.parse.filter.CIK, "cik", "", "select filings for this CIK")
	flags.StringVar(&cfg.parse.setName, "set", "", "select filings for this constituent set")
	flags.Var(&cfg.parse.formTypes, "f", "select this form type; may be repeated")
	flags.Var(&cfg.parse.formTypes, "form-type", "select this form type; may be repeated")
	flags.StringVar(&year, "y", "", "select filings filed in this year")
	flags.StringVar(&year, "year", "", "select filings filed in this year")
	flags.Var(&cfg.parse.files, "file", "local submission file")
	flags.BoolVar(&cfg.parse.noop, "n", false, "parse without writing")
	flags.BoolVar(&cfg.parse.noop, "noop", false, "parse without writing")
	flags.BoolVar(&cfg.parse.reprocess, "r", false, "rebuild parsed results")
	flags.BoolVar(&cfg.parse.reprocess, "reprocess", false, "rebuild parsed results")

	if err := flags.Parse(args); err != nil {
		return appConfig{}, flag.ErrHelp
	}

	if flags.NArg() != 0 {
		return subcommandError(flags, errors.New("unexpected positional arguments; use --file for individual submissions"))
	}

	if len(cfg.parse.files) > 0 && (cfg.parse.filter.CIK != "" ||
		cfg.parse.setName != "" || len(cfg.parse.formTypes) > 0 ||
		strings.TrimSpace(year) != "") {
		return subcommandError(flags, errors.New("--file cannot be combined with --cik, --set, --form-type, or --year"))
	}

	for _, path := range cfg.parse.files {
		if filepath.Ext(path) != ".txt" {
			return subcommandError(flags, errors.New("--file must name a .txt submission"))
		}
	}

	cfg.parse.filter.FormTypes = cfg.parse.formTypes
	if err := applyConstituentSet(&cfg.parse.filter, cfg.parse.setName); err != nil {
		return subcommandError(flags, err)
	}

	if strings.TrimSpace(year) != "" {
		parsedYear, err := strconv.Atoi(strings.TrimSpace(year))
		if err != nil || parsedYear < 1000 || parsedYear > 9999 {
			return subcommandError(flags, errors.New("--year must be a four-digit year"))
		}

		cfg.parse.filter.Year = parsedYear
	}

	return cfg, nil
}

func runParse(ctx context.Context, cfg appConfig) error {
	logger := ctxlog.FromContext(ctx)
	logger.Info("starting EDGAR parse workflow",
		"master", cfg.parse.masterPath,
		"cik", cfg.parse.filter.CIK,
		"set", cfg.parse.setName,
		"set_members", len(cfg.parse.filter.CIKs),
		"form_types", cfg.parse.filter.FormTypes,
		"year", cfg.parse.filter.Year,
		"noop", cfg.parse.noop,
		"reprocess", cfg.parse.reprocess,
	)

	processing := filingworkflow.ProcessingConfig{
		Directory:        edgar.DefaultParsedDirectory,
		FilingsDirectory: edgar.DefaultFilingsDirectory,
		FormType:         "",
		Reprocess:        cfg.parse.reprocess,
		Noop:             cfg.parse.noop,
	}
	if len(cfg.parse.files) > 0 {
		_, err := filingworkflow.ProcessLocalSubmissions(ctx, cfg.parse.files, processing)

		return err
	}

	_, err := filingworkflow.ProcessLocalFilings(ctx, cfg.parse.masterPath, edgar.DefaultFilingsDirectory, processing, cfg.parse.filter)

	return err
}
