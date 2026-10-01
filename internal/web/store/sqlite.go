package store

import (
	"context"
	"os"

	"wingman.com/fetch-ecb/internal/analysisstore"
	dividendview "wingman.com/fetch-ecb/internal/dividend"
	"wingman.com/fetch-ecb/internal/edgar"
	filingview "wingman.com/fetch-ecb/internal/filing"
)

// SQLiteStore reads normalized views from SQLite and raw parsed filings
// from the existing filesystem directory.
type SQLiteStore struct {
	files *JSONDirectory
	views *analysisstore.Store
}

var _ Store = (*SQLiteStore)(nil)

// NewSQLiteStore opens the analysis database used by the web application.
func NewSQLiteStore(parsedDir, databasePath string) (*SQLiteStore, error) {
	views, err := analysisstore.Open(context.Background(), databasePath)
	if err != nil {
		return nil, err
	}

	return &SQLiteStore{files: NewJSONDirectory(parsedDir), views: views}, nil
}

// Close closes the analysis database.
func (s *SQLiteStore) Close() error { return s.views.Close() }

func (s *SQLiteStore) ListSummaries(ctx context.Context) ([]Summary, error) {
	views, err := s.views.ListFilingViews(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]Summary, 0, len(views))
	for _, view := range views {
		result = append(result, Summary{
			CIK: view.Metadata.CIK, Company: view.Metadata.Company,
			Accession: view.Metadata.Accession, FormType: view.Metadata.FormType,
			FilingDate: view.Metadata.FilingDate, ReportDate: view.Metadata.ReportDate,
			Status: view.Counts.Status, Facts: view.Counts.Facts,
		})
	}

	return result, nil
}

func (s *SQLiteStore) LoadView(ctx context.Context, cik, accession string) (filingview.View, error) {
	cik, accession, err := normalizeFilingKey(cik, accession)
	if err != nil {
		return filingview.View{}, os.ErrNotExist
	}

	return s.views.LoadFilingView(ctx, cik, accession)
}

func (s *SQLiteStore) LoadFiling(ctx context.Context, cik, accession string) (*edgar.ParsedFiling, error) {
	filing, err := s.views.LoadParsedFiling(ctx, cik, accession)
	if err == nil {
		return filing, nil
	}

	if !os.IsNotExist(err) {
		return nil, err
	}

	return s.files.LoadFiling(ctx, cik, accession)
}

func (s *SQLiteStore) LoadCompanyHistory(ctx context.Context, cik string) (filingview.CompanyHistory, error) {
	cik = canonicalCIK(cik)
	if cik == "" {
		return filingview.CompanyHistory{}, os.ErrNotExist
	}

	views, err := s.views.ListFilingViews(ctx)
	if err != nil {
		return filingview.CompanyHistory{}, err
	}

	filtered := make([]filingview.View, 0, len(views))
	for _, view := range views {
		if canonicalCIK(view.Metadata.CIK) == cik {
			filtered = append(filtered, view)
		}
	}

	if len(filtered) == 0 {
		return filingview.CompanyHistory{CIK: "", Company: "", Years: []string{}, Statements: []filingview.HistoryStatement{}}, nil
	}

	return filingview.BuildCompanyHistory(filtered, cik)
}

func (s *SQLiteStore) LoadDividendView(ctx context.Context, cik string) (dividendview.View, error) {
	cik = canonicalCIK(cik)
	if cik == "" {
		return dividendview.View{}, os.ErrNotExist
	}

	return s.views.LoadDividendView(ctx, cik)
}

func (s *SQLiteStore) SourcePath(ctx context.Context, filing *edgar.ParsedFiling) (string, error) {
	return s.files.SourcePath(ctx, filing)
}
