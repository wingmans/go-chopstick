package edgar

import (
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
)

// FilingDownloadConfig contains the settings for downloading filing files.
// Directory is the local root under which SEC-relative paths are preserved.
type FilingDownloadConfig struct {
	Directory string
	UserAgent string
	BaseURL   string
}

// UniqueFormTypes reads an EDGAR master TSV and returns its distinct form
// types in sorted order.
func UniqueFormTypes(masterPath string) ([]string, error) {
	file, err := os.Open(masterPath)
	if err != nil {
		return nil, fmt.Errorf("open master index %s: %w", masterPath, err)
	}
	defer func() { _ = file.Close() }()

	reader := NewIndexReader(file, IndexFilter{})
	types := make(map[string]struct{})

	for {
		record, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, fmt.Errorf("read master index %s: %w", masterPath, err)
		}

		types[record.FormType] = struct{}{}
	}

	result := make([]string, 0, len(types))
	for formType := range types {
		result = append(result, formType)
	}

	sort.Strings(result)

	return result, nil
}

// DownloadIndexFiles streams records from masterPath and downloads their
// filing and HTML index files sequentially. Existing files are skipped.
func DownloadIndexFiles(ctx context.Context, client *http.Client, masterPath string, cfg FilingDownloadConfig, filter IndexFilter) error {
	file, err := os.Open(masterPath)
	if err != nil {
		return fmt.Errorf("open master index %s: %w", masterPath, err)
	}
	defer func() { _ = file.Close() }()

	if err := validateFilingDownloadConfig(cfg); err != nil {
		return err
	}

	if client == nil {
		client = http.DefaultClient
	}

	reader := NewIndexReader(file, filter)
	pacer := requestPacer{}

	for {
		record, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("read master index %s: %w", masterPath, err)
		}

		if err := downloadReferencedFiles(ctx, client, cfg, record, &pacer); err != nil {
			return fmt.Errorf("download filing for CIK %s at %s: %w", record.CIK, record.FilingPath, err)
		}
	}
}

// DownloadFiling downloads the filing and HTML index files referenced by one
// EdgarIndex. Existing files are skipped.
func DownloadFiling(ctx context.Context, client *http.Client, cfg FilingDownloadConfig, record EdgarIndex) error {
	if err := validateFilingDownloadConfig(cfg); err != nil {
		return err
	}

	if client == nil {
		client = http.DefaultClient
	}

	pacer := requestPacer{}

	return downloadReferencedFiles(ctx, client, cfg, record, &pacer)
}

func validateFilingDownloadConfig(cfg FilingDownloadConfig) error {
	if strings.TrimSpace(cfg.UserAgent) == "" {
		return errors.New("user agent is required")
	}

	if strings.TrimSpace(cfg.Directory) == "" {
		return errors.New("filing directory is required")
	}

	return nil
}

func downloadReferencedFiles(ctx context.Context, client *http.Client, cfg FilingDownloadConfig, record EdgarIndex, pacer *requestPacer) error {
	paths := []string{record.FilingPath}
	if record.IndexPath != "" && record.IndexPath != record.FilingPath {
		paths = append(paths, record.IndexPath)
	}

	for _, relativePath := range paths {
		if err := downloadReferencedFile(ctx, client, cfg, relativePath, pacer); err != nil {
			return err
		}
	}

	return nil
}

func downloadReferencedFile(ctx context.Context, client *http.Client, cfg FilingDownloadConfig, relativePath string, pacer *requestPacer) error {
	destination, err := localArchivePath(cfg.Directory, relativePath)
	if err != nil {
		return err
	}

	info, err := os.Stat(destination)
	switch {
	case err == nil && !info.IsDir():
		return nil
	case err == nil:
		return fmt.Errorf("download destination is a directory: %s", destination)
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("check downloaded file %s: %w", destination, err)
	}

	if err := os.MkdirAll(filepath.Dir(destination), 0o750); err != nil {
		return fmt.Errorf("create download directory: %w", err)
	}

	if err := pacer.wait(ctx); err != nil {
		return err
	}

	requestURL := archiveURL(cfg.BaseURL, relativePath)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return fmt.Errorf("create request for %s: %w", requestURL, err)
	}

	req.Header.Set("User-Agent", cfg.UserAgent)
	req.Header.Set("Accept-Encoding", "gzip, deflate")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", requestURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("SEC rejected %s with HTTP %s", requestURL, resp.Status)
	}

	temporary, err := os.CreateTemp(filepath.Dir(destination), ".edgar-*.part")
	if err != nil {
		return fmt.Errorf("create temporary file for %s: %w", destination, err)
	}

	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()

	if _, err := io.Copy(temporary, resp.Body); err != nil {
		_ = temporary.Close()

		return fmt.Errorf("save %s: %w", destination, err)
	}

	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary file %s: %w", destination, err)
	}

	if err := os.Rename(temporaryName, destination); err != nil {
		return fmt.Errorf("store downloaded file %s: %w", destination, err)
	}

	return nil
}

func localArchivePath(directory, relativePath string) (string, error) {
	relativePath = strings.TrimSpace(relativePath)
	if relativePath == "" {
		return "", errors.New("archive path is empty")
	}

	cleanPath := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(relativePath, "/")))
	if cleanPath == "." || cleanPath == ".." || strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid archive path %q", relativePath)
	}

	return filepath.Join(directory, cleanPath), nil
}

func archiveURL(baseURL, relativePath string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}

	return baseURL + "/" + strings.TrimLeft(relativePath, "/")
}

type requestPacer struct {
	lastRequest time.Time
}

func (p *requestPacer) wait(ctx context.Context) error {
	if !p.lastRequest.IsZero() {
		if err := waitForRequestBudget(ctx, time.Since(p.lastRequest)); err != nil {
			return err
		}
	}

	p.lastRequest = time.Now()

	return nil
}
