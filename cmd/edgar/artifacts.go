package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"wingman.com/fetch-ecb/internal/appdata"
	"wingman.com/fetch-ecb/internal/artifacts"
	"wingman.com/fetch-ecb/internal/constituents"
	"wingman.com/fetch-ecb/internal/edgar"
)

type artifactCommandConfig struct {
	operation    string
	cik          string
	accession    string
	parsedDir    string
	filingsDir   string
	artifactsDir string
	types        stringList
	filenames    stringList
	all          bool
}

func parseArtifactCommand(args []string) (appConfig, error) {
	cfg := appConfig{command: "artifacts"}
	cfg.artifacts = artifactCommandConfig{
		parsedDir:    appdata.Path("parsed"),
		filingsDir:   appdata.Path("cache", "filings"),
		artifactsDir: appdata.Path("tmp", "artifacts"),
	}
	flags := flag.NewFlagSet("artifacts", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.Usage = func() {
		printSubcommandHelp("artifacts <list|extract|clean>",
			"Inspect disposable documents from one parsed SEC filing.",
			[]helpOption{
				{"--cik <cik>", "Required filing CIK."},
				{"--accession <number>", "Required accession number."},
				{"--type <type>", "Extract a document type; repeatable."},
				{"--document <name>", "Extract one filename; repeatable."},
				{"--all", "Extract every document."},
				{"--artifacts-dir <path>", "Disposable artifact root."},
				{"--parsed-dir <path>", "Parsed filing directory."},
				{"--filings-dir <path>", "Raw filing cache directory."},
			})
	}
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		flags.Usage()

		return appConfig{}, flag.ErrHelp
	}
	if len(args) == 0 {
		return subcommandError(flags,
			errors.New("use 'edgar artifacts <list|extract|clean>'"))
	}
	cfg.artifacts.operation = args[0]
	if cfg.artifacts.operation != "list" &&
		cfg.artifacts.operation != "extract" &&
		cfg.artifacts.operation != "clean" {
		return subcommandError(flags,
			errors.New("use 'edgar artifacts <list|extract|clean>'"))
	}

	flags.StringVar(&cfg.artifacts.cik, "cik", "", "filing CIK")
	flags.StringVar(&cfg.artifacts.accession, "accession", "",
		"filing accession number")
	flags.StringVar(&cfg.artifacts.parsedDir, "parsed-dir",
		cfg.artifacts.parsedDir, "parsed filing directory")
	flags.StringVar(&cfg.artifacts.filingsDir, "filings-dir",
		cfg.artifacts.filingsDir, "raw filing cache directory")
	flags.StringVar(&cfg.artifacts.artifactsDir, "artifacts-dir",
		cfg.artifacts.artifactsDir, "disposable artifact root")
	flags.Var(&cfg.artifacts.types, "type", "document type to extract")
	flags.Var(&cfg.artifacts.filenames, "document",
		"document filename to extract")
	flags.BoolVar(&cfg.artifacts.all, "all", false, "extract every document")
	if err := flags.Parse(args[1:]); err != nil {
		return appConfig{}, flag.ErrHelp
	}
	if flags.NArg() != 0 {
		return subcommandError(flags, errors.New("unexpected positional argument"))
	}

	normalizedCIK, err := constituents.NormalizeCIK(cfg.artifacts.cik)
	if err != nil {
		return subcommandError(flags, fmt.Errorf("--cik: %w", err))
	}
	cfg.artifacts.cik = normalizedCIK
	cfg.artifacts.accession = strings.TrimSpace(cfg.artifacts.accession)
	if cfg.artifacts.accession == "" ||
		filepath.Base(cfg.artifacts.accession) != cfg.artifacts.accession {
		return subcommandError(flags,
			errors.New("--accession must be a safe accession number"))
	}
	if cfg.artifacts.operation == "extract" && !cfg.artifacts.all &&
		len(cfg.artifacts.types) == 0 && len(cfg.artifacts.filenames) == 0 {
		return subcommandError(flags, errors.New(
			"extract requires --type, --document, or --all"))
	}

	return cfg, nil
}

func runArtifacts(ctx context.Context, cfg artifactCommandConfig) error {
	destination := filepath.Join(cfg.artifactsDir, cfg.cik, cfg.accession)
	if cfg.operation == "clean" {
		if err := artifacts.Clean(destination, cfg.cik, cfg.accession); err != nil {
			return err
		}
		fmt.Printf("removed %s\n", destination)

		return nil
	}

	parsedPath := filepath.Join(cfg.parsedDir, cfg.cik, cfg.accession,
		"filing.json")
	filing, err := edgar.LoadParsedFiling(parsedPath)
	if err != nil {
		return err
	}
	if cfg.operation == "list" {
		fmt.Println("SEQ\tTYPE\tBYTES\tFILENAME\tDESCRIPTION")
		for _, document := range filing.Documents {
			fmt.Printf("%s\t%s\t%d\t%s\t%s\n", document.Sequence,
				document.Type, document.ContentLength, document.Filename,
				document.Description)
		}

		return nil
	}

	source, err := artifactSourcePath(cfg.filingsDir, filing)
	if err != nil {
		return err
	}
	matches, err := edgar.SourceFingerprintMatches(ctx, source)
	if err != nil {
		return err
	}
	if !matches {
		return errors.New("raw source changed since parsing; parse it again")
	}

	manifest, err := artifacts.Extract(ctx, filing, source, destination,
		artifacts.Selection{
			All: cfg.all, Types: cfg.types, Filenames: cfg.filenames,
		})
	if err != nil {
		return err
	}
	fmt.Printf("extracted %d documents (%d bytes) to %s\n",
		len(manifest.Documents), manifest.TotalBytes, destination)

	return nil
}

func artifactSourcePath(filingsDir string,
	filing *edgar.ParsedFiling,
) (string, error) {
	switch filing.SourceBase {
	case "absolute":
		return filing.SourcePath, nil
	case "filings":
		path := filepath.Join(filingsDir, filepath.FromSlash(filing.SourcePath))
		relative, err := filepath.Rel(filingsDir, path)
		if err != nil || !filepath.IsLocal(relative) {
			return "", errors.New("parsed source escapes the filings directory")
		}

		return path, nil
	default:
		return "", fmt.Errorf("unsupported parsed source base %q",
			filing.SourceBase)
	}
}
