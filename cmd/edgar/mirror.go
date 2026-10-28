package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"wingman.com/fetch-ecb/internal/appdata"
	"wingman.com/fetch-ecb/internal/mirror"
	"wingman.com/fetch-ecb/internal/mirrorhttp"
)

const edgarMirrorAddressEnv = "EDGAR_MIRROR_ADDRESS"

type mirrorCommandConfig struct {
	operation  string
	address    string
	objectsDir string
	catalogDB  string
	tempDir    string
	sourceDir  string
	userAgent  string
	baseURL    string
}

func parseMirrorConfig(args []string) (appConfig, error) {
	cfg := appConfig{command: "mirror"}
	cfg.mirror = mirrorCommandConfig{
		address:    environmentValue(edgarMirrorAddressEnv, "127.0.0.1:8084"),
		objectsDir: appdata.Path("mirror", "objects"),
		catalogDB:  appdata.Path("mirror", "catalog.db"),
		tempDir:    appdata.Path("mirror", "tmp"),
		userAgent:  environmentValue(edgarUserAgentEnv, defaultUserAgent),
		baseURL:    environmentValue(edgarBaseURLEnv, ""),
	}

	flags := flag.NewFlagSet("mirror", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.Usage = func() {
		printSubcommandHelp("mirror <serve|import|stats|verify|repair|catalog rebuild>",
			"Manage the isolated compressed EDGAR mirror.", []helpOption{
				{"--address <host:port>", "REST address; loopback only."},
				{"--objects-dir <path>", "Compressed object directory."},
				{"--catalog <path>", "Disposable SQLite catalog."},
				{"--temp-dir <path>", "Bounded materialization directory."},
				{"--source <path>", "Existing raw cache for import."},
				{"-h, --help", "Show command help."},
			})
	}

	if len(args) == 0 {
		return subcommandError(flags, errors.New("mirror operation is required"))
	}

	cfg.mirror.operation = args[0]

	remaining := args[1:]
	if cfg.mirror.operation == "catalog" {
		if len(remaining) == 0 || remaining[0] != "rebuild" {
			return subcommandError(flags,
				errors.New("use 'edgar mirror catalog rebuild'"))
		}

		cfg.mirror.operation = "catalog-rebuild"
		remaining = remaining[1:]
	}

	switch cfg.mirror.operation {
	case "serve", "import", "stats", "verify", "repair", "catalog-rebuild":
	default:
		return subcommandError(flags,
			errors.New("unknown mirror operation"))
	}

	flags.StringVar(&cfg.mirror.address, "address", cfg.mirror.address,
		"REST listen address")
	flags.StringVar(&cfg.mirror.objectsDir, "objects-dir",
		cfg.mirror.objectsDir, "compressed object directory")
	flags.StringVar(&cfg.mirror.catalogDB, "catalog", cfg.mirror.catalogDB,
		"SQLite catalog")
	flags.StringVar(&cfg.mirror.tempDir, "temp-dir", cfg.mirror.tempDir,
		"temporary materialization directory")
	flags.StringVar(&cfg.mirror.sourceDir, "source", "",
		"existing raw cache for import")

	if err := flags.Parse(remaining); err != nil {
		return appConfig{}, flag.ErrHelp
	}

	if flags.NArg() != 0 {
		return subcommandError(flags, errors.New("unexpected positional argument"))
	}

	if cfg.mirror.operation == "import" && cfg.mirror.sourceDir == "" {
		return subcommandError(flags, errors.New("--source is required for import"))
	}

	if cfg.mirror.address == "" || cfg.mirror.objectsDir == "" ||
		cfg.mirror.catalogDB == "" || cfg.mirror.tempDir == "" {
		return subcommandError(flags,
			errors.New("mirror paths and address cannot be empty"))
	}

	if cfg.mirror.operation == "serve" {
		if err := validateLoopbackAddress(cfg.mirror.address); err != nil {
			return subcommandError(flags, err)
		}
	}

	return cfg, nil
}

func runMirrorCommand(ctx context.Context, client *http.Client,
	cfg mirrorCommandConfig, logger *slog.Logger,
) error {
	store, err := mirror.NewFileStore(cfg.objectsDir)
	if err != nil {
		return err
	}

	if cfg.operation == "catalog-rebuild" {
		result, err := mirror.RebuildCatalog(ctx, cfg.catalogDB, cfg.tempDir,
			store)
		if err == nil {
			fmt.Printf("rebuilt mirror catalog with %d submissions\n",
				result.Indexed)

			printMirrorProblems(result.Problems)
		}

		return err
	}

	if _, err := os.Stat(cfg.catalogDB); errors.Is(err, os.ErrNotExist) {
		logger.Info("mirror catalog missing; rebuilding", "path", cfg.catalogDB)

		result, rebuildErr := mirror.RebuildCatalog(ctx, cfg.catalogDB,
			cfg.tempDir, store)
		if rebuildErr != nil {
			return rebuildErr
		}

		logger.Info("mirror catalog rebuilt", "submissions", result.Indexed,
			"problems", len(result.Problems))

		for _, problem := range result.Problems {
			logger.Warn("mirror object omitted from rebuilt catalog",
				"error", problem)
		}
	} else if err != nil {
		return fmt.Errorf("inspect mirror catalog: %w", err)
	}

	catalog, _, err := mirror.OpenCatalog(ctx, cfg.catalogDB)
	if errors.Is(err, mirror.ErrCatalogSchema) {
		logger.Info("mirror catalog schema incompatible; rebuilding",
			"path", cfg.catalogDB)

		result, rebuildErr := mirror.RebuildCatalog(ctx, cfg.catalogDB,
			cfg.tempDir, store)
		if rebuildErr != nil {
			return rebuildErr
		}

		logger.Info("mirror catalog rebuilt", "submissions", result.Indexed,
			"problems", len(result.Problems))

		for _, problem := range result.Problems {
			logger.Warn("mirror object omitted from rebuilt catalog",
				"error", problem)
		}

		catalog, _, err = mirror.OpenCatalog(ctx, cfg.catalogDB)
	}

	if err != nil {
		return err
	}

	defer func() { _ = catalog.Close() }()

	origin, err := mirror.NewSECOrigin(client, cfg.baseURL, cfg.userAgent)
	if err != nil {
		return err
	}

	service, err := mirror.NewService(store, catalog, origin, cfg.tempDir)
	if err != nil {
		return err
	}

	switch cfg.operation {
	case "serve":
		return serveMirror(ctx, cfg.address, service)
	case "import":
		count, importErr := service.Import(ctx, cfg.sourceDir)
		if importErr == nil {
			fmt.Printf("imported %d mirror submissions\n", count)

			if err := printMirrorStats(ctx, service); err != nil {
				return err
			}
		}

		return importErr
	case "stats":
		return printMirrorStats(ctx, service)
	case "verify":
		valid, problems, verifyErr := mirror.Verify(ctx, store, cfg.tempDir)
		fmt.Printf("verified %d mirror submissions\n", valid)

		for _, problem := range problems {
			fmt.Printf("  ERROR %v\n", problem)
		}

		if verifyErr != nil {
			return verifyErr
		}

		if len(problems) > 0 {
			return fmt.Errorf("mirror verification found %d problems",
				len(problems))
		}

		return nil
	case "repair":
		repaired, problems, repairErr := mirror.Repair(ctx, store, catalog,
			origin, cfg.tempDir)
		fmt.Printf("repaired %d mirror submissions\n", repaired)

		for _, problem := range problems {
			fmt.Printf("  ERROR %v\n", problem)
		}

		if repairErr != nil {
			return repairErr
		}

		if len(problems) > 0 {
			return fmt.Errorf("mirror repair left %d problems", len(problems))
		}

		return nil
	default:
		return fmt.Errorf("unsupported mirror operation %q", cfg.operation)
	}
}

func printMirrorProblems(problems []error) {
	for _, problem := range problems {
		fmt.Printf("  ERROR %v\n", problem)
	}
}

func serveMirror(ctx context.Context, address string,
	service mirror.API,
) error {
	server := &http.Server{
		Addr: address, Handler: mirrorhttp.New(service),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()

		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx),
			10*time.Second)
		defer cancel()

		_ = server.Shutdown(shutdown)
	}()

	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}

	return err
}

func printMirrorStats(ctx context.Context, service mirror.API) error {
	stats, err := service.Stats(ctx)
	if err != nil {
		return err
	}

	fmt.Printf("objects             %d\n", stats.Objects)
	fmt.Printf("logical bytes       %d\n", stats.LogicalBytes)
	fmt.Printf("stored bytes        %d\n", stats.StoredBytes)
	fmt.Printf("saved bytes         %d\n", stats.SavedBytes)
	fmt.Printf("compression ratio   %.2f%%\n", stats.Ratio*100)

	return nil
}
