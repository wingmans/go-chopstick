package chopstick

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	pipeline "wingman.com/chopstick/internal/chopstick/pipeline"
	"wingman.com/chopstick/internal/edgar"
)

func ensureMasterIndex(ctx context.Context, client *http.Client,
	config Config, logger *slog.Logger,
) error {
	if _, err := os.Stat(config.MasterPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect master index: %w", err)
	}

	logger.Info("starting missing master index bootstrap",
		"from_year", config.FromYear, "path", config.MasterPath)

	err := edgar.DownloadIndex(ctx, client, edgar.Config{
		Directory:     config.IndexDirectory,
		ZipDirectory:  config.IndexZipDirectory,
		MasterPath:    config.MasterPath,
		SinceYear:     config.FromYear,
		UserAgent:     config.UserAgent,
		RefreshLatest: false,
		Stitch:        true,
		BaseURL:       config.BaseURL,
	})
	if err != nil {
		return err
	}

	logger.Info("master index bootstrap complete", "path", config.MasterPath)

	return nil
}

func reconcileMasterIndex(ctx context.Context, config Config,
	logger *slog.Logger,
) error {
	if config.SetName == "" || len(config.CIKs) == 0 {
		logger.Warn("skipping master index reconciliation",
			"reason", "EDGAR_SET is not configured")

		return nil
	}

	bus, err := pipeline.Connect(config.NATSURL)
	if err != nil {
		return err
	}
	defer func() { _ = bus.Close() }()

	return discover(ctx, bus, config, logger)
}

func discover(ctx context.Context, bus *pipeline.NATS, config Config,
	logger *slog.Logger,
) error {
	file, err := os.Open(config.MasterPath)
	if err != nil {
		return fmt.Errorf("open master index: %w", err)
	}
	defer func() { _ = file.Close() }()

	store, err := pipeline.Open(ctx, config.PollingDB)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	reader := edgar.NewIndexReader(file, edgar.IndexFilter{
		CIKs: config.CIKs, FormTypes: config.FormTypes,
		FromYear: config.FromYear,
	})
	batch := make([]pipeline.Event, 0, 1000)
	selected := 0
	discovered := 0
	flush := func() error {
		if len(batch) > 0 {
			newEvents, saveErr := store.SaveBatchContext(ctx, batch)
			if saveErr != nil {
				return saveErr
			}

			discovered += len(newEvents)
			batch = batch[:0]
		}

		return store.PublishPending(ctx, bus)
	}

	for {
		record, readErr := reader.Next()
		if errors.Is(readErr, io.EOF) {
			if err := flush(); err != nil {
				return err
			}

			logger.Info("master index reconciliation complete",
				"selected", selected, "new", discovered)

			return nil
		}

		if readErr != nil {
			return fmt.Errorf("read master index: %w", readErr)
		}

		accession := strings.TrimSuffix(filepath.Base(record.FilingPath),
			filepath.Ext(record.FilingPath))
		batch = append(batch, pipeline.Event{
			AccessionNumber: accession, CIK: record.CIK,
			CompanyName: record.CompanyName, FormType: record.FormType,
			FilingDate: record.DateFiled.Format("2006-01-02"),
			FilingPath: record.FilingPath,
			FilingURL: "https://www.sec.gov/Archives/" +
				strings.TrimPrefix(record.FilingPath, "/"),
			SourceFeed: "BATCH", Timestamp: time.Now().UTC(),
		})
		selected++

		if len(batch) == cap(batch) {
			if err := flush(); err != nil {
				return err
			}
		}
	}
}
