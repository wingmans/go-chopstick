// Package config parses and validates application configuration.
package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	// Dataset is the fixed ECB exchange-rate dataflow.
	Dataset = "EXR"

	defaultCurrency      = "USD"
	defaultCurrencyDenom = "EUR"
	defaultOutput        = "text"
	dateLayout           = "2006-01-02"
)

// RequestTimeout is the fixed timeout for ECB requests.
const RequestTimeout = 10 * time.Second

// Config contains the application inputs used to request ECB data.
type Config struct {
	Currency      string
	CurrencyDenom string
	StartPeriod   string
	EndPeriod     string
	Output        string
}

// ParseConfig parses command-line arguments and environment-backed defaults.
func ParseConfig(args []string, now time.Time) (Config, error) {
	startPeriod, endPeriod := defaultPeriods(now)

	cfg := Config{
		Currency:      envOrDefault("ECB_CURRENCY", defaultCurrency),
		CurrencyDenom: envOrDefault("ECB_CURRENCY_DENOM", defaultCurrencyDenom),
		StartPeriod:   envOrDefault("ECB_START_PERIOD", startPeriod),
		EndPeriod:     envOrDefault("ECB_END_PERIOD", endPeriod),
		Output:        envOrDefault("ECB_OUTPUT", defaultOutput),
	}

	flags := flag.NewFlagSet("ecb", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.Usage = func() {
		fmt.Fprintln(os.Stdout, "Usage: ecb [options]")
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "Options:")
		fmt.Fprintf(os.Stdout, "  -c, --currency           ECB currency (default %q)\n", cfg.Currency)
		fmt.Fprintf(os.Stdout, "  -n, --currency-denom     ECB currency denomination (default %q)\n", cfg.CurrencyDenom)
		fmt.Fprintf(os.Stdout, "  -s, --start              Start period (YYYY-MM-DD) (default %q)\n", cfg.StartPeriod)
		fmt.Fprintf(os.Stdout, "  -e, --end                End period (YYYY-MM-DD) (default %q)\n", cfg.EndPeriod)
		fmt.Fprintf(os.Stdout, "  -o, --output             Output format: text, json, csv (default %q)\n", cfg.Output)
	}

	flags.StringVar(&cfg.Currency, "c", cfg.Currency, "ECB currency")
	flags.StringVar(&cfg.Currency, "currency", cfg.Currency, "ECB currency")
	flags.StringVar(&cfg.CurrencyDenom, "n", cfg.CurrencyDenom, "ECB currency denomination")
	flags.StringVar(&cfg.CurrencyDenom, "currency-denom", cfg.CurrencyDenom, "ECB currency denomination")
	flags.StringVar(&cfg.StartPeriod, "d", cfg.StartPeriod, "Start period (YYYY-MM-DD)")
	flags.StringVar(&cfg.StartPeriod, "start", cfg.StartPeriod, "Start period (YYYY-MM-DD)")
	flags.StringVar(&cfg.EndPeriod, "e", cfg.EndPeriod, "End period (YYYY-MM-DD)")
	flags.StringVar(&cfg.EndPeriod, "end", cfg.EndPeriod, "End period (YYYY-MM-DD)")
	flags.StringVar(&cfg.Output, "o", cfg.Output, "Output format: text, json, csv")
	flags.StringVar(&cfg.Output, "output", cfg.Output, "Output format: text, json, csv")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return Config{}, flag.ErrHelp
		}

		return Config{}, err
	}

	cfg.Currency = strings.ToUpper(strings.TrimSpace(cfg.Currency))
	cfg.CurrencyDenom = strings.ToUpper(strings.TrimSpace(cfg.CurrencyDenom))
	cfg.StartPeriod = strings.TrimSpace(cfg.StartPeriod)
	cfg.EndPeriod = strings.TrimSpace(cfg.EndPeriod)
	cfg.Output = strings.ToLower(strings.TrimSpace(cfg.Output))

	if err := validateConfig(cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func validateConfig(cfg Config) error {
	if cfg.Currency == "" {
		return errors.New("currency is required")
	}

	if cfg.CurrencyDenom == "" {
		return errors.New("currency denomination is required")
	}

	switch cfg.Output {
	case "text", "json", "csv":
	default:
		return fmt.Errorf("invalid output %q: must be text, json, or csv", cfg.Output)
	}

	var (
		start time.Time
		end   time.Time
		err   error
	)

	if cfg.StartPeriod != "" {
		start, err = time.Parse(dateLayout, cfg.StartPeriod)
		if err != nil {
			return fmt.Errorf("invalid start date %q: must use %s", cfg.StartPeriod, dateLayout)
		}
	}

	if cfg.EndPeriod != "" {
		end, err = time.Parse(dateLayout, cfg.EndPeriod)
		if err != nil {
			return fmt.Errorf("invalid end date %q: must use %s", cfg.EndPeriod, dateLayout)
		}
	}

	if !start.IsZero() && !end.IsZero() && start.After(end) {
		return errors.New("start date must be on or before end date")
	}

	return nil
}

func defaultPeriods(now time.Time) (string, string) {
	now = now.UTC()

	return now.AddDate(0, -1, 0).Format(dateLayout), now.Format(dateLayout)
}

func envOrDefault(key, defaultValue string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return defaultValue
	}

	return value
}
