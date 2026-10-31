// Command chopd runs the Chop application as one foreground process.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"wingman.com/chopstick/internal/ctxlog"
	"wingman.com/chopstick/internal/edgarapp"
	"wingman.com/chopstick/internal/envfile"
)

func main() {
	if err := run(); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}

		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if err := envfile.Load(".env"); err != nil {
		return err
	}

	config, err := edgarapp.ConfigFromEnvironment()
	if err != nil {
		return err
	}

	flags := flag.NewFlagSet("chopd", flag.ContinueOnError)
	flags.StringVar(&config.NATSURL, "nats-url", config.NATSURL, "NATS server URL")
	flags.StringVar(&config.HTTPAddress, "address", config.HTTPAddress, "HTTP listen address")
	flags.StringVar(&config.PollingDB, "polling-db", config.PollingDB, "pipeline state database")
	flags.StringVar(&config.NewsDB, "news-db", config.NewsDB, "news projection database")
	flags.StringVar(&config.FilingsDirectory, "filings-dir", config.FilingsDirectory, "downloaded filing directory")
	flags.StringVar(&config.ParsedDirectory, "parsed-dir", config.ParsedDirectory, "parsed filing directory")
	flags.StringVar(&config.AnalysisDB, "analysis-db", config.AnalysisDB, "analysis database")
	flags.StringVar(&config.OwnershipDB, "ownership-db", config.OwnershipDB, "ownership database")
	flags.StringVar(&config.UserAgent, "user-agent", config.UserAgent, "SEC user agent")
	flags.StringVar(&config.BaseURL, "base-url", config.BaseURL, "SEC Archives base URL override")
	flags.DurationVar(&config.Interval, "interval", config.Interval, "SEC Atom polling interval")
	flags.IntVar(&config.MaxAttempts, "max-attempts", config.MaxAttempts, "attempts before work is marked failed")

	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}

	if flags.NArg() != 0 {
		return errors.New("chopd does not accept positional arguments")
	}

	if err := config.Validate(); err != nil {
		return err
	}

	logger := ctxlog.New()
	ctx := ctxlog.WithLogger(context.Background(), logger)

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("starting Chop application",
		"address", config.HTTPAddress,
		"nats_url", config.NATSURL,
		"set", config.SetName,
		"form_types", config.FormTypes,
	)

	if err := edgarapp.Run(ctx, config, logger); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}

		return fmt.Errorf("run Chop application: %w", err)
	}

	return nil
}
