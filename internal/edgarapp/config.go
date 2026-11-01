// Package edgarapp composes the long-running EDGAR monolith.
package edgarapp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"wingman.com/chopstick/internal/appdata"
	"wingman.com/chopstick/internal/constituents"
	"wingman.com/chopstick/internal/edgar"
	"wingman.com/chopstick/internal/eventbus"
	"wingman.com/chopstick/internal/transport"
)

const (
	DefaultUserAgent      = "wingman paul@wingmen.io"
	initialRoleRetryDelay = time.Second
	maximumRoleRetryDelay = 30 * time.Second
	stableRoleRun         = time.Minute
	newsShutdownTimeout   = 5 * time.Second
)

// Config contains the typed configuration of the long-running application.
type Config struct {
	NATSURL           string
	CommandAddress    string
	PollingDB         string
	NewsDB            string
	FilingsDirectory  string
	ParsedDirectory   string
	AnalysisDB        string
	OwnershipDB       string
	MasterPath        string
	IndexDirectory    string
	IndexZipDirectory string
	UserAgent         string
	BaseURL           string
	HTTPAddress       string
	Interval          time.Duration
	SetName           string
	CIKs              []string
	FormTypes         []string
	FromYear          int
	MaxAttempts       int
}

// ConfigFromEnvironment returns service configuration without involving CLI
// parsing. Command-line adapters may override the returned typed values.
func ConfigFromEnvironment() (Config, error) {
	fromYear, err := environmentInt("EDGAR_FROM_YEAR",
		edgar.EarliestPipelineYear)
	if err != nil {
		return Config{}, err
	}

	forms, err := edgar.ValidateSupportedFormTypes(strings.Split(
		environmentValue("EDGAR_FORM_TYPES", "10-K,10-Q,8-K"), ",",
	))
	if err != nil {
		return Config{}, err
	}

	config := Config{
		NATSURL:           environmentValue("EDGAR_NATS_URL", eventbus.DefaultURL),
		CommandAddress:    environmentValue("CHOP_COMMAND_ADDRESS", transport.DefaultAddress),
		PollingDB:         appdata.Path("polling.db"),
		NewsDB:            appdata.Path("feed.db"),
		FilingsDirectory:  appdata.Path("cache", "filings"),
		ParsedDirectory:   appdata.Path("parsed"),
		AnalysisDB:        appdata.Path("analysis.db"),
		OwnershipDB:       appdata.Path("ownership.db"),
		MasterPath:        appdata.Path("cache", "indexes", "master.tsv"),
		IndexDirectory:    appdata.Path("cache", "indexes", "quarterly"),
		IndexZipDirectory: appdata.Path("cache", "index-zips"),
		UserAgent:         environmentValue("EDGAR_USER_AGENT", DefaultUserAgent),
		BaseURL:           environmentValue("EDGAR_BASE_URL", ""),
		HTTPAddress:       webAddressFromEnvironment(),
		Interval:          time.Minute,
		SetName:           environmentValue("EDGAR_SET", ""),
		CIKs:              nil,
		FormTypes:         forms,
		FromYear:          fromYear,
		MaxAttempts:       5,
	}

	if err := config.SetConstituentSet(config.SetName); err != nil {
		return Config{}, err
	}

	return config, config.Validate()
}

// SetConstituentSet resolves a logical set name into the service's CIK scope.
func (c *Config) SetConstituentSet(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		c.SetName = ""
		c.CIKs = nil

		return nil
	}

	if filepath.Base(name) != name || filepath.Ext(name) != "" {
		return errors.New("set must be a name, not a path")
	}

	set, err := constituents.LoadJSON(appdata.Path("sets", name+".json"))
	if err != nil {
		return err
	}

	c.SetName = name
	c.CIKs = set.CIKs()

	return nil
}

// Validate checks service configuration independently of a command parser.
func (c *Config) Validate() error {
	switch {
	case strings.TrimSpace(c.NATSURL) == "":
		return errors.New("NATS URL is required")
	case strings.TrimSpace(c.CommandAddress) == "":
		return errors.New("command address is required")
	case strings.TrimSpace(c.UserAgent) == "":
		return errors.New("SEC user agent is required")
	case strings.TrimSpace(c.HTTPAddress) == "":
		return errors.New("HTTP address is required")
	case c.Interval <= 0:
		return errors.New("poll interval must be positive")
	case c.MaxAttempts <= 0:
		return errors.New("maximum attempts must be positive")
	case c.FromYear < edgar.EarliestPipelineYear:
		return fmt.Errorf("from year must be %d or later",
			edgar.EarliestPipelineYear)
	case len(c.FormTypes) == 0:
		return errors.New("at least one supported form type is required")
	}

	for name, path := range map[string]string{
		"polling database":   c.PollingDB,
		"news database":      c.NewsDB,
		"filings directory":  c.FilingsDirectory,
		"parsed directory":   c.ParsedDirectory,
		"analysis database":  c.AnalysisDB,
		"ownership database": c.OwnershipDB,
		"master index":       c.MasterPath,
	} {
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("%s path is required", name)
		}
	}

	return nil
}

// Run starts every continuous role, the web application, and the local command
// transport in one foreground process.
func ensureParentDirectories(config Config) error {
	for _, path := range []string{
		config.PollingDB, config.NewsDB, config.AnalysisDB,
		config.OwnershipDB,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return fmt.Errorf("create directory for %s: %w", path, err)
		}
	}

	return nil
}

func environmentValue(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}

	return fallback
}

func environmentInt(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}

	return value, nil
}

func webAddressFromEnvironment() string {
	for _, name := range []string{
		"EDGAR_HTTP_ADDRESS", "EDGAR_FILINGS_ADDRESS", "EDGAR_NEWS_ADDRESS",
	} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value
		}
	}

	return "127.0.0.1:8083"
}
