// Package edgar downloads the SEC EDGAR quarterly filing indexes.
package edgar

import (
	"archive/zip"
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"wingman.com/fetch-ecb/internal/ctxlog"
)

const (
	EarliestYear   = 1993
	DefaultBaseURL = "https://www.sec.gov/Archives"
	headerLines    = 11
	separator      = "|"
	requestBudget  = 2 * time.Second
)

// Config contains the inputs used to download the quarterly indexes.
type Config struct {
	Directory     string
	ZipDirectory  string
	MasterPath    string
	SinceYear     int
	UserAgent     string
	RefreshLatest bool
	Stitch        bool
	BaseURL       string
}

// Archive describes one quarterly EDGAR index archive.
type Archive struct {
	URL      string
	FileName string
}

// QuarterlyArchives returns archives from sinceYear through the current
// quarter, newest first.
func QuarterlyArchives(sinceYear int, now time.Time, baseURL string) ([]Archive, error) {
	if sinceYear < EarliestYear {
		return nil, fmt.Errorf("start year must be %d or later", EarliestYear)
	}

	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}

	currentYear := now.Year()
	if sinceYear > currentYear {
		return []Archive{}, nil
	}

	currentQuarter := (int(now.Month())-1)/3 + 1
	archives := make([]Archive, 0, (currentYear-sinceYear+1)*4)

	for year := currentYear; year >= sinceYear; year-- {
		lastQuarter := 4
		if year == currentYear {
			lastQuarter = currentQuarter
		}

		for quarter := lastQuarter; quarter >= 1; quarter-- {
			fileName := fmt.Sprintf("%d-QTR%d.tsv", year, quarter)
			archives = append(archives, Archive{
				URL:      fmt.Sprintf("%s/edgar/full-index/%d/QTR%d/master.zip", baseURL, year, quarter),
				FileName: fileName,
			})
		}
	}

	return archives, nil
}

// DownloadIndex downloads and extracts all requested quarterly indexes.
func DownloadIndex(ctx context.Context, client *http.Client, cfg Config) error {
	if strings.TrimSpace(cfg.UserAgent) == "" {
		return errors.New("user agent is required")
	}

	if cfg.Directory == "" {
		return errors.New("directory is required")
	}

	if client == nil {
		client = http.DefaultClient
	}

	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}

	archives, err := QuarterlyArchives(cfg.SinceYear, time.Now(), cfg.BaseURL)
	if err != nil {
		return err
	}

	zipDirectory := cfg.ZipDirectory
	if zipDirectory == "" {
		zipDirectory = cfg.Directory
	}

	masterPath := cfg.MasterPath
	if masterPath == "" {
		masterPath = filepath.Join(cfg.Directory, "master.tsv")
	}

	for _, directory := range []string{cfg.Directory, zipDirectory, filepath.Dir(masterPath)} {
		if err := os.MkdirAll(directory, 0o750); err != nil {
			return fmt.Errorf("create destination directory %s: %w", directory, err)
		}
	}

	for i, archive := range archives {
		started := time.Now()

		downloaded, err := downloadArchive(ctx, client, cfg.Directory, zipDirectory, archive, cfg.UserAgent, cfg.RefreshLatest && i == 0)
		if err != nil {
			return err
		}

		if downloaded {
			ctxlog.FromContext(ctx).Debug("downloaded archive",
				"filename", archive.FileName,
				"duration_ms", time.Since(started).Milliseconds(),
			)
		} else {
			ctxlog.FromContext(ctx).Debug("skipped existing archive", "filename", archive.FileName)
		}

		if downloaded && i < len(archives)-1 {
			if err := waitForRequestBudget(ctx, time.Since(started)); err != nil {
				return err
			}
		}
	}

	if cfg.Stitch {
		if err := StitchTo(cfg.Directory, masterPath); err != nil {
			return err
		}
	}

	return nil
}

// Stitch concatenates all quarterly TSV files in directory into master.tsv.
func Stitch(directory string) error {
	return StitchTo(directory, filepath.Join(directory, "master.tsv"))
}

// StitchTo concatenates all quarterly TSV files in directory into destination.
func StitchTo(directory, destination string) error {
	paths, err := filepath.Glob(filepath.Join(directory, "*-QTR*.tsv"))
	if err != nil {
		return fmt.Errorf("find quarterly indexes: %w", err)
	}

	if len(paths) == 0 {
		return fmt.Errorf("no quarterly TSV files found in %s", directory)
	}

	sort.Strings(paths)

	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return fmt.Errorf("create master index directory: %w", err)
	}

	temporary, err := os.CreateTemp(filepath.Dir(destination), ".master-*.part")
	if err != nil {
		return fmt.Errorf("create stitched index: %w", err)
	}

	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()

	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			_ = temporary.Close()

			return fmt.Errorf("open quarterly index %s: %w", path, err)
		}

		_, copyErr := io.Copy(temporary, file)

		closeErr := file.Close()
		if copyErr != nil {
			_ = temporary.Close()

			return fmt.Errorf("stitch quarterly index %s: %w", path, copyErr)
		}

		if closeErr != nil {
			_ = temporary.Close()

			return fmt.Errorf("close quarterly index %s: %w", path, closeErr)
		}
	}

	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close stitched index: %w", err)
	}

	if err := os.Rename(temporaryName, destination); err != nil {
		return fmt.Errorf("store stitched index %s: %w", destination, err)
	}

	return nil
}

func downloadArchive(ctx context.Context,
	client *http.Client, directory, zipDirectory string,
	archive Archive, userAgent string, force bool,
) (bool, error) {
	indexPath := filepath.Join(directory, archive.FileName)
	if !force {
		if _, err := os.Stat(indexPath); err == nil {
			return false, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("check %s: %w", indexPath, err)
		}
	}

	zipPath := filepath.Join(zipDirectory, strings.TrimSuffix(archive.FileName, ".tsv")+".zip")
	if err := ensureZip(ctx, client, archive, zipPath, userAgent); err != nil {
		return false, err
	}

	if err := extractIndex(zipPath, indexPath); err != nil {
		return false, fmt.Errorf("extract %s: %w", archive.FileName, err)
	}

	return true, nil
}

func waitForRequestBudget(ctx context.Context, elapsed time.Duration) error {
	wait := requestBudget - elapsed
	if wait <= 0 {
		return nil
	}

	ctxlog.FromContext(ctx).Debug("waiting before next download", "wait_ms", wait.Milliseconds())

	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func downloadZip(ctx context.Context, client *http.Client, url, destination, userAgent string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request for %s: %w", url, err)
	}

	req.Header.Set("User-Agent", userAgent)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("SEC rejected %s with HTTP %s", url, resp.Status)
	}

	temporary, err := os.CreateTemp(filepath.Dir(destination), ".edgar-*.part")
	if err != nil {
		return fmt.Errorf("create temporary archive: %w", err)
	}

	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()

	if _, err := io.Copy(temporary, resp.Body); err != nil {
		_ = temporary.Close()

		return fmt.Errorf("save archive %s: %w", url, err)
	}

	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary archive: %w", err)
	}

	if !isZip(temporaryName) {
		return fmt.Errorf("SEC response was not a valid ZIP archive: %s", url)
	}

	if err := os.Rename(temporaryName, destination); err != nil {
		return fmt.Errorf("store archive %s: %w", destination, err)
	}

	return nil
}

func isZip(path string) bool {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return false
	}

	return reader.Close() == nil
}

func ensureZip(ctx context.Context, client *http.Client, archive Archive, zipPath, userAgent string) error {
	_, err := os.Stat(zipPath)
	switch {
	case err == nil && isZip(zipPath):
		return nil
	case err == nil:
		if removeErr := os.Remove(zipPath); removeErr != nil {
			return fmt.Errorf("remove invalid archive %s: %w", zipPath, removeErr)
		}
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return fmt.Errorf("check %s: %w", zipPath, err)
	}

	return downloadZip(ctx, client, archive.URL, zipPath, userAgent)
}

func extractIndex(zipPath, indexPath string) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()

	var master *zip.File

	for _, file := range reader.File {
		if file.Name == "master.idx" {
			master = file

			break
		}
	}

	if master == nil {
		return errors.New("archive does not contain master.idx")
	}

	source, err := master.Open()
	if err != nil {
		return fmt.Errorf("open master.idx: %w", err)
	}
	defer func() { _ = source.Close() }()

	output, err := os.Create(indexPath)
	if err != nil {
		return fmt.Errorf("create index: %w", err)
	}
	defer func() { _ = output.Close() }()

	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for range headerLines {
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("read master.idx header: %w", err)
			}

			return errors.New("master.idx has fewer than 11 header lines")
		}
	}

	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			continue
		}

		fields := strings.Split(line, separator)
		if len(fields) < 5 {
			return fmt.Errorf("invalid master.idx row: %q", line)
		}

		line += separator + strings.ReplaceAll(fields[len(fields)-1], ".txt", "-index.html")
		if _, err := fmt.Fprintln(output, line); err != nil {
			return fmt.Errorf("write index: %w", err)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read master.idx: %w", err)
	}

	return nil
}
