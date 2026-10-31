package edgarapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"wingman.com/chopstick/internal/appdata"
	"wingman.com/chopstick/internal/applock"
	"wingman.com/chopstick/internal/edgar"
	"wingman.com/chopstick/internal/eventbus"
	"wingman.com/chopstick/internal/eventworker"
	"wingman.com/chopstick/internal/pollingworker"
)

// Backfill downloads the historical index, publishes discoveries, and drains
// the finite selection, download, and processing workload.
func Backfill(ctx context.Context, config Config, logger *slog.Logger) error {
	lock, err := applock.Acquire(appdata.Path("chopd.lock"))
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

	logger.Info("starting backfill", "set", config.SetName,
		"from_year", config.FromYear, "through", "current available data")

	client := &http.Client{Timeout: 30 * time.Second}
	if err := edgar.DownloadIndex(ctx, client, edgar.Config{
		Directory:     config.IndexDirectory,
		ZipDirectory:  config.IndexZipDirectory,
		MasterPath:    config.MasterPath,
		SinceYear:     config.FromYear,
		UserAgent:     config.UserAgent,
		RefreshLatest: false,
		Stitch:        true,
		BaseURL:       config.BaseURL,
	}); err != nil {
		return fmt.Errorf("index stage: %w", err)
	}

	bus, err := eventbus.Connect(config.NATSURL)
	if err != nil {
		return err
	}
	defer func() { _ = bus.Close() }()

	if err := discover(ctx, bus, config, logger.With("role", "discover")); err != nil {
		return fmt.Errorf("discover stage: %w", err)
	}

	if err := eventworker.Select(ctx, bus, eventworker.SelectionConfig{
		SetName: config.SetName, CIKs: config.CIKs,
		FormTypes: config.FormTypes, Drain: true,
	}, logger.With("role", "select")); err != nil {
		return fmt.Errorf("select stage: %w", err)
	}

	failures, err := pollingworker.NewDedupeServiceContext(
		ctx, config.PollingDB,
	)
	if err != nil {
		return err
	}
	defer func() { _ = failures.Close() }()

	if err := eventworker.Download(ctx, bus, eventworker.DownloadConfig{
		Directory: config.FilingsDirectory, UserAgent: config.UserAgent,
		BaseURL: config.BaseURL, Drain: true,
		MaxAttempts: config.MaxAttempts, Failures: failures,
	}, logger.With("role", "download")); err != nil {
		return fmt.Errorf("download stage: %w", err)
	}

	if err := eventworker.Process(ctx, bus, eventworker.ProcessConfig{
		FilingsDirectory: config.FilingsDirectory,
		ParsedDirectory:  config.ParsedDirectory,
		AnalysisDBPath:   config.AnalysisDB, Drain: true,
		MaxAttempts: config.MaxAttempts, Failures: failures,
	}, logger.With("role", "process")); err != nil {
		return fmt.Errorf("process stage: %w", err)
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
		"from_year", config.FromYear)

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

	lock, err := applock.Acquire(appdata.Path("chopd.lock"))
	if err != nil {
		return fmt.Errorf("rebuild requires chopd to be stopped: %w", err)
	}
	defer func() { _ = lock.Close() }()

	if err := resetDerivedState(); err != nil {
		return err
	}

	if err := eventbus.ResetStream(config.NATSURL); err != nil {
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

	files := []string{filepath.Join(root, "cache", "indexes", "master.tsv")}

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
