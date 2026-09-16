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

	"wingman.com/fetch-ecb/internal/edgar"
	"wingman.com/fetch-ecb/internal/filingweb"
)

func parseServeConfig(args []string) (appConfig, error) {
	var cfg appConfig

	cfg.command = "serve"
	cfg.serve.address = "127.0.0.1:8080"
	cfg.serve.parsedDir = edgar.DefaultParsedDirectory

	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.Usage = func() {
		printSubcommandHelp("serve", "Browse locally parsed filing data.", []helpOption{
			{"-a, --address <host:port>", "HTTP listen address; default 127.0.0.1:8080."},
			{"-d, --parsed-dir <path>", "Parsed filing directory; default ./data/parsed."},
			{"-s, --set <name>", "Resolve ticker and company searches from data/sets/<name>.json."},
			{"-h, --help", "Show command help."},
		})
	}
	flags.StringVar(&cfg.serve.address, "a", cfg.serve.address, "HTTP listen address")
	flags.StringVar(&cfg.serve.address, "address", cfg.serve.address, "HTTP listen address")
	flags.StringVar(&cfg.serve.parsedDir, "d", cfg.serve.parsedDir, "parsed filing directory")
	flags.StringVar(&cfg.serve.parsedDir, "parsed-dir", cfg.serve.parsedDir, "parsed filing directory")
	flags.StringVar(&cfg.serve.setName, "s", "", "resolve human-readable company searches from a set")
	flags.StringVar(&cfg.serve.setName, "set", "", "resolve human-readable company searches from a set")

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

	if strings.ContainsAny(cfg.serve.setName, `/\\`) {
		return subcommandError(flags, errors.New("--set must be a set name, not a path"))
	}

	return cfg, nil
}

func runServe(ctx context.Context, logger *slog.Logger, address, parsedDir, setName string) error {
	server, err := filingweb.NewConfiguredServer(parsedDir, defaultSetDir, setName, logger)
	if err != nil {
		return err
	}

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
