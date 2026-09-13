package filingworkflow

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"wingman.com/fetch-ecb/internal/ctxlog"
	"wingman.com/fetch-ecb/internal/edgar"
)

// ProcessFilings streams the filtered index, ensures local source files, then
// reuses or rebuilds parsed results. Download-only APIs remain available separately.
func ProcessFilings(ctx context.Context, client *http.Client, master string, download edgar.FilingDownloadConfig,
	processing ProcessingConfig, filter edgar.IndexFilter,
) (ProcessingSummary, error) {
	var summary ProcessingSummary
	defer func() { summary.log(ctx, download.Noop) }()

	if processing.Directory == "" {
		return summary, errors.New("parsed directory is required")
	}

	file, err := os.Open(master)
	if err != nil {
		return summary, err
	}

	defer func() { _ = file.Close() }()

	processing.FilingsDirectory, processing.Noop = download.Directory, download.Noop

	if client == nil {
		client = http.DefaultClient
	}

	reader := edgar.NewIndexReader(file, filter)

	for {
		if err := ctx.Err(); err != nil {
			return summary, err
		}

		record, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return summary, err
		}

		result, err := processIndexRecord(ctx, client, download, processing, record)
		summary.record(ctx, record.FilingPath, result, err)

		if err := ctx.Err(); err != nil {
			return summary, err
		}
	}

	return summary, summary.failure()
}

// ProcessLocalFilings processes only submissions already present on disk. The
// master index supplies the same identity and filters used by the download flow.
func ProcessLocalFilings(ctx context.Context, master, directory string, processing ProcessingConfig,
	filter edgar.IndexFilter,
) (ProcessingSummary, error) {
	var summary ProcessingSummary
	defer func() { summary.log(ctx, processing.Noop) }()

	var failed ProcessingResult

	file, err := os.Open(master)
	if err != nil {
		return summary, err
	}

	defer func() { _ = file.Close() }()

	reader := edgar.NewIndexReader(file, filter)

	for {
		if err := ctx.Err(); err != nil {
			return summary, err
		}

		record, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return summary, err
		}

		path, err := edgar.LocalFilingPath(directory, record.FilingPath)
		if err != nil {
			return summary, err
		}

		_, statErr := os.Stat(path)
		if errors.Is(statErr, os.ErrNotExist) {
			ctxlog.FromContext(ctx).Debug("skipping unavailable local filing", "source", "local", "path", record.FilingPath)

			continue
		}

		if statErr != nil {
			summary.record(ctx, record.FilingPath, failed, statErr)

			continue
		}

		prepare, err := sourceNeedsPreparation(path)
		if err != nil {
			summary.record(ctx, record.FilingPath, failed, err)

			continue
		}

		if prepare {
			if processing.Noop {
				summary.record(ctx, record.FilingPath, ProcessingResult{Action: ActionPlanned, Path: path, Filing: nil}, nil)

				continue
			}

			if err := edgar.NormalizeLocalFiling(path); err != nil {
				summary.record(ctx, record.FilingPath, failed, err)

				continue
			}
		}

		result, err := ProcessSubmission(ctx, path, processing)
		summary.record(ctx, record.FilingPath, result, err)
	}

	return summary, summary.failure()
}

func processIndexRecord(ctx context.Context, client *http.Client, download edgar.FilingDownloadConfig,
	processing ProcessingConfig, record edgar.EdgarIndex,
) (ProcessingResult, error) {
	var result ProcessingResult

	started := time.Now()

	if err := edgar.DownloadFiling(ctx, client, download, record); err != nil {
		return result, err
	}

	path, err := edgar.LocalFilingPath(download.Directory, record.FilingPath)
	if err != nil {
		return result, err
	}

	if processing.Noop {
		prepare, err := sourceNeedsPreparation(path)
		if err != nil {
			return result, err
		}

		if prepare {
			result.Action = ActionPlanned
			ctxlog.FromContext(ctx).Info("would process after downloading or normalizing source",
				"path", record.FilingPath, "filename", filepath.Base(path), "action", result.Action,
				"duration_ms", time.Since(started).Milliseconds())

			return result, nil
		}
	}

	return ProcessSubmission(ctx, path, processing)
}
