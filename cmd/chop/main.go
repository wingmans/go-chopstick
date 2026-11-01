// Command chop manages SEC EDGAR data and the local application.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alecthomas/kong"

	"wingman.com/chopstick/internal/buildinfo"
	"wingman.com/chopstick/internal/choppie"
	"wingman.com/chopstick/internal/ctxlog"
	"wingman.com/chopstick/internal/envfile"
)

func main() {
	os.Exit(runMain(os.Args[1:]))
}

func runMain(args []string) int {
	if err := envfile.Load(".env"); err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	defaults, err := commandDefaultsFromEnvironment()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	var model cli

	parser, err := newParser(&model, defaults)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	args = commandArgs(args)

	parsed, err := parser.Parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	if model.Verbose {
		level := "debug"
		model.Log = &level
	}

	if model.Log != nil {
		if err := os.Setenv("LOG_LEVEL", *model.Log); err != nil {
			fmt.Fprintln(os.Stderr, err)

			return 1
		}
	}

	logger := ctxlog.New()
	ctx := ctxlog.WithLogger(context.Background(), logger)

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	runtime := &commandRuntime{
		Client:  &http.Client{Timeout: 30 * time.Second},
		Logger:  logger,
		Verbose: model.Verbose,
	}

	started := time.Now()

	if err := parsed.Run(ctx, runtime); err != nil {
		err = choppie.ClassifyError(ctx, err)
		if errors.Is(err, context.Canceled) {
			logger.Info("shutdown requested")

			return 0
		}

		logger.Error("command failed", "error", err)

		return 1
	}

	logger.Debug("finished Chop command", "command", parsed.Command(),
		"duration_ms", time.Since(started).Milliseconds())

	return 0
}

func commandArgs(args []string) []string {
	if len(args) == 0 {
		return []string{"--help"}
	}

	return args
}

func newParser(model *cli, defaults commandDefaults) (*kong.Kong, error) {
	return kong.New(model,
		kong.Name("chop"),
		kong.Description(
			"Operate the local Chop application and SEC EDGAR data."),
		kong.Writers(os.Stdout, os.Stderr),
		kong.Vars(defaults.vars(buildinfo.Version)),
		kong.Groups{
			"operations": "Operations",
			"data":       "EDGAR data",
			"diag":       "Troubleshooting",
		},
		kong.ConfigureHelp(kong.HelpOptions{
			Compact: true, Summary: true, NoExpandSubcommands: true,
			WrapUpperBound: 100,
		}),
	)
}
