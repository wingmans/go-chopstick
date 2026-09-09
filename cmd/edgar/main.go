// Package main runs the SEC EDGAR filing-index downloader.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"wingman.com/fetch-ecb/internal/ctxlog"
	"wingman.com/fetch-ecb/internal/edgar"
)

type appConfig struct {
	directory     string
	sinceYear     int
	userAgent     string
	refreshLatest bool
}

func main() {
	if err := runMain(); err != nil {
		os.Exit(1)
	}
}

func runMain() error {
	logger := ctxlog.New()
	ctx := ctxlog.WithLogger(context.Background(), logger)

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:]); err != nil {
		if errors.Is(err, context.Canceled) {
			logger.Info("shutdown requested")

			return nil
		}

		logger.Error("command failed", "error", err)

		return err
	}

	return nil
}

func run(ctx context.Context, args []string) error {
	cfg, err := parseConfig(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return err
	}

	logger := ctxlog.FromContext(ctx)
	started := time.Now()

	logger.Info("starting EDGAR download", "from_year", cfg.sinceYear, "directory", cfg.directory)
	defer func() {
		logger.Info("finished EDGAR download", "duration_ms", time.Since(started).Milliseconds())
	}()

	err = edgar.DownloadIndex(ctx, &http.Client{
		Transport:     nil,
		CheckRedirect: nil,
		Jar:           nil,
		Timeout:       30 * time.Second,
	}, edgar.Config{
		Directory:     cfg.directory,
		SinceYear:     cfg.sinceYear,
		UserAgent:     cfg.userAgent,
		RefreshLatest: cfg.refreshLatest,
		BaseURL:       "",
	})
	if err != nil {
		return err
	}

	return nil
}

func parseConfig(args []string) (appConfig, error) {
	cfg := appConfig{
		directory:     "",
		sinceYear:     0,
		userAgent:     "",
		refreshLatest: false,
	}
	flags := flag.NewFlagSet("edgar", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() {
		fmt.Fprintln(os.Stdout, "Usage: edgar [options]")
		fmt.Fprintln(os.Stdout)
		flags.PrintDefaults()
	}
	flags.StringVar(&cfg.directory, "d", "./data", "directory for downloaded index files")
	flags.StringVar(&cfg.directory, "directory", "./data", "directory for downloaded index files")
	flags.IntVar(&cfg.sinceYear, "y", edgar.EarliestYear, "first year to download")
	flags.IntVar(&cfg.sinceYear, "from-year", edgar.EarliestYear, "first year to download")
	flags.StringVar(&cfg.userAgent, "ua", "", "SEC User-Agent, including a contact email address")
	flags.StringVar(&cfg.userAgent, "user-agent", "", "SEC User-Agent, including a contact email address")
	flags.BoolVar(&cfg.refreshLatest, "s", false, "refresh the latest quarter and reuse older files")
	flags.BoolVar(&cfg.refreshLatest, "refresh-latest", false, "refresh the latest quarter and reuse older files")

	if err := flags.Parse(args); err != nil {
		return appConfig{}, err
	}

	if cfg.sinceYear < edgar.EarliestYear {
		return appConfig{}, fmt.Errorf("-from-year must be %d or later", edgar.EarliestYear)
	}

	if cfg.userAgent == "" {
		return appConfig{}, errors.New("-user-agent is required")
	}

	return cfg, nil
}
