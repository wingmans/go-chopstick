package operator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wingman.com/chopstick/internal/chopstick"
	"wingman.com/chopstick/internal/ingest/sec"
	"wingman.com/chopstick/internal/platform/appdata"
	"wingman.com/chopstick/internal/platform/outbound"
	"wingman.com/chopstick/internal/platform/xerr"
)

type doctorReport struct {
	ok       int
	warnings int
	failures int
}

// doctorReport represents the results of running the "doctor" subcommand,
// including counts of OK checks, warnings, and failures.
func (r *doctorReport) check(level, component, message string) {
	fmt.Printf("%-7s %-14s %s\n", "["+level+"]", component, message)

	switch level {
	case "OK":
		r.ok++
	case "WARN":
		r.warnings++
	case "FAIL":
		r.failures++
	}
}

func runDoctor(ctx context.Context, cfg DoctorConfig) error {
	fmt.Println("Chop doctor")
	fmt.Println()

	report := doctorReport{}

	infrastructureErr := chopstick.CheckInfrastructure(ctx, cfg.Config)
	if infrastructureErr == nil {
		report.check("OK", "infrastructure", "SQLite, NATS, and JetStream are ready")
	} else {
		report.check("FAIL", "infrastructure", "see errors and hints below")
	}

	if cfg.SetName == "" {
		report.check("WARN", "constituent set", "not configured; historical backfill requires EDGAR_SET")
	} else {
		report.check("OK", "constituent set", cfg.SetName)
	}

	if _, err := os.Stat(cfg.MasterPath); err == nil {
		report.check("OK", "master index", cfg.MasterPath)
	} else if errors.Is(err, os.ErrNotExist) {
		report.check("WARN", "master index", "missing; run './bin/chop index'")
	} else {
		report.check("WARN", "master index", err.Error())
	}

	if err := outbound.Check(ctx); err != nil {
		report.check("WARN", "gateway", err.Error())
	} else {
		report.check("OK", "gateway", "health and credentials verified")
	}

	checkUpstreams(ctx, &report, cfg.Upstreams, cfg.UserAgent)

	if cfg.Verbose {
		printResolvedConfiguration(cfg)
		printStorageUsage(appdata.Root())
	}

	fmt.Println()
	fmt.Printf("Result: %d failure(s), %d warning(s)\n", report.failures, report.warnings)

	if infrastructureErr != nil {
		return infrastructureErr
	}

	if report.failures > 0 {
		return &xerr.PreflightError{
			Title: "One or more upstream probes failed",
			Hints: []string{"Check the local gateway and its logs:", "  make gateway-status"},
		}
	}

	return ctx.Err()
}

func printResolvedConfiguration(cfg DoctorConfig) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = sec.DefaultBaseURL
	}

	fmt.Println()
	fmt.Println("Configuration")
	fmt.Printf("  set               %s\n", environmentValue(edgarSetEnv,
		"not configured"))
	fmt.Printf("  from year         %d\n", cfg.FromYear)
	fmt.Printf("  form types        %s\n", strings.Join(cfg.FormTypes, ","))
	fmt.Printf("  parser supports   %s\n", strings.Join(sec.SupportedFormTypes(), ","))
	fmt.Printf("  SEC archives      %s\n", baseURL)
	fmt.Printf("  SEC user agent    %s\n", cfg.UserAgent)
	fmt.Printf("  NATS              %s\n", chopstick.NATSAddress(cfg.NATSURL))
	fmt.Printf("  web URL           %s (served by chopd)\n",
		httpURL(cfg.HTTPAddress))
	fmt.Printf("  data directory    %s\n", appdata.Root())
	fmt.Printf("  filings cache     %s\n", cfg.FilingsDirectory)
	fmt.Printf("  parsed filings    %s\n", cfg.ParsedDirectory)
	fmt.Printf("  temp artifacts    %s\n", appdata.Path("tmp", "artifacts"))
	fmt.Printf("  constituent sets  %s\n", appdata.Path("sets"))

	setName := environmentValue(edgarSetEnv, "")
	if setName != "" {
		fmt.Printf("  selected set      %s\n", appdata.Path("sets", setName+".json"))
	}

	fmt.Printf("  polling DB        %s\n", cfg.PollingDB)
	fmt.Printf("  news DB           %s\n", cfg.NewsDB)
	fmt.Printf("  analysis DB       %s\n", cfg.AnalysisDB)
	fmt.Printf("  ownership DB      %s\n", cfg.OwnershipDB)
	fmt.Printf("  master index      %s\n", cfg.MasterPath)
	fmt.Printf("  event log         %s\n", appdata.Path("logs", "sec.log"))
	fmt.Printf("  log level         %s\n", environmentValue("LOGLEVEL", "info"))
}

func printStorageUsage(root string) {
	fmt.Println()
	fmt.Println("Storage used")

	for _, entry := range []struct {
		label string
		path  string
	}{
		{label: "total", path: root},
		{label: "filings cache", path: appdata.Path("cache", "filings")},
		{label: "index cache", path: appdata.Path("cache", "indexes")},
		{label: "parsed filings", path: appdata.Path("parsed")},
		{label: "temp artifacts", path: appdata.Path("tmp", "artifacts")},
		{label: "constituent sets", path: appdata.Path("sets")},
		{label: "polling DB", path: appdata.Path("polling.db")},
		{label: "news DB", path: appdata.Path("feed.db")},
		{label: "analysis DB", path: appdata.Path("analysis.db")},
		{label: "NATS", path: appdata.Path("nats")},
		{label: "logs", path: appdata.Path("logs")},
	} {
		bytes, err := pathSize(entry.path)
		if errors.Is(err, os.ErrNotExist) {
			fmt.Printf("  %-17s not created\n", entry.label)

			continue
		}

		if err != nil {
			fmt.Printf("  %-17s unavailable: %v\n", entry.label, err)

			continue
		}

		fmt.Printf("  %-17s %s (%d bytes)\n", entry.label,
			formatBytes(bytes), bytes)
	}
}

// pathSize calculates the total size of the given path in bytes. It walks through all files and sums their sizes.
func pathSize(path string) (int64, error) {
	var total int64

	err := filepath.WalkDir(path, func(current string, entry os.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}

		if entry.IsDir() {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect %s: %w", current, err)
		}

		total += info.Size()

		return nil
	})

	return total, err
}

// formatBytes converts a byte count into a human-readable string with appropriate units.
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}

	divisor := int64(unit)

	exponent := 0
	for value := bytes / unit; value >= unit && exponent < 4; value /= unit {
		divisor *= unit
		exponent++
	}

	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(divisor),
		"KMGTPE"[exponent])
}

func runStatus(ctx context.Context, cfg StatusConfig) error {
	status, err := Status(ctx, cfg.Address)
	if err != nil {
		return err
	}

	fmt.Println("Chop status")
	fmt.Println()
	fmt.Printf("  %-22s%s\n", "started", status.StartedAt.Format(time.RFC3339))
	fmt.Printf("  %-22s%s\n", "web URL", httpURL(status.HTTPAddress))
	fmt.Printf("  %-22s%d\n", "accessions", status.Pipeline.Accessions)
	fmt.Printf("  %-22s%d\n", "outbox pending", status.Pipeline.OutboxPending)
	fmt.Printf("  %-22s%d\n", "outbox published", status.Pipeline.OutboxPublished)
	fmt.Printf("  %-22s%d\n", "failures", status.Pipeline.Failures)
	fmt.Printf("  %-22s%d\n", "stream messages", status.StreamMessages)
	fmt.Printf("  %-22s%d\n", "stream bytes", status.StreamBytes)

	if status.IndexImport != nil {
		fmt.Printf("  %-22s%s (%s)\n", "index import",
			status.IndexImport.State, status.IndexImport.Phase)
		fmt.Printf("  %-22sfiles=%d examined=%d queued=%d duplicates=%d\n",
			"import progress",
			status.IndexImport.Files, status.IndexImport.Examined,
			status.IndexImport.Queued, status.IndexImport.Duplicates)
	}

	for _, consumer := range status.Consumers {
		fmt.Printf("  %-22sremaining=%d pending=%d in_flight=%d\n",
			consumer.Name, consumer.Pending+uint64(max(consumer.InFlight, 0)),
			consumer.Pending, consumer.InFlight)
	}

	return nil
}

func httpURL(address string) string {
	if strings.HasPrefix(address, "http://") ||
		strings.HasPrefix(address, "https://") {
		return address
	}

	return "http://" + address
}

// runListFailures retrieves unresolved work failures through chopd.
func runListFailures(ctx context.Context, cfg FailureListConfig) error {
	rows, err := ListFailures(ctx, cfg.Address, cfg.Stage, cfg.Limit)
	if err != nil {
		return err
	}

	if len(rows) == 0 {
		fmt.Println("No unresolved pipeline failures.")

		return nil
	}

	fmt.Printf("%-10s %-22s %-8s %-19s %s\n", "STAGE", "ACCESSION", "ATTEMPTS", "LAST FAILURE", "ERROR")

	for _, row := range rows {
		fmt.Printf("%-10s %-22s %-8d %-19s %s\n", row.Stage,
			row.AccessionNumber, row.Attempts,
			row.LastFailedAt.Format(time.RFC3339), row.LastError)
	}

	return nil
}

func runRetryFailures(ctx context.Context, cfg FailureRetryConfig) error {
	count, err := RetryFailures(ctx, cfg.Address, cfg.Stage)
	if err != nil {
		return err
	}

	fmt.Printf("Requeued %d %s failure(s)\n", count, cfg.Stage)

	return nil
}
