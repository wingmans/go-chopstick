package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	"wingman.com/fetch-ecb/internal/appdata"
	"wingman.com/fetch-ecb/internal/ctxlog"
	"wingman.com/fetch-ecb/internal/edgar"
	"wingman.com/fetch-ecb/internal/ownership"
	"wingman.com/fetch-ecb/internal/ownershippipeline"
	"wingman.com/fetch-ecb/internal/ownershipstore"
)

type ownershipCommandConfig struct {
	operation    string
	masterPath   string
	databasePath string
	download     edgar.FilingDownloadConfig
	formTypes    stringList
	fromYear     int
	toYear       int
	reprocess    bool
	noop         bool
}

func parseOwnershipConfig(args []string) (appConfig, error) {
	operation := "import"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		operation = args[0]
		args = args[1:]
	}

	if operation != "import" && operation != "status" {
		return ownershipCommandError(operation,
			errors.New("use 'edgar ownership <import|status>'"))
	}

	var cfg appConfig

	cfg.command = "ownership"
	cfg.ownership.operation = operation

	cfg.ownership.databasePath = appdata.Path("ownership.db")
	if operation == "status" {
		return parseOwnershipStatusConfig(cfg, args)
	}

	cfg.ownership.masterPath = appdata.Path("cache", "indexes", "master.tsv")
	cfg.ownership.fromYear = 2022
	cfg.ownership.download = edgar.FilingDownloadConfig{
		Directory: appdata.Path("cache", "filings"),
		UserAgent: environmentValue(edgarUserAgentEnv, defaultUserAgent),
		BaseURL:   environmentValue(edgarBaseURLEnv, ""),
	}
	flags := ownershipFlagSet("ownership import")
	flags.Usage = func() { printOwnershipImportHelp() }
	flags.StringVar(&cfg.ownership.masterPath, "master",
		cfg.ownership.masterPath, "master index path")
	flags.StringVar(&cfg.ownership.databasePath, "database",
		cfg.ownership.databasePath, "ownership database path")
	flags.StringVar(&cfg.ownership.download.Directory, "cache-dir",
		cfg.ownership.download.Directory, "raw filing cache directory")
	flags.Var(&cfg.ownership.formTypes, "f",
		"select this ownership form; may be repeated")
	flags.Var(&cfg.ownership.formTypes, "form-type",
		"select this ownership form; may be repeated")
	flags.IntVar(&cfg.ownership.fromYear, "from-year",
		cfg.ownership.fromYear, "first filing year to include")
	flags.IntVar(&cfg.ownership.toYear, "to-year", 0,
		"last filing year to include")
	flags.BoolVar(&cfg.ownership.reprocess, "r", false,
		"reparse terminal outcomes at the current parser version")
	flags.BoolVar(&cfg.ownership.reprocess, "reprocess", false,
		"reparse terminal outcomes at the current parser version")
	flags.BoolVar(&cfg.ownership.noop, "n", false,
		"show the number of selected accessions without changing files")
	flags.BoolVar(&cfg.ownership.noop, "noop", false,
		"show the number of selected accessions without changing files")

	if err := flags.Parse(args); err != nil {
		return appConfig{}, err
	}

	if flags.NArg() != 0 {
		return subcommandError(flags,
			errors.New("unexpected positional arguments"))
	}

	if cfg.ownership.fromYear < edgar.EarliestPipelineYear {
		return subcommandError(flags, fmt.Errorf(
			"--from-year must be %d or later", edgar.EarliestPipelineYear))
	}

	if cfg.ownership.toYear != 0 &&
		cfg.ownership.toYear < cfg.ownership.fromYear {
		return subcommandError(flags,
			errors.New("--to-year cannot be before --from-year"))
	}

	if strings.TrimSpace(cfg.ownership.masterPath) == "" ||
		strings.TrimSpace(cfg.ownership.databasePath) == "" ||
		strings.TrimSpace(cfg.ownership.download.Directory) == "" {
		return subcommandError(flags,
			errors.New("ownership paths cannot be empty"))
	}

	if !cfg.ownership.noop &&
		strings.TrimSpace(cfg.ownership.download.UserAgent) == "" {
		return subcommandError(flags,
			fmt.Errorf("%s cannot be empty", edgarUserAgentEnv))
	}

	seen := make(map[string]bool)

	validated := make(stringList, 0, len(cfg.ownership.formTypes))
	for _, raw := range cfg.ownership.formTypes {
		form := ownership.NormalizeFormType(raw)
		if !ownership.RecognizesForm(form) {
			return subcommandError(flags,
				fmt.Errorf("unrecognized ownership form %q", raw))
		}

		if !seen[form] {
			seen[form] = true
			validated = append(validated, form)
		}
	}

	cfg.ownership.formTypes = validated
	cfg.ownership.download.Noop = cfg.ownership.noop

	return cfg, nil
}

func parseOwnershipStatusConfig(cfg appConfig,
	args []string,
) (appConfig, error) {
	flags := ownershipFlagSet("ownership status")
	flags.Usage = func() {
		printSubcommandHelp("ownership status",
			"Show durable ownership outcomes and latest completion coverage.",
			[]helpOption{
				{"    --database <path>", "Ownership SQLite database."},
				{"-h, --help", "Show command help."},
			})
	}
	flags.StringVar(&cfg.ownership.databasePath, "database",
		cfg.ownership.databasePath, "ownership database path")

	if err := flags.Parse(args); err != nil {
		return appConfig{}, err
	}

	if flags.NArg() != 0 {
		return subcommandError(flags,
			errors.New("unexpected positional arguments"))
	}

	if strings.TrimSpace(cfg.ownership.databasePath) == "" {
		return subcommandError(flags,
			errors.New("--database cannot be empty"))
	}

	return cfg, nil
}

func ownershipFlagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(os.Stdout)

	return flags
}

func ownershipCommandError(operation string, err error) (appConfig, error) {
	flags := ownershipFlagSet("ownership " + operation)
	flags.Usage = printOwnershipImportHelp

	return subcommandError(flags, err)
}

func printOwnershipImportHelp() {
	printSubcommandHelp("ownership [import]",
		"Import all recognized ownership filings from a local master index.",
		[]helpOption{
			{"    --master <path>", "Local master.tsv path."},
			{"    --database <path>", "Ownership SQLite database."},
			{"    --cache-dir <path>", "Existing raw filing cache directory."},
			{"-f, --form-type <type>", "Select a form type; may be repeated."},
			{"    --from-year <year>", "First filing year; default 2022."},
			{"    --to-year <year>", "Last filing year; default is present."},
			{"-r, --reprocess", "Reparse already terminal accessions."},
			{noopHelpFlags, "Count selected accessions without writing."},
			{"-h, --help", "Show command help."},
		})
}

func runOwnership(ctx context.Context, client *http.Client,
	cfg ownershipCommandConfig,
) error {
	if cfg.operation == "status" {
		return printOwnershipStatus(ctx, cfg.databasePath)
	}

	summary, err := ownershippipeline.Run(ctx, client,
		ownershippipeline.Config{
			MasterPath: cfg.masterPath, DatabasePath: cfg.databasePath,
			Download: cfg.download, FromYear: cfg.fromYear,
			ToYear: cfg.toYear, FormTypes: cfg.formTypes,
			Reprocess: cfg.reprocess, Noop: cfg.noop,
		})
	ctxlog.FromContext(ctx).Info("ownership import summary",
		"selected", summary.Selected, "planned", summary.Planned,
		"processed", summary.Processed, "reused", summary.Reused,
		"unsupported", summary.Unsupported, "failed", summary.Failed)

	return err
}

func printOwnershipStatus(ctx context.Context, databasePath string) error {
	if _, err := os.Stat(databasePath); err != nil {
		return fmt.Errorf("inspect ownership database: %w", err)
	}

	store, err := ownershipstore.Open(ctx, databasePath)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	coverage, err := store.LatestCoverage(ctx, ownership.ParserVersion)
	if err != nil {
		return err
	}

	if len(coverage) == 0 {
		fmt.Fprintln(os.Stdout, "No ownership completion runs recorded.")
	} else {
		for _, row := range coverage {
			fmt.Fprintf(os.Stdout,
				"%-13s %-8s %s through %s: %d processed, "+
					"%d failed, %d unsupported\n",
				ownershipFamilyLabel(row.Family), row.Status,
				row.FromDate, row.ThroughDate, row.Processed,
				row.Failed, row.Unsupported)
		}
	}

	counts, err := store.Counts(ctx, "", "")
	if err != nil {
		return err
	}

	if len(counts) > 0 {
		fmt.Fprintln(os.Stdout, "\nDurable filing outcomes:")

		for _, count := range counts {
			fmt.Fprintf(os.Stdout, "%-13s %-11s %d\n",
				ownershipFamilyLabel(count.Family), count.Status, count.Count)
		}
	}

	return nil
}

func ownershipFamilyLabel(family string) string {
	switch family {
	case ownership.FamilyInstitutional:
		return "13F"
	case ownership.FamilyInsider:
		return "Forms 3/4/5"
	case ownership.FamilyBeneficial:
		return "13D/G"
	default:
		return family
	}
}
