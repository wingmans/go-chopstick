// Package indeximport imports local quarterly EDGAR indexes into the event
// pipeline.
package indeximport

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

	"wingman.com/chopstick/internal/appdata"
	"wingman.com/chopstick/internal/applock"
	"wingman.com/chopstick/internal/edgar"
	"wingman.com/chopstick/internal/eventbus"
	"wingman.com/chopstick/internal/pollingworker"
)

var quarterlyFilePattern = regexp.MustCompile(`^(\d{4})-QTR([1-4])\.tsv$`)

// Config selects local index files and their destination.
type Config struct {
	IndexesDirectory string
	PollingDB        string
	NATSURL          string
	File             string
	Latest           bool
	Year             int
	FromYear         int
	ToYear           int
	DryRun           bool
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

	if strings.TrimSpace(c.NATSURL) == "" {
		return errors.New("NATS URL is required")
	}

	return nil
}

// Run imports selected local indexes and publishes their pending events.
func Run(ctx context.Context, config Config, logger *slog.Logger) error {
	if err := config.Validate(); err != nil {
		return err
	}

	files, err := selectFiles(config)
	if err != nil {
		return err
	}

	events, err := readEvents(ctx, files)
	if err != nil {
		return err
	}

	logger.Info("read quarterly index events",
		"files", len(files), "events", len(events))

	if config.DryRun {
		return nil
	}

	lock, err := applock.Acquire(appdata.Path("edgard.lock"))
	if err != nil {
		return fmt.Errorf("index import requires edgard to be stopped: %w", err)
	}
	defer func() { _ = lock.Close() }()

	store, err := pollingworker.NewDedupeServiceContext(ctx, config.PollingDB)
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

	bus, err := eventbus.Connect(config.NATSURL)
	if err != nil {
		return err
	}
	defer func() { _ = bus.Close() }()

	return store.PublishPending(ctx, bus)
}

func selectFiles(config Config) ([]string, error) {
	if config.File != "" {
		return []string{config.File}, nil
	}

	entries, err := os.ReadDir(config.IndexesDirectory)
	if err != nil {
		return nil, fmt.Errorf("read indexes directory: %w", err)
	}

	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		match := quarterlyFilePattern.FindStringSubmatch(entry.Name())
		if len(match) != 3 {
			continue
		}

		year, _ := strconv.Atoi(match[1])
		if (config.Year != 0 && year != config.Year) ||
			(config.FromYear != 0 && year < config.FromYear) ||
			(config.ToYear != 0 && year > config.ToYear) {
			continue
		}

		files = append(files, filepath.Join(config.IndexesDirectory,
			entry.Name()))
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

func readEvents(ctx context.Context,
	files []string,
) ([]pollingworker.FilingEvent, error) {
	events := make([]pollingworker.FilingEvent, 0)

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
			events = append(events, pollingworker.FilingEvent{
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
