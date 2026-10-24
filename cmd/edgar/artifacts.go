package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	address      string
	types        stringList
	filenames    stringList
	all          bool
}

func parseArtifactCommand(args []string) (appConfig, error) {
	cfg := appConfig{
		command: "artifacts",
		artifacts: artifactCommandConfig{
			parsedDir:    appdata.Path("parsed"),
			filingsDir:   appdata.Path("cache", "filings"),
			artifactsDir: appdata.Path("tmp", "artifacts"),
			address:      "127.0.0.1:8090",
		},
	}
	flags := flag.NewFlagSet("artifacts", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)

	flags.Usage = func() {
		printSubcommandHelp("artifacts <list|extract|serve|clean>",
			"Inspect disposable documents from one parsed SEC filing.",
			[]helpOption{
				{"--cik <cik>", "Required filing CIK."},
				{"--accession <number>", "Required accession number."},
				{"--type <type>", "Extract a document type; repeatable."},
				{"--document <name>", "Extract one filename; repeatable."},
				{"--all", "Extract every document."},
				{"--address <host:port>", "Viewer address; loopback only."},
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
			errors.New("use 'edgar artifacts <list|extract|serve|clean>'"))
	}

	cfg.artifacts.operation = args[0]
	if cfg.artifacts.operation != "list" &&
		cfg.artifacts.operation != "extract" &&
		cfg.artifacts.operation != "serve" &&
		cfg.artifacts.operation != "clean" {
		return subcommandError(flags,
			errors.New("use 'edgar artifacts <list|extract|serve|clean>'"))
	}

	flags.StringVar(&cfg.artifacts.cik, "cik", "", "filing CIK")
	flags.StringVar(&cfg.artifacts.accession, "accession", "", "filing accession number")
	flags.StringVar(&cfg.artifacts.parsedDir, "parsed-dir", cfg.artifacts.parsedDir, "parsed filing directory")
	flags.StringVar(&cfg.artifacts.filingsDir, "filings-dir", cfg.artifacts.filingsDir, "raw filing cache directory")
	flags.StringVar(&cfg.artifacts.artifactsDir, "artifacts-dir", cfg.artifacts.artifactsDir, "disposable artifact root")
	flags.StringVar(&cfg.artifacts.address, "address", cfg.artifacts.address, "viewer listen address")
	flags.Var(&cfg.artifacts.types, "type", "document type to extract")
	flags.Var(&cfg.artifacts.filenames, "document", "document filename to extract")
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
		return subcommandError(flags, errors.New("--accession must be a safe accession number"))
	}

	if cfg.artifacts.operation == "extract" && !cfg.artifacts.all &&
		len(cfg.artifacts.types) == 0 && len(cfg.artifacts.filenames) == 0 {
		return subcommandError(flags, errors.New(
			"extract requires --type, --document, or --all"))
	}

	if cfg.artifacts.operation == "serve" {
		if err := validateViewerAddress(cfg.artifacts.address); err != nil {
			return subcommandError(flags, err)
		}
	}

	return cfg, nil
}

func runArtifacts(ctx context.Context, cfg artifactCommandConfig) error {
	destination := filepath.Join(cfg.artifactsDir, cfg.cik, cfg.accession)
	if cfg.operation == "serve" {
		return serveArtifacts(ctx, cfg, destination)
	}

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

func validateViewerAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("--address must be host:port: %w", err)
	}

	if !strings.EqualFold(host, "localhost") {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return errors.New("--address must use a loopback host")
		}
	}

	return nil
}

func serveArtifacts(ctx context.Context, cfg artifactCommandConfig, destination string) error {
	handler, err := artifacts.Viewer(destination, cfg.cik, cfg.accession)
	if err != nil {
		return err
	}

	listenConfig := net.ListenConfig{}

	listener, err := listenConfig.Listen(ctx, "tcp", cfg.address)
	if err != nil {
		return fmt.Errorf("listen for artifact viewer: %w", err)
	}

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	done := make(chan struct{})

	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(
				context.WithoutCancel(ctx),
				5*time.Second)
			defer cancel()

			_ = server.Shutdown(shutdownCtx)
		case <-done:
		}
	}()

	defer close(done)

	fmt.Printf("viewing %s at http://%s (press Ctrl-C to stop)\n", destination, listener.Addr())

	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve artifact viewer: %w", err)
	}

	return nil
}

func artifactSourcePath(filingsDir string, filing *edgar.ParsedFiling,
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
