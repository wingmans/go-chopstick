package filingweb

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"wingman.com/fetch-ecb/internal/edgar"
	"wingman.com/fetch-ecb/internal/filingview"
)

func (s *Server) loadCompanyHistory(cik string) (filingview.CompanyHistory, error) {
	views := make([]filingview.View, 0)

	err := filepath.Walk(s.parsedDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
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

func (s *Server) sourcePath(filing *edgar.ParsedFiling) (string, error) {
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

func (s *Server) loadView(parts []string) (filingview.View, error) {
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return filingview.View{}, os.ErrNotExist
	}

	cik := canonicalCIK(parts[0])
	if cik == "" {
		return filingview.View{}, os.ErrNotExist
	}

	path := filepath.Join(s.parsedDir, cik, parts[1], "filing-view.json")
	if !withinDirectory(s.parsedDir, path) {
		return filingview.View{}, os.ErrNotExist
	}

	return filingview.Load(path)
}

func (s *Server) loadFiling(parts []string) (*edgar.ParsedFiling, error) {
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, os.ErrNotExist
	}

	cik := canonicalCIK(parts[0])
	if cik == "" {
		return nil, os.ErrNotExist
	}

	path := filepath.Join(s.parsedDir, cik, parts[1], "filing.json")
	if !withinDirectory(s.parsedDir, path) {
		return nil, os.ErrNotExist
	}

	filing, err := edgar.LoadParsedFiling(path)

	return filing, err
}

func (s *Server) loadSummaries() ([]filingSummary, error) {
	var result []filingSummary

	err := filepath.Walk(s.parsedDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
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
			filingSummary{
				CIK:        view.Metadata.CIK,
				Ticker:     s.memberByCIK[canonicalCIK(view.Metadata.CIK)].Ticker,
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
		return []filingSummary{}, nil
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

// canonicalCIK preserves significant digits while normalizing leading padding.
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
