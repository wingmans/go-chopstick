// Package main runs the ECB exchange-rate command-line application.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
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
	cfg, err := parseConfig(os.Args[1:], time.Now())
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return xerr.Wrap(xerr.InvalidInput, "INVALID_CONFIGURATION", "invalid command-line configuration", err)
	}

	return NewApp(cfg, os.Stdout).Run(ctx)
}

func parseConfig(args []string, now time.Time) (config.Config, error) {
	startPeriod, endPeriod := defaultPeriods(now)
	cfg := config.Config{
		Currency:      envOrDefault("ECB_CURRENCY", "USD"),
		CurrencyDenom: envOrDefault("ECB_CURRENCY_DENOM", "EUR"),
		StartPeriod:   envOrDefault("ECB_START_PERIOD", startPeriod),
		EndPeriod:     envOrDefault("ECB_END_PERIOD", endPeriod),
		Output:        envOrDefault("ECB_OUTPUT", "text"),
	}

	flags := flag.NewFlagSet("ecb", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() {
		fmt.Fprintln(os.Stdout, "Usage: ecb [options]")
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "Options:")
		flags.PrintDefaults()
	}
	flags.StringVar(&cfg.Currency, "c", cfg.Currency, "ECB currency")
	flags.StringVar(&cfg.Currency, "currency", cfg.Currency, "ECB currency")
	flags.StringVar(&cfg.CurrencyDenom, "n", cfg.CurrencyDenom, "ECB currency denomination")
	flags.StringVar(&cfg.CurrencyDenom, "currency-denom", cfg.CurrencyDenom, "ECB currency denomination")
	flags.StringVar(&cfg.StartPeriod, "d", cfg.StartPeriod, "Start period (YYYY-MM-DD)")
	flags.StringVar(&cfg.StartPeriod, "s", cfg.StartPeriod, "Start period (YYYY-MM-DD)")
	flags.StringVar(&cfg.StartPeriod, "start", cfg.StartPeriod, "Start period (YYYY-MM-DD)")
	flags.StringVar(&cfg.EndPeriod, "e", cfg.EndPeriod, "End period (YYYY-MM-DD)")
	flags.StringVar(&cfg.EndPeriod, "end", cfg.EndPeriod, "End period (YYYY-MM-DD)")
	flags.StringVar(&cfg.Output, "o", cfg.Output, "Output format: text, json, csv")
	flags.StringVar(&cfg.Output, "output", cfg.Output, "Output format: text, json, csv")

	if err := flags.Parse(args); err != nil {
		return config.Config{}, err
	}

	cfg.Currency = strings.ToUpper(strings.TrimSpace(cfg.Currency))
	cfg.CurrencyDenom = strings.ToUpper(strings.TrimSpace(cfg.CurrencyDenom))
	cfg.StartPeriod = strings.TrimSpace(cfg.StartPeriod)
	cfg.EndPeriod = strings.TrimSpace(cfg.EndPeriod)
	cfg.Output = strings.ToLower(strings.TrimSpace(cfg.Output))

	if err := cfg.Validate(); err != nil {
		return config.Config{}, err
	}

	return cfg, nil
}

func defaultPeriods(now time.Time) (string, string) {
	now = now.UTC()

	return now.AddDate(0, -1, 0).Format("2006-01-02"), now.Format("2006-01-02")
}

func envOrDefault(key, defaultValue string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultValue
	}

	return value
}

func logCLIError(err error) {
	if structured, ok := errors.AsType[*xerr.Error](err); ok {
		log.Printf("[%d] %s: %s", structured.Code, structured.Reason, structured.Error())

		return
	}

	log.Print(err)
}
