package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"wingman.com/chopstick/internal/appdata"
	"wingman.com/chopstick/internal/news"
	filingweb "wingman.com/chopstick/internal/web"
)

func parseServeConfig(args []string) (appConfig, error) {
	var cfg appConfig

	cfg.command = "serve"
	cfg.serve.address = webAddressFromEnvironment()
	cfg.serve.parsedDir = appdata.Path("parsed")
	cfg.serve.analysisDB = appdata.Path("analysis.db")
	cfg.serve.ownershipDB = appdata.Path("ownership.db")
	cfg.serve.newsDB = appdata.Path("feed.db")

	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.Usage = func() {
		printSubcommandHelp("serve", "Serve the local web application.", []helpOption{
			{"-a, --address <host:port>", "HTTP listen address; default 127.0.0.1:8083."},
			{"-d, --parsed-dir <path>", "Parsed filing directory."},
			{"-s, --set <name>", "Select companies and resolve company searches."},
			{"--ownership-db <path>", "Ownership SQLite database."},
			{"--news-db <path>", "News SQLite database."},
			{"-h, --help", "Show command help."},
		})
	}
	flags.StringVar(&cfg.serve.address, "a", cfg.serve.address, "HTTP listen address")
	flags.StringVar(&cfg.serve.address, "address", cfg.serve.address, "HTTP listen address")
	flags.StringVar(&cfg.serve.parsedDir, "d", cfg.serve.parsedDir, "parsed filing directory")
	flags.StringVar(&cfg.serve.parsedDir, "parsed-dir", cfg.serve.parsedDir, "parsed filing directory")
	flags.StringVar(&cfg.serve.analysisDB, "analysis-db", cfg.serve.analysisDB, "SQLite analysis database")
	flags.StringVar(&cfg.serve.ownershipDB, "ownership-db",
		cfg.serve.ownershipDB, "ownership SQLite database")
	flags.StringVar(&cfg.serve.newsDB, "news-db", cfg.serve.newsDB,
		"news SQLite database")
	flags.StringVar(&cfg.serve.setName, "s", "",
		"select companies and resolve company searches")
	flags.StringVar(&cfg.serve.setName, "set", "",
		"select companies and resolve company searches")

	if err := flags.Parse(args); err != nil {
		return appConfig{}, flag.ErrHelp
	}

	if flags.NArg() != 0 {
		return subcommandError(flags, errors.New("unexpected positional arguments"))
	}

	if cfg.serve.address == "" {
		return subcommandError(flags, errors.New("--address cannot be empty"))
	}

	if cfg.serve.parsedDir == "" {
		return subcommandError(flags, errors.New("--parsed-dir cannot be empty"))
	}

	if cfg.serve.ownershipDB == "" {
		return subcommandError(flags,
			errors.New("--ownership-db cannot be empty"))
	}

	if cfg.serve.newsDB == "" {
		return subcommandError(flags, errors.New("--news-db cannot be empty"))
	}

	if strings.ContainsAny(cfg.serve.setName, `/\\`) {
		return subcommandError(flags, errors.New("--set must be a set name, not a path"))
	}

	return cfg, nil
}

func runServe(ctx context.Context, logger *slog.Logger, address, parsedDir,
	setName, analysisDB, ownershipDB, newsDB string,
) error {
	server, err := filingweb.NewConfiguredServerWithDatabases(ctx, parsedDir,
		analysisDB, ownershipDB, appdata.Path("sets"), setName, logger)
	if err != nil {
		return err
	}
	defer func() { _ = server.Close() }()

	newsStore, _, err := news.Open(ctx, newsDB)
	if err != nil {
		return err
	}
	defer func() { _ = newsStore.Close() }()

	server.MountNews(filingweb.NewNewsServer(newsStore, nil, logger))

	httpServer := &http.Server{ //nolint:exhaustruct_v5 // standard library server has many optional fields.
		Addr:              address,
		Handler:           server,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()

		shutdownContext, cancel := context.WithTimeout(
			context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()

		_ = httpServer.Shutdown(shutdownContext)
	}()

	err = httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}

	return err
}
