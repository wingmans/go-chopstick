package chopstick

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	filingstore "wingman.com/chopstick/internal/edgar/filing/store"
	"wingman.com/chopstick/internal/edgar/news"
	ownershipstore "wingman.com/chopstick/internal/edgar/ownership/store"
	"wingman.com/chopstick/internal/edgar/pipeline"
	"wingman.com/chopstick/internal/platform/appdata"
	"wingman.com/chopstick/internal/platform/xerr"
)

// Setup provisions the application databases and JetStream resources.
func Setup(ctx context.Context, config Config) error {
	if err := config.Validate(); err != nil {
		return ConfigurationError(err)
	}

	lock, err := appdata.AcquireLock(appdata.Path("chopd.lock"))
	if err != nil {
		return fmt.Errorf("setup requires chopd and backfill to be stopped: %w", err)
	}
	defer func() { _ = lock.Close() }()

	if err := initializeInfrastructure(ctx, config); err != nil {
		if errors.Is(err, pipeline.ErrIncompatibleResources) {
			report := infrastructureError(config, []string{err.Error()}, false, err)
			report.Title = "Chopstick setup failed"

			return report
		}

		return &xerr.PreflightError{
			Title: "Chopstick setup failed", Problems: []string{err.Error()},
			Hints: []string{"Inspect the configured dependencies:", "  " + doctorCommand(config)},
		}
	}

	return CheckInfrastructure(ctx, config)
}

func initializeInfrastructure(ctx context.Context, config Config) error {
	// Reset a stale news cursor before creating a replacement projection. If
	// setup stops between the two operations, a subsequent setup can retry.
	info, err := os.Stat(config.NewsDB)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect news database at %s: %w", config.NewsDB, err)
	}

	natsCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	if err := pipeline.Provision(natsCtx, config.NATSURL, info == nil || info.Size() == 0); err != nil {
		return fmt.Errorf("provision JetStream at %s: %w", NATSAddress(config.NATSURL), err)
	}

	if err := ensureParentDirectories(config); err != nil {
		return err
	}

	for _, directory := range []string{config.FilingsDirectory, config.ParsedDirectory} {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			return fmt.Errorf("create data directory %s: %w", directory, err)
		}
	}

	for _, database := range databases(config) {
		if database.initialize == nil {
			continue
		}

		if err := openAndClose(ctx, database.path, database.initialize); err != nil {
			return fmt.Errorf("initialize %s SQLite database at %s: %w",
				database.label, database.path, err)
		}
	}

	projection, _, err := news.Open(ctx, config.NewsDB)
	if err != nil {
		return fmt.Errorf("initialize news SQLite database at %s: %w", config.NewsDB, err)
	}

	return projection.Close()
}

// CheckInfrastructure verifies the local dependencies required for startup.
func CheckInfrastructure(ctx context.Context, config Config) error {
	if err := config.Validate(); err != nil {
		return ConfigurationError(err)
	}

	var problems []string

	missingStorage := false

	for _, database := range databases(config) {
		if err := openAndClose(ctx, database.path, database.open); err != nil {
			problems = append(problems, fmt.Sprintf("%s SQLite database at %s: %v",
				database.label, database.path, err))
			missingStorage = missingStorage || errors.Is(err, os.ErrNotExist)
		}
	}

	for _, directory := range []string{config.FilingsDirectory, config.ParsedDirectory} {
		info, err := os.Stat(directory)
		if err != nil {
			problems = append(problems, fmt.Sprintf("data directory %s: %v", directory, err))
			missingStorage = missingStorage || errors.Is(err, os.ErrNotExist)
		} else if !info.IsDir() {
			problems = append(problems, directory+" is not a data directory")
		}
	}

	natsErr := checkNATS(ctx, config.NATSURL)
	if natsErr != nil {
		problems = append(problems, natsErr.Error())
	}

	if len(problems) == 0 {
		return nil
	}

	return infrastructureError(config, problems, missingStorage, natsErr)
}

func infrastructureError(config Config, problems []string, needsSetup bool,
	natsErr error,
) *xerr.PreflightError {
	report := &xerr.PreflightError{
		Title: "Chopstick infrastructure check failed", Problems: problems,
	}

	localNATS := config.NATSURL == pipeline.DefaultURL
	customFlags := infrastructureFlags(config)
	missingResources := errors.Is(natsErr, nats.ErrStreamNotFound) ||
		errors.Is(natsErr, nats.ErrConsumerNotFound)
	incompatible := errors.Is(natsErr, pipeline.ErrIncompatibleResources)
	natsUnavailable := natsErr != nil && !missingResources && !incompatible
	needsSetup = needsSetup || missingResources

	if incompatible {
		if localNATS && customFlags == "" {
			report.Hints = append(report.Hints,
				"If the retained events are disposable, reset project-local NATS, then run setup:",
				"  make nats-stop && make nats-reset && make setup")
		} else {
			report.Hints = append(report.Hints, "Ask the NATS administrator to correct the stream or consumer settings.")
		}

		needsSetup = false
	}

	switch {
	case localNATS && customFlags == "" && (needsSetup || natsUnavailable):
		report.Hints = append(report.Hints,
			"Start local dependencies and provision missing resources:",
			"  make setup")
	default:
		if natsUnavailable {
			if localNATS {
				report.Hints = append(report.Hints, "Check local NATS:", "  make nats-status")
			} else {
				report.Hints = append(report.Hints, "Check the NATS server configured by EDGAR_NATS_URL or --nats-url.")
			}
		}

		if needsSetup {
			report.Hints = append(report.Hints,
				"Once the dependencies are available, provision the configured resources:",
				"  ./bin/chop setup"+customFlags)
		}
	}

	report.Hints = append(report.Hints, "Verify the configured dependencies:", "  "+doctorCommand(config))

	return report
}

func checkNATS(ctx context.Context, address string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	bus, err := pipeline.Inspect(ctx, address)
	if err != nil {
		return fmt.Errorf("NATS at %s: %w", NATSAddress(address), err)
	}
	defer bus.CloseImmediately()

	if err := bus.CheckResources(ctx); err != nil {
		return fmt.Errorf("JetStream at %s: %w", NATSAddress(address), err)
	}

	return nil
}

// ConfigurationError formats a configuration blocker for the operator.
func ConfigurationError(err error) error {
	return &xerr.PreflightError{
		Title: "Chopstick configuration is invalid", Problems: []string{err.Error()},
		Hints: []string{
			"Correct the setting shown above, then verify your configuration:",
			"  ./bin/chop doctor",
		},
	}
}

type database struct {
	label      string
	path       string
	open       func(context.Context, string) (io.Closer, error)
	initialize func(context.Context, string) (io.Closer, error)
}

func databases(config Config) []database {
	return []database{
		{"polling", config.PollingDB, opener(pipeline.OpenExisting), opener(pipeline.Open)},
		{"news", config.NewsDB, opener(news.OpenExisting), nil},
		{"analysis", config.AnalysisDB, opener(filingstore.OpenExisting), opener(filingstore.Open)},
		{"ownership", config.OwnershipDB, opener(ownershipstore.OpenExisting), opener(ownershipstore.Open)},
	}
}

func opener[T io.Closer](open func(context.Context, string) (T, error)) func(context.Context, string) (io.Closer, error) {
	return func(ctx context.Context, path string) (io.Closer, error) {
		return open(ctx, path)
	}
}

func openAndClose(ctx context.Context, path string, open func(context.Context, string) (io.Closer, error)) error {
	db, err := open(ctx, path)
	if err != nil {
		return err
	}

	return db.Close()
}

// NATSAddress displays the configured endpoints without credentials.
func NATSAddress(raw string) string {
	addresses := strings.Split(raw, ",")
	for index, address := range addresses {
		parsed, err := url.Parse(strings.TrimSpace(address))
		if err != nil || parsed.Host == "" {
			addresses[index] = "the configured EDGAR_NATS_URL"

			continue
		}

		addresses[index] = parsed.Scheme + "://" + parsed.Host
	}

	return strings.Join(addresses, ", ")
}

func infrastructureFlags(config Config) string {
	var flags strings.Builder

	if config.NATSURL != pipeline.DefaultURL && config.NATSURL != environmentValue("EDGAR_NATS_URL", pipeline.DefaultURL) {
		// Use an environment setting for URLs that can contain credentials.
		if NATSAddress(config.NATSURL) == config.NATSURL {
			flags.WriteString(" --nats-url " + xerr.ShellArg(config.NATSURL))
		}
	}

	for _, setting := range []struct{ name, path, defaultPath string }{
		{"polling-db", config.PollingDB, appdata.Path("polling.db")},
		{"news-db", config.NewsDB, appdata.Path("feed.db")},
		{"analysis-db", config.AnalysisDB, appdata.Path("analysis.db")},
		{"ownership-db", config.OwnershipDB, appdata.Path("ownership.db")},
		{"filings-dir", config.FilingsDirectory, appdata.Path("cache", "filings")},
		{"parsed-dir", config.ParsedDirectory, appdata.Path("parsed")},
	} {
		if setting.path != setting.defaultPath {
			flags.WriteString(" --" + setting.name + " " + xerr.ShellArg(setting.path))
		}
	}

	return flags.String()
}

func doctorCommand(config Config) string {
	return "./bin/chop --verbose doctor" + infrastructureFlags(config)
}
