// Package main runs the ECB exchange-rate command-line application.
package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"wingman.com/fetch-ecb/internal/config"
	"wingman.com/fetch-ecb/internal/ecb"
	"wingman.com/fetch-ecb/internal/xerr"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)

	if err := run(ctx); err != nil {
		stop()
		logCLIError(err)
		os.Exit(1)
	}

	stop()
}

type App struct {
	Config config.Config
	Client *http.Client
	Stdout io.Writer
}

func NewApp(cfg config.Config, stdout io.Writer) *App {
	return &App{
		Config: cfg,
		Client: &http.Client{
			Transport:     nil,
			CheckRedirect: nil,
			Jar:           nil,
			Timeout:       config.RequestTimeout,
		},
		Stdout: stdout,
	}
}

func (a *App) Run(ctx context.Context) error {
	return ecb.FetchAndWrite(ctx, a.Client, a.Config, a.Stdout)
}

func run(ctx context.Context) error {
	cfg, err := config.ParseConfig(os.Args[1:], time.Now())
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return xerr.Wrap(xerr.InvalidInput, "INVALID_CONFIGURATION", "invalid command-line configuration", err)
	}

	return NewApp(cfg, os.Stdout).Run(ctx)
}

func logCLIError(err error) {
	if structured, ok := errors.AsType[*xerr.Error](err); ok {
		log.Printf("[%d] %s: %s", structured.Code, structured.Reason, structured.Error())

		return
	}

	log.Print(err)
}
