package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/nats-io/nats.go"

	"wingman.com/chopstick/internal/appdata"
	"wingman.com/chopstick/internal/constituents"
	"wingman.com/chopstick/internal/control"
	"wingman.com/chopstick/internal/edgar"
	"wingman.com/chopstick/internal/eventbus"
	"wingman.com/chopstick/internal/eventworker"
	"wingman.com/chopstick/internal/news"
)

// operationsConfig holds the configuration for the operations subcommands.
// It includes settings for NATS, databases, file paths, HTTP addresses, and command-specific options.
type operationsConfig struct {
	natsURL     string
	pollingDB   string
	newsDB      string
	masterPath  string
	httpAddress string
	stage       string
	limit       int
	verbose     bool
}

// parseOperationsConfig parses the command-line arguments and environment variables
// for the operations subcommands, returning an appConfig with the parsed settings.
func parseOperationsConfig(command string, args []string) (appConfig, error) {
	displayCommand := command
	switch command {
	case "errors":
		displayCommand = "failures list"
	case "retry":
		displayCommand = "failures retry"
	}

	cfg := appConfig{
		command: command,
		operations: operationsConfig{
			natsURL:     environmentValue(edgarNATSURLEnv, eventbus.DefaultURL),
			pollingDB:   appdata.Path("polling.db"),
			newsDB:      appdata.Path("feed.db"),
			masterPath:  appdata.Path("cache", "indexes", "master.tsv"),
			httpAddress: webAddressFromEnvironment(),
			limit:       50,
		},
	}

	flags := flag.NewFlagSet(displayCommand, flag.ContinueOnError)
	flags.SetOutput(os.Stdout)
	flags.StringVar(&cfg.operations.natsURL, "nats-url", cfg.operations.natsURL, "NATS server URL")
	flags.StringVar(&cfg.operations.pollingDB, "polling-db", cfg.operations.pollingDB, "pipeline state database")

	options := []helpOption{
		{"--nats-url <url>", "NATS server; default nats://127.0.0.1:4222."},
		{"--polling-db <path>", "Pipeline state database."},
	}

	switch command {
	case "doctor":
		flags.StringVar(&cfg.operations.newsDB, "news-db", cfg.operations.newsDB, "disposable news database")
		flags.StringVar(&cfg.operations.masterPath, "master", cfg.operations.masterPath, "stitched master index")
		flags.BoolVar(&cfg.operations.verbose, "verbose", false, "show resolved configuration and details")

		options = append(options,
			helpOption{"--news-db <path>", "Disposable news database."},
			helpOption{"--master <path>", "Stitched master index."},
			helpOption{"--verbose", "Show configuration and check details."},
		)
	case "status":
		flags.StringVar(&cfg.operations.newsDB, "news-db", cfg.operations.newsDB, "disposable news database")
		flags.StringVar(&cfg.operations.httpAddress, "address",
			cfg.operations.httpAddress, "application HTTP listen address")

		options = append(options,
			helpOption{"--news-db <path>", "Disposable news database."},
			helpOption{"--address <host:port>", "Application HTTP address."},
		)
	case "errors":
		flags.StringVar(&cfg.operations.stage, "stage", "", "limit failures to download or process")
		flags.IntVar(&cfg.operations.limit, "limit", cfg.operations.limit, "maximum failures to show")

		options = append(options,
			helpOption{"--stage <stage>", "Limit failures to download or process."},
			helpOption{"--limit <count>", "Maximum failures to show; default 50."},
		)
	case "retry":
		flags.StringVar(&cfg.operations.stage, "stage", "", "failure stage to retry")

		options = append(options,
			helpOption{"--stage <stage>", "Required: download or process."},
		)
	}

	flags.Usage = func() {
		printSubcommandHelp(displayCommand, operationsDescription(command),
			options)
	}
	if err := flags.Parse(args); err != nil {
		return appConfig{}, flag.ErrHelp
	}

	if flags.NArg() != 0 {
		return subcommandError(flags, errors.New("unexpected positional argument"))
	}

	if cfg.operations.limit <= 0 {
		return subcommandError(flags, errors.New("--limit must be positive"))
	}

	if cfg.operations.stage != "" && cfg.operations.stage != "download" &&
		cfg.operations.stage != "process" {
		return subcommandError(flags,
			errors.New("--stage must be download or process"))
	}

	if command == "retry" && cfg.operations.stage == "" {
		return subcommandError(flags, errors.New("--stage is required"))
	}

	return cfg, nil
}

func operationsDescription(command string) string {
	return map[string]string{
		"doctor": "Validate configuration and dependencies without changing them.",
		"status": "Show current outbox, stream, consumer, failure, and viewer state.",
		"errors": "List unresolved download and processing failures.",
		"retry":  "Requeue unresolved failures for one stage.",
	}[command]
}

func configuredFormTypes() ([]string, error) {
	value := environmentValue(edgarFormTypesEnv, "10-K,10-Q,8-K")

	return edgar.ValidateSupportedFormTypes(strings.Split(value, ","))
}

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

func runDoctor(ctx context.Context, cfg operationsConfig) error {
	fmt.Println("EDGAR doctor")
	fmt.Println()

	report := doctorReport{}
	if strings.TrimSpace(environmentValue(edgarUserAgentEnv,
		defaultUserAgent)) == "" {
		report.check("FAIL", "configuration", "SEC user agent is empty")
	} else {
		report.check("OK", "configuration", "SEC user agent configured")
	}

	checkConfiguredSet(&report)

	if forms, err := configuredFormTypes(); err != nil {
		report.check("FAIL", "form types", err.Error())
	} else {
		report.check("OK", "form types", strings.Join(forms, ","))
	}

	if _, err := os.Stat(cfg.masterPath); err == nil {
		report.check("OK", "master index", cfg.masterPath)
	} else if errors.Is(err, os.ErrNotExist) {
		report.check("WARN", "master index", "missing; run 'edgar index'")
	} else {
		report.check("FAIL", "master index", err.Error())
	}

	checkSQLite(ctx, &report, cfg.pollingDB, "polling DB", []string{"processed_accessions", "polling_outbox"})
	checkOptionalSQLiteTable(ctx, &report, cfg.pollingDB, "work_failures")
	checkSQLite(ctx, &report, cfg.newsDB, "viewer DB", []string{"feed_items"})

	bus, err := eventbus.Inspect(cfg.natsURL)
	if err != nil {
		report.check("FAIL", "NATS", "unavailable; start local NATS")

		if cfg.verbose {
			fmt.Printf("       detail         %v\n", err)
		}
	} else {
		defer func() { _ = bus.Close() }()

		report.check("OK", "NATS", "connected to "+cfg.natsURL)

		if _, err := bus.JetStream().StreamInfo(eventbus.StreamName); err != nil {
			if errors.Is(err, nats.ErrStreamNotFound) {
				report.check("WARN", "JetStream",
					"EDGAR_EVENTS will be created by edgard")
			} else {
				report.check("FAIL", "JetStream", err.Error())
			}
		} else {
			report.check("OK", "JetStream", "EDGAR_EVENTS is available")
			checkConsumer(&report, bus, eventworker.DownloadDurable)
			checkConsumer(&report, bus, eventworker.ProcessDurable)
			checkConsumer(&report, bus, eventworker.SelectionDurable)
			checkConsumer(&report, bus, eventworker.OwnershipDurable)
			checkConsumer(&report, bus, news.DurableName)
		}
	}

	if cfg.verbose {
		printResolvedConfiguration(cfg)
		printStorageUsage(appdata.Root())
	}

	fmt.Println()
	fmt.Printf("Result: %d failure(s), %d warning(s)\n", report.failures, report.warnings)

	if report.failures > 0 {
		return errors.New("doctor found failed checks")
	}

	return ctx.Err()
}

// checkSQLite checks the existence and schema of a SQLite database, reporting results to the doctorReport.
func checkSQLite(ctx context.Context, report *doctorReport,
	path, component string,
	tables []string,
) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			report.check("WARN", component,
				path+" does not exist; start edgard")
		} else {
			report.check("FAIL", component, err.Error())
		}

		return
	}

	db, err := openSQLiteReadOnly(ctx, path)
	if err != nil {
		report.check("FAIL", component, err.Error())

		return
	}
	defer func() { _ = db.Close() }()

	for _, table := range tables {
		var found int

		err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master
			WHERE type = 'table' AND name = ?`, table).Scan(&found)
		if err != nil || found == 0 {
			report.check("FAIL", component,
				"missing table "+table+"; start edgard")

			return
		}
	}

	report.check("OK", component, path)
}

// checkOptionalSQLiteTable checks for the existence of an optional table in a SQLite database,
// reporting a warning to the doctorReport if the table is missing.
func checkOptionalSQLiteTable(ctx context.Context, report *doctorReport,
	path, table string,
) {
	if _, err := os.Stat(path); err != nil {
		return
	}

	db, err := openSQLiteReadOnly(ctx, path)
	if err != nil {
		return
	}
	defer func() { _ = db.Close() }()

	var found int

	err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name = ?`, table).Scan(&found)
	if err == nil && found == 0 {
		report.check("WARN", "schema",
			table+" will be created by the next worker start")
	}
}

func checkConsumer(report *doctorReport, bus *eventbus.NATS, durable string) {
	_, err := bus.JetStream().ConsumerInfo(eventbus.StreamName, durable)

	switch {
	case errors.Is(err, nats.ErrConsumerNotFound):
		report.check("WARN", "consumer",
			durable+" has not been created; start edgard")
	case err != nil:
		report.check("FAIL", "consumer", durable+": "+err.Error())
	default:
		report.check("OK", "consumer", durable)
	}
}

func checkConfiguredSet(report *doctorReport) {
	setName := environmentValue(edgarSetEnv, "")
	if setName == "" {
		report.check("WARN", "constituent set",
			"not configured; historical backfill requires EDGAR_SET")

		return
	}

	path := appdata.Path("sets", setName+".json")

	set, err := constituents.LoadJSON(path)
	if err != nil {
		report.check("FAIL", "constituent set", err.Error())

		return
	}

	report.check("OK", "constituent set", fmt.Sprintf(
		"%s (%d members)", path, len(set.Members)))
}

func printResolvedConfiguration(cfg operationsConfig) {
	fromYear, _ := environmentFromYear()
	baseURL := environmentValue(edgarBaseURLEnv, edgar.DefaultBaseURL)
	forms, _ := configuredFormTypes()

	fmt.Println()
	fmt.Println("Configuration")
	fmt.Printf("  set               %s\n", environmentValue(edgarSetEnv,
		"not configured"))
	fmt.Printf("  from year         %d\n", fromYear)
	fmt.Printf("  form types        %s\n", strings.Join(forms, ","))
	fmt.Printf("  parser supports   %s\n", strings.Join(edgar.SupportedFormTypes(), ","))
	fmt.Printf("  SEC archives      %s\n", baseURL)
	fmt.Printf("  SEC user agent    %s\n", environmentValue(edgarUserAgentEnv, defaultUserAgent))
	fmt.Printf("  NATS              %s\n", cfg.natsURL)
	fmt.Printf("  web URL           %s (served by edgard)\n",
		httpURL(cfg.httpAddress))
	fmt.Printf("  data directory    %s\n", appdata.Root())
	fmt.Printf("  filings cache     %s\n", appdata.Path("cache", "filings"))
	fmt.Printf("  parsed filings    %s\n", appdata.Path("parsed"))
	fmt.Printf("  temp artifacts    %s\n", appdata.Path("tmp", "artifacts"))
	fmt.Printf("  constituent sets  %s\n", appdata.Path("sets"))

	setName := environmentValue(edgarSetEnv, "")
	if setName != "" {
		fmt.Printf("  selected set      %s\n", appdata.Path("sets", setName+".json"))
	}

	fmt.Printf("  polling DB        %s\n", cfg.pollingDB)
	fmt.Printf("  news DB           %s\n", cfg.newsDB)
	fmt.Printf("  analysis DB       %s\n", appdata.Path("analysis.db"))
	fmt.Printf("  master index      %s\n", cfg.masterPath)
	fmt.Printf("  event log         %s\n", appdata.Path("logs", "edgar.log"))
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

func runStatus(ctx context.Context, cfg operationsConfig) error {
	client, err := control.NewClient(cfg.natsURL)
	if err != nil {
		return err
	}
	defer client.Close()

	status, err := client.Status(ctx)
	if err != nil {
		return err
	}

	fmt.Println("EDGAR status")
	fmt.Println()
	fmt.Printf("  started           %s\n", status.StartedAt.Format(time.RFC3339))
	fmt.Printf("  web URL           %s\n", httpURL(status.HTTPAddress))
	fmt.Printf("  accessions        %d\n", status.Pipeline.Accessions)
	fmt.Printf("  outbox pending    %d\n", status.Pipeline.OutboxPending)
	fmt.Printf("  outbox published  %d\n", status.Pipeline.OutboxPublished)
	fmt.Printf("  failures          %d\n", status.Pipeline.Failures)
	fmt.Printf("  stream messages   %d\n", status.StreamMessages)
	fmt.Printf("  stream bytes      %d\n", status.StreamBytes)

	for _, consumer := range status.Consumers {
		fmt.Printf("  %-17s remaining=%d pending=%d in_flight=%d\n",
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

// runErrors retrieves and displays unresovled work failures from the polling database.
func runErrors(ctx context.Context, cfg operationsConfig) error {
	client, err := control.NewClient(cfg.natsURL)
	if err != nil {
		return err
	}
	defer client.Close()

	rows, err := client.Failures(ctx, control.FailureRequest{
		Stage: cfg.stage, Limit: cfg.limit,
	})
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

func runRetry(ctx context.Context, cfg operationsConfig) error {
	client, err := control.NewClient(cfg.natsURL)
	if err != nil {
		return err
	}
	defer client.Close()

	result, err := client.RetryFailures(ctx, control.RetryRequest{
		Stage: cfg.stage,
	})
	if err != nil {
		return err
	}

	fmt.Printf("Requeued %d %s failure(s)\n", result.Count, cfg.stage)

	return nil
}

// openSQLiteReadOnly opens a SQLite database in read-only mode, returning an error if the database cannot be accessed.
// It ensures that the database file exists and that a connection can be successfully established.
func openSQLiteReadOnly(ctx context.Context, path string) (*sql.DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}

	dsn := "file:" + filepath.ToSlash(path) + "?mode=ro&_busy_timeout=5000"

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("ping database: %w", err)
	}

	return db, nil
}
