package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/nats-io/nats.go"

	"wingman.com/fetch-ecb/internal/appdata"
	"wingman.com/fetch-ecb/internal/constituents"
	"wingman.com/fetch-ecb/internal/edgar"
	"wingman.com/fetch-ecb/internal/eventbus"
	"wingman.com/fetch-ecb/internal/eventworker"
	"wingman.com/fetch-ecb/internal/news"
	"wingman.com/fetch-ecb/internal/pollingworker"
)

// operationsConfig holds the configuration for the operations subcommands.
// It includes settings for NATS, databases, file paths, HTTP addresses, and command-specific options.
type operationsConfig struct {
	natsURL        string
	pollingDB      string
	newsDB         string
	masterPath     string
	newsAddress    string
	filingsAddress string
	stage          string
	limit          int
	verbose        bool
}

// parseOperationsConfig parses the command-line arguments and environment variables
// for the operations subcommands, returning an appConfig with the parsed settings.
func parseOperationsConfig(command string, args []string) (appConfig, error) {
	cfg := appConfig{
		command: command,
		operations: operationsConfig{
			natsURL:        environmentValue(edgarNATSURLEnv, eventbus.DefaultURL),
			pollingDB:      appdata.Path("polling.db"),
			newsDB:         appdata.Path("feed.db"),
			masterPath:     appdata.Path("cache", "indexes", "master.tsv"),
			newsAddress:    environmentValue(edgarNewsAddressEnv, "127.0.0.1:8081"),
			filingsAddress: environmentValue(edgarWebAddressEnv, "127.0.0.1:8083"),
			limit:          50,
		},
	}

	flags := flag.NewFlagSet(command, flag.ContinueOnError)
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
		flags.StringVar(&cfg.operations.newsAddress, "address", cfg.operations.newsAddress, "news HTTP listen address")

		options = append(options,
			helpOption{"--news-db <path>", "Disposable news database."},
			helpOption{"--address <host:port>", "News HTTP address."},
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
		printSubcommandHelp(command, operationsDescription(command), options)
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
		report.check("FAIL", "NATS", "unavailable; run make nats-start")

		if cfg.verbose {
			fmt.Printf("       detail         %v\n", err)
		}
	} else {
		defer func() { _ = bus.Close() }()

		report.check("OK", "NATS", "connected to "+cfg.natsURL)

		if _, err := bus.JetStream().StreamInfo(eventbus.StreamName); err != nil {
			if errors.Is(err, nats.ErrStreamNotFound) {
				report.check("WARN", "JetStream",
					"EDGAR_EVENTS will be created by make dev")
			} else {
				report.check("FAIL", "JetStream", err.Error())
			}
		} else {
			report.check("OK", "JetStream", "EDGAR_EVENTS is available")
			checkConsumer(&report, bus, eventworker.DownloadDurable)
			checkConsumer(&report, bus, eventworker.ProcessDurable)
			checkConsumer(&report, bus, eventworker.SelectionDurable)
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
				path+" does not exist; run make dev")
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
				"missing table "+table+"; run make dev")

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
			durable+" has not been created; run make dev")
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
	fromYear, _ := environmentFromYear(edgar.EarliestPipelineYear)
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
	fmt.Printf("  news URL          %s (started by make dev)\n", httpURL(cfg.newsAddress))
	fmt.Printf("  filings web URL   %s (started by make dev)\n", httpURL(cfg.filingsAddress))
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
	fmt.Println("EDGAR status")
	fmt.Println()

	printDatabaseStatus(ctx, cfg.pollingDB)
	printNATSStatus(cfg.natsURL)
	printViewerStatus(ctx, cfg.newsDB, cfg.newsAddress)
	printHTTPStatus("Filings web", cfg.filingsAddress)

	return ctx.Err()
}

func printDatabaseStatus(ctx context.Context, path string) {
	fmt.Println("Pipeline database")

	db, err := openSQLiteReadOnly(ctx, path)
	if err != nil {
		fmt.Printf("  state             unavailable: %v\n\n", err)

		return
	}

	defer func() { _ = db.Close() }()

	printCount(ctx, db, "accessions", `SELECT COUNT(*) FROM processed_accessions`)
	printCount(ctx, db, "outbox pending", `SELECT COUNT(*) FROM polling_outbox
		WHERE published_at IS NULL`)
	printCount(ctx, db, "outbox published", `SELECT COUNT(*) FROM polling_outbox
		WHERE published_at IS NOT NULL`)

	if sqliteTableExists(ctx, db, "work_failures") {
		printCount(ctx, db, "failures", `SELECT COUNT(*) FROM work_failures
			WHERE resolved_at IS NULL`)
	} else {
		fmt.Println("  failures          not initialized")
	}

	fmt.Println()
}

func printCount(ctx context.Context, db *sql.DB, label, query string) {
	var count int64
	if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		fmt.Printf("  %-17s unavailable: %v\n", label, err)

		return
	}

	fmt.Printf("  %-17s %d\n", label, count)
}

func sqliteTableExists(ctx context.Context, db *sql.DB, table string) bool {
	var found int

	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name = ?`, table).Scan(&found)

	return err == nil && found > 0
}

// printNATSStatus prints the status of the NATS server, including stream and consumer information.
func printNATSStatus(url string) {
	fmt.Println("NATS")

	bus, err := eventbus.Inspect(url)
	if err != nil {
		fmt.Printf("  server            unavailable: %v\n\n", err)

		return
	}

	defer func() { _ = bus.Close() }()

	fmt.Println("  server            running at " + url)

	stream, err := bus.JetStream().StreamInfo(eventbus.StreamName)
	if err != nil {
		fmt.Printf("  stream            unavailable: %v\n\n", err)

		return
	}

	fmt.Printf("  stream messages   %d\n", stream.State.Msgs)
	fmt.Printf("  stream bytes      %d\n", stream.State.Bytes)

	for _, durable := range []string{
		eventworker.SelectionDurable,
		eventworker.DownloadDurable,
		eventworker.ProcessDurable,
		news.DurableName,
	} {
		consumer, err := bus.JetStream().ConsumerInfo(eventbus.StreamName,
			durable)
		if errors.Is(err, nats.ErrConsumerNotFound) {
			fmt.Printf("  %-17s not created\n", durable)

			continue
		}

		if err != nil {
			fmt.Printf("  %-17s unavailable: %v\n", durable, err)

			continue
		}

		ackPending := uint64(0)
		if consumer.NumAckPending > 0 {
			ackPending = uint64(consumer.NumAckPending)
		}

		remaining := consumer.NumPending + ackPending
		fmt.Printf("  %-17s state=%s remaining=%d pending=%d in_flight=%d\n",
			durable, consumerActivity(consumer), remaining,
			consumer.NumPending, consumer.NumAckPending)
		fmt.Printf("  %-17s redelivered=%d caught_up=%t\n", "",
			consumer.NumRedelivered, remaining == 0)

		if consumer.Delivered.Last != nil {
			fmt.Printf("  %-17s last activity %s\n", "",
				consumer.Delivered.Last.UTC().Format(time.RFC3339))
		}
	}

	fmt.Println()
}

// consumerActivity determines the current activity state of a NATS consumer based on its pending and waiting messages.
func consumerActivity(info *nats.ConsumerInfo) string {
	if info.NumAckPending > 0 {
		return "working"
	}

	if info.NumWaiting > 0 {
		return "waiting"
	}

	return "idle-or-stopped"
}

// printViewerStatus prints the status of the viewer server and its associated SQLite database.
func printViewerStatus(ctx context.Context, path, address string) {
	fmt.Println("News")

	dialer := net.Dialer{Timeout: 250 * time.Millisecond}

	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err == nil {
		_ = connection.Close()

		fmt.Println("  URL               " + httpURL(address) + " (listening)")
	} else {
		fmt.Println("  URL               " + httpURL(address) + " (stopped)")
	}

	db, err := openSQLiteReadOnly(ctx, path)
	if err != nil {
		fmt.Printf("  projection        unavailable: %v\n", err)

		return
	}
	defer func() { _ = db.Close() }()

	printCount(ctx, db, "projection rows", `SELECT COUNT(*) FROM feed_items`)
	fmt.Println()
}

// printHTTPStatus prints the status of an HTTP service by attempting to connect to its address.
func printHTTPStatus(name, address string) {
	fmt.Println(name)

	dialer := net.Dialer{Timeout: 250 * time.Millisecond}

	connection, err := dialer.Dial("tcp", address)
	if err == nil {
		_ = connection.Close()

		fmt.Println("  URL               " + httpURL(address) + " (listening)")
	} else {
		fmt.Println("  URL               " + httpURL(address) + " (stopped)")
	}

	fmt.Println()
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
	db, err := openSQLiteReadOnly(ctx, cfg.pollingDB)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	if !sqliteTableExists(ctx, db, "work_failures") {
		fmt.Println("No pipeline failures have been recorded.")

		return nil
	}

	query := `SELECT stage, accession_number, attempts, last_failed_at,
		last_error FROM work_failures WHERE resolved_at IS NULL`
	args := []any{}

	if cfg.stage != "" {
		query += " AND stage = ?"

		args = append(args, cfg.stage)
	}

	query += " ORDER BY last_failed_at DESC LIMIT ?"

	args = append(args, cfg.limit)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("read work failures: %w", err)
	}
	defer func() { _ = rows.Close() }()

	fmt.Printf("%-10s %-22s %-8s %-19s %s\n", "STAGE", "ACCESSION", "ATTEMPTS", "LAST FAILURE", "ERROR")

	for rows.Next() {
		var (
			stage, accession, failedAt, message string
			attempts                            int
		)

		if err := rows.Scan(&stage, &accession, &attempts, &failedAt,
			&message); err != nil {
			return err
		}

		fmt.Printf("%-10s %-22s %-8d %-19s %s\n", stage, accession,
			attempts, failedAt, message)
	}

	return rows.Err()
}

func runRetry(ctx context.Context, cfg operationsConfig) error {
	store, err := pollingworker.NewDedupeServiceContext(ctx, cfg.pollingDB)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	bus, err := eventbus.Connect(cfg.natsURL)
	if err != nil {
		return err
	}
	defer func() { _ = bus.Close() }()

	count, err := store.RetryWorkFailures(ctx, cfg.stage, bus)
	if err != nil {
		return err
	}

	fmt.Printf("Requeued %d %s failure(s)\n", count, cfg.stage)

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
