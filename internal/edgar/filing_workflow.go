package edgar

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"wingman.com/fetch-ecb/internal/ctxlog"
)

// ProcessFilings streams the filtered index, ensures local source files, then
// reuses or rebuilds parsed results. Download-only APIs remain available separately.
func ProcessFilings(ctx context.Context, client *http.Client, master string, download FilingDownloadConfig,
	processing ProcessingConfig, filter IndexFilter,
) (ProcessingSummary, error) {
	var summary ProcessingSummary
	defer func() { summary.log(ctx, download.Noop) }()

	if err := validateFilingDownloadConfig(download); err != nil {
		return summary, err
	}

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

	reader := NewIndexReader(file, filter)

	var pacer requestPacer

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

		result, err := processIndexRecord(ctx, client, download, processing, record, &pacer)
		summary.record(ctx, record.FilingPath, result, err)

		if err := ctx.Err(); err != nil {
			return summary, err
		}
	}

	return summary, summary.failure()
}

func processIndexRecord(ctx context.Context, client *http.Client, download FilingDownloadConfig,
	processing ProcessingConfig, record EdgarIndex, pacer *requestPacer,
) (ProcessingResult, error) {
	var result ProcessingResult

	started := time.Now()

	if err := downloadReferencedFiles(ctx, client, download, record, pacer); err != nil {
		return result, err
	}

	path, err := localFilingPath(download.Directory, record.FilingPath)
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
