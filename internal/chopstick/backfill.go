package chopstick

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	pipeline "wingman.com/chopstick/internal/edgar/pipeline"
	"wingman.com/chopstick/internal/ingest/sec"
	"wingman.com/chopstick/internal/platform/appdata"
	"wingman.com/chopstick/internal/platform/outbound"
)

// Backfill downloads the historical index, publishes discoveries, and drains
// the finite selection, download, and processing workload.
func Backfill(ctx context.Context, config Config, logger *slog.Logger) error {
	lock, err := appdata.AcquireLock(appdata.Path("chopd.lock"))
	if err != nil {
		return fmt.Errorf("backfill requires chopd to be stopped: %w", err)
	}
	defer func() { _ = lock.Close() }()

	return backfill(ctx, config, logger)
}

func backfill(ctx context.Context, config Config, logger *slog.Logger) error {
	if err := config.Validate(); err != nil {
		return err
	}

	if config.SetName == "" || len(config.CIKs) == 0 {
		return errors.New("a constituent set is required")
	}

	if err := ensureParentDirectories(config); err != nil {
		return err
	}

	started := time.Now()

	logger.Info("starting backfill", "set", config.SetName,
		"from_year", config.FromYear, "through", "current available data")

	client := outbound.NewClient(30 * time.Second)
	requestGate := sec.NewRequestGate()

	if err := runBackfillStage(logger, "index", func() error {
		return sec.DownloadIndex(ctx, client, sec.Config{
			Directory:     config.IndexDirectory,
			ZipDirectory:  config.IndexZipDirectory,
			MasterPath:    config.MasterPath,
			SinceYear:     config.FromYear,
			UserAgent:     config.UserAgent,
			RefreshLatest: false,
			Stitch:        true,
			BaseURL:       config.BaseURL,
			RequestGate:   requestGate,
		})
	}); err != nil {
		return fmt.Errorf("index stage: %w", err)
	}

	bus, err := pipeline.Connect(ctx, config.NATSURL)
	if err != nil {
		return err
	}
	defer func() { _ = bus.Close() }()

	if err := runBackfillStage(logger, "discovery", func() error {
		return discover(ctx, bus, config, logger.With("role", "discover"))
	}); err != nil {
		return fmt.Errorf("discover stage: %w", err)
	}

	if err := runBackfillStage(logger, "selection", func() error {
		return pipeline.RunSelection(ctx, bus, pipeline.SelectionConfig{
			SetName: config.SetName, CIKs: config.CIKs,
			FormTypes: config.FormTypes, Drain: true,
		}, logger.With("role", "select"))
	}); err != nil {
		return fmt.Errorf("select stage: %w", err)
	}

	failures, err := pipeline.Open(
		ctx, config.PollingDB,
	)
	if err != nil {
		return err
	}
	defer func() { _ = failures.Close() }()

	if err := runBackfillStage(logger, "download", func() error {
		return pipeline.RunSubmission(ctx, bus, pipeline.SubmissionConfig{
			Directory: config.FilingsDirectory, UserAgent: config.UserAgent,
			BaseURL: config.BaseURL, Drain: true,
			MaxAttempts: config.MaxAttempts, Failures: failures,
			RequestGate: requestGate,
		}, logger.With("role", "download"))
	}); err != nil {
		return fmt.Errorf("download stage: %w", err)
	}

	if err := runBackfillStage(logger, "processing", func() error {
		return pipeline.RunExtraction(ctx, bus, pipeline.ExtractionConfig{
			FilingsDirectory: config.FilingsDirectory,
			ParsedDirectory:  config.ParsedDirectory,
			AnalysisDBPath:   config.AnalysisDB, Drain: true,
			MaxAttempts: config.MaxAttempts, Failures: failures,
		}, logger.With("role", "process"))
	}); err != nil {
		return fmt.Errorf("process stage: %w", err)
	}

	if err := runBackfillStage(logger, "ownership", func() error {
		return pipeline.RunOwnershipConsumer(ctx, bus, pipeline.OwnershipConfig{
			FilingsDirectory: config.FilingsDirectory,
			DatabasePath:     config.OwnershipDB,
			UserAgent:        config.UserAgent,
			BaseURL:          config.BaseURL,
			Drain:            true,
			MaxAttempts:      config.MaxAttempts,
			Failures:         failures,
			RequestGate:      requestGate,
		}, logger.With("role", "ownership"))
	}); err != nil {
		return fmt.Errorf("ownership stage: %w", err)
	}

	unresolved, err := failures.WorkFailures(ctx, "")
	if err != nil {
		return err
	}

	if len(unresolved) > 0 {
		return fmt.Errorf("backfill completed with %d unresolved failure(s); "+
			"run 'chop failures list'", len(unresolved))
	}

	logger.Info("backfill complete", "set", config.SetName,
		"from_year", config.FromYear,
		"duration_ms", time.Since(started).Milliseconds())

	return nil
}

func runBackfillStage(logger *slog.Logger, stage string, run func() error) error {
	started := time.Now()

	logger.Info("backfill stage started", "stage", stage)

	if err := run(); err != nil {
		return err
	}

	logger.Info("backfill stage completed", "stage", stage,
		"duration_ms", time.Since(started).Milliseconds())

	return nil
}

// Rebuild removes rebuildable EDGAR state, resets retained pipeline events,
// and restores the requested historical scope from retained inputs and SEC.
func Rebuild(ctx context.Context, config Config, logger *slog.Logger) error {
	if err := config.Validate(); err != nil {
		return err
	}

	if config.SetName == "" || len(config.CIKs) == 0 {
		return errors.New("a constituent set is required")
	}

	lock, err := appdata.AcquireLock(appdata.Path("chopd.lock"))
	if err != nil {
		return fmt.Errorf("rebuild requires chopd to be stopped: %w", err)
	}
	defer func() { _ = lock.Close() }()

	if err := resetDerivedState(); err != nil {
		return err
	}

	if err := pipeline.ResetStream(ctx, config.NATSURL); err != nil {
		return err
	}

	return backfill(ctx, config, logger)
}

func resetDerivedState() error {
	root, err := filepath.Abs(appdata.Root())
	if err != nil {
		return fmt.Errorf("resolve data root: %w", err)
	}

	workingDirectory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve working directory: %w", err)
	}

	workingDirectory, err = filepath.Abs(workingDirectory)
	if err != nil {
		return fmt.Errorf("resolve working directory: %w", err)
	}

	if root == string(filepath.Separator) || root == workingDirectory {
		return fmt.Errorf("refusing to rebuild unsafe data root %s", root)
	}

	for _, directory := range []string{"parsed", "validation", "tmp"} {
		if err := os.RemoveAll(filepath.Join(root, directory)); err != nil {
			return fmt.Errorf("remove %s: %w", directory, err)
		}
	}

	files := []string{
		filepath.Join(root, "cache", "indexes", "master.tsv"),
		filepath.Join(root, "cache", "indexes", "import-state.json"),
	}

	for _, name := range []string{"polling.db", "analysis.db", "feed.db"} {
		for _, suffix := range []string{"", "-shm", "-wal", "-journal"} {
			files = append(files, filepath.Join(root, name+suffix))
		}
	}

	for _, path := range files {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
	}

	return nil
}
