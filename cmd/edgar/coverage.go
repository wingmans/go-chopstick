package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"time"

	"wingman.com/fetch-ecb/internal/coverage"
	"wingman.com/fetch-ecb/internal/ctxlog"
	"wingman.com/fetch-ecb/internal/edgar"
	"wingman.com/fetch-ecb/internal/filingview"
)

func parseCoverageConfig(args []string) (appConfig, error) {
	var cfg appConfig

	cfg.command = "coverage"

	cfg.coverage.masterPath = "./data/indexes/master.tsv"
	cfg.coverage.indexesDir = "./data/indexes/quarterly"
	cfg.coverage.filingsDir = edgar.DefaultFilingsDirectory
	cfg.coverage.parsedDir = edgar.DefaultParsedDirectory
	cfg.coverage.out = "./data/validation/coverage-report.json"

	flags := flag.NewFlagSet("coverage", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.Usage = func() {
		printSubcommandHelp("coverage", "Report expected local filing coverage.", []helpOption{
			{"-s, --set <name>", "Select current members from data/sets/<name>.json."},
			{"-c, --cik <cik>", "Select filings for this CIK."},
			{"-f, --form-type <type>", "Select this form type; may be repeated."},
			{"    --from-year <year>", "First filing year to include."},
			{"    --to-year <year>", "Last filing year to include."},
			{"-m, --master <path>", "Master TSV; default ./data/indexes/master.tsv."},
			{"    --indexes-dir <path>", "Quarterly indexes directory."},
			{"    --filings-dir <path>", "Downloaded filings directory."},
			{"-d, --parsed-dir <path>", "Parsed filing directory."},
			{"-o, --out <path>", "JSON report path."},
			{"-h, --help", "Show command help."},
		})
	}
	flags.StringVar(&cfg.coverage.setName, "s", "", "select current members from a constituent set")
	flags.StringVar(&cfg.coverage.setName, "set", "", "select current members from a constituent set")
	flags.StringVar(&cfg.coverage.filter.CIK, "c", "", "select filings for this CIK")
	flags.StringVar(&cfg.coverage.filter.CIK, "cik", "", "select filings for this CIK")
	flags.Var(&cfg.coverage.formTypes, "f", "select this form type; may be repeated")
	flags.Var(&cfg.coverage.formTypes, "form-type", "select this form type; may be repeated")
	flags.IntVar(&cfg.coverage.fromYear, "from-year", 0, "first filing year to include")
	flags.IntVar(&cfg.coverage.toYear, "to-year", 0, "last filing year to include")
	flags.StringVar(&cfg.coverage.masterPath, "m", cfg.coverage.masterPath, "master TSV path")
	flags.StringVar(&cfg.coverage.masterPath, "master", cfg.coverage.masterPath, "master TSV path")
	flags.StringVar(&cfg.coverage.indexesDir, "indexes-dir", cfg.coverage.indexesDir, "quarterly indexes directory")
	flags.StringVar(&cfg.coverage.filingsDir, "filings-dir", cfg.coverage.filingsDir, "downloaded filings directory")
	flags.StringVar(&cfg.coverage.parsedDir, "d", cfg.coverage.parsedDir, "parsed filing directory")
	flags.StringVar(&cfg.coverage.parsedDir, "parsed-dir", cfg.coverage.parsedDir, "parsed filing directory")
	flags.StringVar(&cfg.coverage.out, "o", cfg.coverage.out, "JSON report path")
	flags.StringVar(&cfg.coverage.out, "out", cfg.coverage.out, "JSON report path")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return appConfig{}, err
		}

		return appConfig{}, flag.ErrHelp
	}

	if flags.NArg() != 0 {
		return subcommandError(flags, errors.New("unexpected positional arguments"))
	}

	if cfg.coverage.fromYear != 0 && (cfg.coverage.fromYear < edgar.EarliestYear ||
		cfg.coverage.fromYear > 9999) {
		return subcommandError(flags, errors.New("--from-year must be a valid EDGAR year"))
	}

	if cfg.coverage.toYear != 0 && (cfg.coverage.toYear < edgar.EarliestYear ||
		cfg.coverage.toYear > 9999) {
		return subcommandError(flags, errors.New("--to-year must be a valid EDGAR year"))
	}

	if cfg.coverage.fromYear != 0 && cfg.coverage.toYear != 0 &&
		cfg.coverage.fromYear > cfg.coverage.toYear {
		return subcommandError(flags, errors.New("--from-year must not be after --to-year"))
	}

	if err := applyConstituentSet(&cfg.coverage.filter, cfg.coverage.setName); err != nil {
		return subcommandError(flags, err)
	}

	cfg.coverage.filter.FormTypes = cfg.coverage.formTypes

	return cfg, nil
}

func runCoverageCommand(ctx context.Context, cfg appConfig) error {
	taxonomy, err := filingview.LoadTaxonomy("")
	if err != nil {
		return err
	}

	report, err := coverage.Build(ctx, coverage.Config{
		MasterPath: cfg.coverage.masterPath, IndexesDir: cfg.coverage.indexesDir,
		FilingsDir: cfg.coverage.filingsDir, ParsedDir: cfg.coverage.parsedDir,
		Filter: cfg.coverage.filter, SetName: cfg.coverage.setName,
		FromYear: cfg.coverage.fromYear, ToYear: cfg.coverage.toYear,
		Taxonomy: taxonomy, GeneratedAt: time.Now().UTC(),
	})
	if err != nil {
		return err
	}

	if err := coverage.WriteJSON(cfg.coverage.out, report); err != nil {
		return err
	}

	logger := ctxlog.FromContext(ctx)
	logger.Info("EDGAR coverage report written", "path", cfg.coverage.out,
		"expected", report.Summary.Expected, "downloaded", report.Summary.Downloaded,
		"parsed", report.Summary.Parsed, "missing", report.Summary.Missing,
		"unparsed", report.Summary.Unparsed,
		"missing_metrics", report.Summary.MissingMetrics,
		"acquisition_requests", len(report.Acquisition))

	return nil
}
