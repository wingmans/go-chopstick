package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"

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
			{"-h, --help", "Show command help."},
		})
	}
	flags.StringVar(&cfg.serve.address, "a", cfg.serve.address, "HTTP listen address")
	flags.StringVar(&cfg.serve.address, "address", cfg.serve.address, "HTTP listen address")
	flags.StringVar(&cfg.serve.parsedDir, "d", cfg.serve.parsedDir, "parsed filing directory")
	flags.StringVar(&cfg.serve.parsedDir, "parsed-dir", cfg.serve.parsedDir, "parsed filing directory")

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

	return cfg, nil
}

func runServe(ctx context.Context, logger *slog.Logger, address, parsedDir string) error {
	httpServer := &http.Server{Addr: address, Handler: filingweb.NewServer(parsedDir, logger)}

	go func() {
		<-ctx.Done()

		_ = httpServer.Shutdown(context.Background())
	}()

	err := httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}

	return err
}
