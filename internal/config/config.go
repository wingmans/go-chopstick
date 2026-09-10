// Package config defines and validates application configuration.
package config

import (
	"errors"
	"fmt"
	"time"
)

const (
	// Dataset is the fixed ECB exchange-rate dataflow.
	Dataset = "EXR"

	dateLayout = "2006-01-02"
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

// Validate checks that the configuration contains supported values.
func (cfg Config) Validate() error {
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
