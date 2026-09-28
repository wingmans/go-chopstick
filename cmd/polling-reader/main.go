package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"wingman.com/fetch-ecb/internal/ctxlog"
	"wingman.com/fetch-ecb/internal/edgar"
	pw "wingman.com/fetch-ecb/internal/pollingworker"
)

var quarterlyFilePattern = regexp.MustCompile(`^(\d{4})-QTR([1-4])\.tsv$`)

type config struct {
	indexesDir string
	dbPath     string
	file       string
	latest     bool
	year       int
	fromYear   int
	toYear     int
	dryRun     bool
}

func main() {
	logger := ctxlog.New()

	ctx := ctxlog.WithLogger(context.Background(), logger)
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	cfg, err := parseConfig(args)
	if err != nil {
		return err
	}

	logger := ctxlog.FromContext(ctx)

	files, err := selectFiles(cfg)
	if err != nil {
		return err
	}

	logger.Debug("selected quarterly index files", "files", files)

	events, err := readEvents(files)
	if err != nil {
		return err
	}

	logger.Info("read quarterly index events", "files", len(files), "events", len(events))

	if cfg.dryRun {
		logger.Info("dry run complete", "events", len(events))

		return nil
	}

	store, err := pw.NewDedupeServiceContext(ctx, cfg.dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	newEvents, err := store.SaveBatchContext(ctx, events)
	if err != nil {
		return fmt.Errorf("import quarterly indexes: %w", err)
	}

	logger.Info("imported quarterly index events", "new_events", len(newEvents),
		"duplicates", len(events)-len(newEvents))

	return nil
}

func parseConfig(args []string) (config, error) {
	flags := flag.NewFlagSet("polling-reader", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	cfg := config{}
	flags.StringVar(&cfg.indexesDir, "indexes-dir", "./data/indexes/quarterly", "quarterly index directory")
	flags.StringVar(&cfg.dbPath, "db-path", "./data/polling.db", "SQLite database path")
	flags.StringVar(&cfg.file, "file", "", "import one quarterly TSV file")
	flags.BoolVar(&cfg.latest, "latest", false, "import the newest local quarterly TSV")
	flags.IntVar(&cfg.year, "year", 0, "import all quarters for one year")
	flags.IntVar(&cfg.fromYear, "from-year", 0, "first year to import")
	flags.IntVar(&cfg.toYear, "to-year", 0, "last year to import")
	flags.BoolVar(&cfg.dryRun, "dry-run", false, "read and report without writing SQLite")

	if err := flags.Parse(args); err != nil {
		return config{}, err
	}

	if flags.NArg() != 0 {
		return config{}, errors.New("unexpected positional arguments")
	}

	if cfg.file != "" && (cfg.latest || cfg.year != 0 || cfg.fromYear != 0 || cfg.toYear != 0) {
		return config{}, errors.New("--file cannot be combined with --latest or year filters")
	}

	if cfg.latest && (cfg.year != 0 || cfg.fromYear != 0 || cfg.toYear != 0) {
		return config{}, errors.New("--latest cannot be combined with year filters")
	}

	if cfg.year != 0 && (cfg.fromYear != 0 || cfg.toYear != 0) {
		return config{}, errors.New("--year cannot be combined with --from-year or --to-year")
	}

	if cfg.fromYear != 0 && cfg.toYear != 0 && cfg.fromYear > cfg.toYear {
		return config{}, errors.New("--from-year must not be after --to-year")
	}

	if cfg.file == "" && !cfg.latest && cfg.year == 0 && cfg.fromYear == 0 && cfg.toYear == 0 {
		return config{}, errors.New("one of --file, --latest, --year, --from-year, or --to-year is required")
	}

	return cfg, nil
}

func selectFiles(cfg config) ([]string, error) {
	if cfg.file != "" {
		return []string{cfg.file}, nil
	}

	entries, err := os.ReadDir(cfg.indexesDir)
	if err != nil {
		return nil, fmt.Errorf("read indexes directory: %w", err)
	}

	var files []string

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		match := quarterlyFilePattern.FindStringSubmatch(entry.Name())
		if len(match) != 3 {
			continue
		}

		year, _ := strconv.Atoi(match[1])
		if cfg.year != 0 && year != cfg.year {
			continue
		}

		if cfg.fromYear != 0 && year < cfg.fromYear {
			continue
		}

		if cfg.toYear != 0 && year > cfg.toYear {
			continue
		}

		files = append(files, filepath.Join(cfg.indexesDir, entry.Name()))
	}

	sort.Strings(files)

	if cfg.latest {
		if len(files) == 0 {
			return nil, errors.New("no quarterly index files found")
		}

		return files[len(files)-1:], nil
	}

	if len(files) == 0 {
		return nil, errors.New("no quarterly index files matched")
	}

	return files, nil
}

func readEvents(files []string) ([]pw.FilingEvent, error) {
	var events []pw.FilingEvent

	for _, path := range files {
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", path, err)
		}

		reader := edgar.NewIndexReader(file, edgar.IndexFilter{})
		for {
			record, err := reader.Next()
			if errors.Is(err, io.EOF) {
				break
			}

			if err != nil {
				_ = file.Close()

				return nil, fmt.Errorf("read %s: %w", path, err)
			}

			events = append(events, pw.FilingEvent{
				AccessionNumber: accessionFromPath(record.FilingPath),
				CIK:             record.CIK,
				FormType:        record.FormType,
				FilingDate:      record.DateFiled.Format("2006-01-02"),
				SourceFeed:      "BATCH",
				Timestamp:       record.DateFiled,
			})
		}

		if err := file.Close(); err != nil {
			return nil, fmt.Errorf("close %s: %w", path, err)
		}
	}

	return events, nil
}

func accessionFromPath(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, "-index.htm")

	base = strings.TrimSuffix(base, ".txt")
	if len(base) == 20 && base[10] == '-' && base[13] == '-' {
		return base
	}

	if len(base) == 18 {
		return base[:10] + "-" + base[10:12] + "-" + base[12:]
	}

	return base
}
