package filingstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"wingman.com/fetch-ecb/internal/edgar"
	"wingman.com/fetch-ecb/internal/filingview"
)

// Store defines the read operations needed by the filing web UI.
type Store interface {
	ListSummaries(ctx context.Context) ([]Summary, error)
	LoadView(ctx context.Context, cik, accession string) (filingview.View, error)
	LoadFiling(ctx context.Context, cik, accession string) (*edgar.ParsedFiling, error)
	LoadCompanyHistory(ctx context.Context, cik string) (filingview.CompanyHistory, error)
	SourcePath(ctx context.Context, filing *edgar.ParsedFiling) (string, error)
}

// Summary is storage-level filing metadata for list screens and API responses.
type Summary struct {
	CIK        string
	Company    string
	Accession  string
	FormType   string
	FilingDate string
	ReportDate string
	Status     string
	Facts      int
}

type JSONDirectory struct {
	parsedDir string
}

var _ Store = (*JSONDirectory)(nil)

// NewJSONDirectory creates a Store backed by the parsed JSON directory.
func NewJSONDirectory(parsedDir string) *JSONDirectory {
	return &JSONDirectory{parsedDir: parsedDir}
}

func (s *JSONDirectory) LoadCompanyHistory(
	ctx context.Context,
	cik string,
) (filingview.CompanyHistory, error) {
	cik = canonicalCIK(cik)
	if cik == "" {
		return filingview.CompanyHistory{}, os.ErrNotExist
	}

	views := make([]filingview.View, 0)

	err := filepath.Walk(s.parsedDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if err := checkContext(ctx); err != nil {
			return err
		}

		if info.IsDir() || info.Name() != "filing-view.json" {
			return nil
		}

		view, err := filingview.Load(path)
		if err != nil {
			return fmt.Errorf("load %s: %w", path, err)
		}

		if canonicalCIK(view.Metadata.CIK) == cik {
			views = append(views, view)
		}

		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return filingview.CompanyHistory{
			CIK: "", Company: "", Years: []string{},
			Statements: []filingview.HistoryStatement{},
		}, nil
	}

	if err != nil {
		return filingview.CompanyHistory{}, err
	}

	return filingview.BuildCompanyHistory(views, cik)
}

func (s *JSONDirectory) LoadView(
	ctx context.Context,
	cik string,
	accession string,
) (filingview.View, error) {
	if err := checkContext(ctx); err != nil {
		return filingview.View{}, err
	}

	cik, accession, err := normalizeFilingKey(cik, accession)
	if err != nil {
		return filingview.View{}, os.ErrNotExist
	}

	path := filepath.Join(s.parsedDir, cik, accession, "filing-view.json")
	if !withinDirectory(s.parsedDir, path) {
		return filingview.View{}, os.ErrNotExist
	}

	return filingview.Load(path)
}

func (s *JSONDirectory) LoadFiling(
	ctx context.Context,
	cik string,
	accession string,
) (*edgar.ParsedFiling, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}

	cik, accession, err := normalizeFilingKey(cik, accession)
	if err != nil {
		return nil, os.ErrNotExist
	}

	path := filepath.Join(s.parsedDir, cik, accession, "filing.json")
	if !withinDirectory(s.parsedDir, path) {
		return nil, os.ErrNotExist
	}

	filing, err := edgar.LoadParsedFiling(path)

	return filing, err
}

func (s *JSONDirectory) ListSummaries(ctx context.Context) ([]Summary, error) {
	var result []Summary

	err := filepath.Walk(s.parsedDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if err := checkContext(ctx); err != nil {
			return err
		}

		if info.IsDir() || info.Name() != "filing-view.json" {
			return nil
		}

		view, err := filingview.Load(path)
		if err != nil {
			return fmt.Errorf("load %s: %w", path, err)
		}

		result = append(result,
			Summary{
				CIK:        view.Metadata.CIK,
				Company:    view.Metadata.Company,
				Accession:  view.Metadata.Accession,
				FormType:   view.Metadata.FormType,
				FilingDate: view.Metadata.FilingDate,
				ReportDate: view.Metadata.ReportDate,
				Status:     view.Counts.Status,
				Facts:      view.Counts.Facts,
			})

		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return []Summary{}, nil
	}

	if err != nil {
		return nil, err
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].FilingDate != result[j].FilingDate {
			return result[i].FilingDate > result[j].FilingDate
		}

		return result[i].Accession > result[j].Accession
	})

	return result, nil
}

func (s *JSONDirectory) SourcePath(
	ctx context.Context,
	filing *edgar.ParsedFiling,
) (string, error) {
	if err := checkContext(ctx); err != nil {
		return "", err
	}

	var source string

	switch filing.SourceBase {
	case "absolute":
		source = filing.SourcePath
	case "filings":
		source = filepath.Join(filepath.Dir(s.parsedDir), "filings",
			filepath.FromSlash(filing.SourcePath))
	default:
		return "", os.ErrNotExist
	}

	if !withinDirectory(filepath.Dir(s.parsedDir), source) {
		return "", os.ErrNotExist
	}

	return source, nil
}

func normalizeFilingKey(cik, accession string) (string, string, error) {
	cik = canonicalCIK(cik)
	accession = strings.TrimSpace(accession)
	if cik == "" || accession == "" {
		return "", "", os.ErrNotExist
	}

	return cik, accession, nil
}

func checkContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func canonicalCIK(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 10 {
		return ""
	}

	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return ""
		}
	}

	value = strings.TrimLeft(value, "0")
	if value == "" {
		value = "0"
	}

	return strings.Repeat("0", 10-len(value)) + value
}

func withinDirectory(directory, path string) bool {
	directory, err := filepath.Abs(directory)
	if err != nil {
		return false
	}

	path, err = filepath.Abs(path)
	if err != nil {
		return false
	}

	relative, err := filepath.Rel(directory, path)

	return err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
