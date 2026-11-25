package operator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

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

	if cfg.Verbose {
		fmt.Println("Infrastructure")
	}

	infrastructureErr := chopstick.CheckInfrastructure(ctx, cfg.Config)
	if infrastructureErr == nil {
		report.check("OK", "infrastructure", "SQLite, NATS, and JetStream are ready")
	} else {
		report.check("FAIL", "infrastructure", "see errors and hints below")
	}

	if !cfg.Verbose {
		checkConstituentSet(&report, cfg.SetName)
	}

	if _, err := os.Stat(cfg.MasterPath); err == nil {
		detail := cfg.MasterPath
		if cfg.Verbose {
			detail = "available"
		}

		report.check("OK", "master index", detail)
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
		printInfrastructureSettings(cfg)
		printStorageUsage(os.Stdout, cfg)

		fmt.Println()
		fmt.Println("Functional settings")
		checkConstituentSet(&report, cfg.SetName)
		printFunctionalSettings(cfg)
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

func checkConstituentSet(report *doctorReport, name string) {
	if name == "" {
		report.check("WARN", "constituent set", "not configured; historical backfill requires EDGAR_SET")

		return
	}

	report.check("OK", "constituent set", name)
}

func printInfrastructureSettings(cfg DoctorConfig) {
	fmt.Println()
	fmt.Printf("  NATS              %s\n", chopstick.NATSAddress(cfg.NATSURL))
	fmt.Printf("  web URL           %s (served by chopd)\n", httpURL(cfg.HTTPAddress))
	fmt.Printf("  log level         %s\n", environmentValue("LOGLEVEL", "info"))
}

func printFunctionalSettings(cfg DoctorConfig) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = sec.DefaultBaseURL
	}

	fmt.Printf("  from year         %d\n", cfg.FromYear)
	fmt.Printf("  form types        %s\n", strings.Join(cfg.FormTypes, ","))
	fmt.Printf("  parser supports   %s\n", strings.Join(sec.SupportedFormTypes(), ","))
	fmt.Printf("  SEC archives      %s\n", baseURL)
	fmt.Printf("  SEC user agent    %s\n", cfg.UserAgent)
}

func printStorageUsage(output io.Writer, cfg DoctorConfig) {
	type storagePath struct{ label, path string }

	entries := []storagePath{
		{"data directory", appdata.Root()},
		{"filings cache", cfg.FilingsDirectory},
		{"index cache", appdata.Path("cache", "indexes")},
		{"master index", cfg.MasterPath},
		{"parsed filings", cfg.ParsedDirectory},
		{"temp artifacts", appdata.Path("tmp", "artifacts")},
		{"constituent sets", appdata.Path("sets")},
		{"polling DB", cfg.PollingDB},
		{"news DB", cfg.NewsDB},
		{"analysis DB", cfg.AnalysisDB},
		{"ownership DB", cfg.OwnershipDB},
		{"NATS storage", appdata.Path("nats")},
		{"logs", appdata.Path("logs")},
		{"event log", appdata.Path("logs", "sec.log")},
	}

	if cfg.SetName != "" {
		entries = append(entries, storagePath{"selected manifest", appdata.Path("sets", cfg.SetName+".json")})
	}

	pathWidth := len("Path")
	for _, entry := range entries {
		pathWidth = max(pathWidth, utf8.RuneCountInString(entry.path))
	}

	const row = "  %-17s  %-*s  %11s\n"

	fmt.Fprintln(output)
	fmt.Fprintf(output, row, "Storage", pathWidth, "Path", "Size")

	for _, entry := range entries {
		fmt.Fprintf(output, row, entry.label, pathWidth, entry.path, storageSize(entry.path))
	}
}

func storageSize(path string) string {
	bytes, err := pathSize(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "not created"
	case err != nil:
		return "unavailable"
	default:
		return formatBytes(bytes)
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
