// Package ingest discovers filings from concrete external sources.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	pipeline "wingman.com/chopstick/internal/chopstick/pipeline"
	"wingman.com/chopstick/internal/edgar"
)

var quarterlyFilePattern = regexp.MustCompile(`^(\d{4})-QTR([1-4])\.tsv$`)

// Config selects local index files and their destination.
type Config struct {
	IndexesDirectory string
	MasterPath       string
	PollingDB        string
	NATSURL          string
	File             string
	Latest           bool
	Year             int
	FromYear         int
	ToYear           int
	DryRun           bool
	Progress         func(Progress)
}

// Progress is a coarse snapshot of one index import.
type Progress struct {
	Phase      string
	Files      int
	Examined   int
	Queued     int
	Duplicates int
}

// Validate checks that the file selection is unambiguous.
func (c *Config) Validate() error {
	if c.File != "" &&
		(c.Latest || c.Year != 0 || c.FromYear != 0 || c.ToYear != 0) {
		return errors.New(
			"file cannot be combined with latest or year filters",
		)
	}

	if c.Latest && (c.Year != 0 || c.FromYear != 0 || c.ToYear != 0) {
		return errors.New("latest cannot be combined with year filters")
	}

	if c.Year != 0 && (c.FromYear != 0 || c.ToYear != 0) {
		return errors.New("year cannot be combined with a year range")
	}

	if c.FromYear != 0 && c.ToYear != 0 && c.FromYear > c.ToYear {
		return errors.New("from year must not be after to year")
	}

	if c.File == "" && !c.Latest && c.Year == 0 && c.FromYear == 0 &&
		c.ToYear == 0 {
		return errors.New("an index file selection is required")
	}

	if strings.TrimSpace(c.PollingDB) == "" {
		return errors.New("polling database is required")
	}

	if strings.TrimSpace(c.MasterPath) == "" {
		return errors.New("master index path is required")
	}

	if strings.TrimSpace(c.NATSURL) == "" {
		return errors.New("NATS URL is required")
	}

	return nil
}

// Run imports selected local indexes and publishes their pending events.
// RunIndex imports selected local EDGAR indexes into the event pipeline.
func RunIndex(ctx context.Context, config Config, logger *slog.Logger) error {
	if err := config.Validate(); err != nil {
		return err
	}

	files, err := selectFiles(config)
	if err != nil {
		return err
	}

	config.report(Progress{Phase: "selected", Files: len(files)})

	if err := ensureMasterIndex(ctx, config, logger); err != nil {
		return err
	}

	events, err := readEvents(ctx, files)
	if err != nil {
		return err
	}

	logger.Info("read quarterly index events",
		"files", len(files), "events", len(events))
	config.report(Progress{
		Phase: "read", Files: len(files), Examined: len(events),
	})

	if config.DryRun {
		config.report(Progress{
			Phase: "dry-run", Files: len(files), Examined: len(events),
		})

		return nil
	}

	store, err := pipeline.Open(ctx, config.PollingDB)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	newEvents, err := store.SaveBatchContext(ctx, events)
	if err != nil {
		return fmt.Errorf("import quarterly indexes: %w", err)
	}

	logger.Info("imported quarterly index events",
		"new_events", len(newEvents),
		"duplicates", len(events)-len(newEvents))
	progress := Progress{
		Phase: "queued", Files: len(files), Examined: len(events),
		Queued: len(newEvents), Duplicates: len(events) - len(newEvents),
	}
	config.report(progress)

	if err := recordImport(config.MasterPath, files, ImportSummary{
		CommittedAt: time.Now().UTC(), Examined: progress.Examined,
		Queued: progress.Queued, Duplicates: progress.Duplicates,
	}); err != nil {
		return fmt.Errorf("record index import: %w", err)
	}

	bus, err := pipeline.Connect(config.NATSURL)
	if err != nil {
		return err
	}
	defer func() { _ = bus.Close() }()

	if err := store.PublishPending(ctx, bus); err != nil {
		return err
	}

	config.report(Progress{
		Phase: "published", Files: len(files), Examined: len(events),
		Queued: len(newEvents), Duplicates: len(events) - len(newEvents),
	})

	return nil
}

func (c *Config) report(progress Progress) {
	if c.Progress != nil {
		c.Progress(progress)
	}
}

func ensureMasterIndex(ctx context.Context, config Config,
	logger *slog.Logger,
) error {
	paths, err := localQuarterlyFiles(config.IndexesDirectory)
	if err != nil {
		if config.File != "" && errors.Is(err, os.ErrNotExist) {
			return nil
		}

		return err
	}

	if len(paths) == 0 {
		return nil
	}

	stale, err := masterNeedsStitch(config.MasterPath, paths)
	if err != nil {
		return err
	}

	if !stale {
		return nil
	}

	config.report(Progress{Phase: "stitching", Files: len(paths)})

	if config.DryRun {
		logger.Info("master index needs stitching",
			"path", config.MasterPath, "quarterly_files", len(paths),
			"dry_run", true)

		return nil
	}

	logger.Info("stitching master index",
		"path", config.MasterPath, "quarterly_files", len(paths))

	if err := edgar.StitchToContext(ctx, config.IndexesDirectory,
		config.MasterPath); err != nil {
		return fmt.Errorf("stitch master index: %w", err)
	}

	return nil
}

func masterNeedsStitch(masterPath string, quarterly []string) (bool, error) {
	master, err := os.Stat(masterPath)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}

	if err != nil {
		return false, fmt.Errorf("inspect master index: %w", err)
	}

	for _, path := range quarterly {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return false, fmt.Errorf("inspect quarterly index %s: %w",
				path, statErr)
		}

		if info.ModTime().After(master.ModTime()) {
			return true, nil
		}
	}

	return false, nil
}

func selectFiles(config Config) ([]string, error) {
	if config.File != "" {
		return []string{config.File}, nil
	}

	localFiles, err := localQuarterlyFiles(config.IndexesDirectory)
	if err != nil {
		return nil, err
	}

	files := make([]string, 0, len(localFiles))
	for _, path := range localFiles {
		match := quarterlyFilePattern.FindStringSubmatch(filepath.Base(path))

		year, _ := strconv.Atoi(match[1])
		if (config.Year != 0 && year != config.Year) ||
			(config.FromYear != 0 && year < config.FromYear) ||
			(config.ToYear != 0 && year > config.ToYear) {
			continue
		}

		files = append(files, path)
	}

	sort.Strings(files)

	if len(files) == 0 {
		return nil, errors.New("no quarterly index files matched")
	}

	if config.Latest {
		return files[len(files)-1:], nil
	}

	return files, nil
}

func localQuarterlyFiles(directory string) ([]string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read indexes directory: %w", err)
	}

	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() ||
			!quarterlyFilePattern.MatchString(entry.Name()) {
			continue
		}

		files = append(files, filepath.Join(directory, entry.Name()))
	}

	sort.Strings(files)

	return files, nil
}

func readEvents(ctx context.Context,
	files []string,
) ([]pipeline.Event, error) {
	events := make([]pipeline.Event, 0)

	for _, path := range files {
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", path, err)
		}

		reader := edgar.NewIndexReader(file, edgar.IndexFilter{})

		for {
			if err := ctx.Err(); err != nil {
				_ = file.Close()

				return nil, err
			}

			record, err := reader.Next()
			if errors.Is(err, io.EOF) {
				break
			}

			if err != nil {
				_ = file.Close()

				return nil, fmt.Errorf("read %s: %w", path, err)
			}

			if record.DateFiled.Year() < edgar.EarliestPipelineYear {
				continue
			}

			accession := accessionFromPath(record.FilingPath)
			events = append(events, pipeline.Event{
				SchemaVersion: 1, EventID: "filing.discovered:" + accession,
				AccessionNumber: accession, CIK: record.CIK,
				CompanyName: record.CompanyName, FormType: record.FormType,
				FilingDate: record.DateFiled.Format("2006-01-02"),
				FilingPath: record.FilingPath,
				FilingURL: "https://www.sec.gov/Archives/" +
					strings.TrimPrefix(record.FilingPath, "/"),
				SourceFeed: "BATCH", Timestamp: time.Now().UTC(),
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
