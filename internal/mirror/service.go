package mirror

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"wingman.com/fetch-ecb/internal/constituents"
)

// API is the transport-independent mirror business interface.
type API interface {
	Ensure(ctx context.Context, ref SubmissionRef) (Submission, error)
	Get(ctx context.Context, accession string) (Submission, error)
	Search(ctx context.Context, filter SearchFilter) (SubmissionPage, error)
	Open(ctx context.Context, accession string) (Content, error)
	Stats(ctx context.Context) (Stats, error)
}

// Service coordinates storage, catalog queries, and SEC cache misses.
type Service struct {
	store   SubmissionStore
	catalog *Catalog
	origin  Origin
	tempDir string
	ensure  sync.Mutex
}

// NewService creates a mirror service from explicit dependencies.
func NewService(store SubmissionStore, catalog *Catalog, origin Origin,
	temporaryDirectory string,
) (*Service, error) {
	if store == nil || catalog == nil {
		return nil, errors.New("mirror store and catalog are required")
	}

	return &Service{
		store: store, catalog: catalog, origin: origin,
		tempDir: temporaryDirectory,
	}, nil
}

// Ensure returns a cached submission or retrieves and stores one SEC miss.
func (s *Service) Ensure(ctx context.Context, ref SubmissionRef) (
	Submission, error,
) {
	if err := validateRef(ref); err != nil {
		return Submission{}, err
	}

	if strings.TrimSpace(ref.FormType) == "" ||
		strings.TrimSpace(ref.FilingDate) == "" {
		return Submission{}, fmt.Errorf("%w: form type and filing date are required",
			ErrInvalidReference)
	}

	normalizedCIK, err := constituents.NormalizeCIK(ref.CIK)
	if err != nil {
		return Submission{}, fmt.Errorf("%w: %w", ErrInvalidReference, err)
	}

	ref.CIK = normalizedCIK
	ref.Accession = strings.TrimSpace(ref.Accession)
	ref.FormType = strings.ToUpper(strings.TrimSpace(ref.FormType))

	ref.FilingDate, err = normalizedFilingDate(ref.FilingDate)
	if err != nil {
		return Submission{}, fmt.Errorf("%w: %w", ErrInvalidReference, err)
	}

	s.ensure.Lock()
	defer s.ensure.Unlock()

	if object, err := s.store.Stat(ctx, ref.FilingPath); err == nil {
		if value, catalogErr := s.catalog.Get(ctx, ref.Accession); catalogErr == nil {
			if err := validateDiscovery(value, ref); err != nil {
				return Submission{}, err
			}

			return value, nil
		} else if !errors.Is(catalogErr, ErrNotFound) {
			return Submission{}, catalogErr
		}

		value, inspectErr := inspectObject(ctx, s.store, object, s.tempDir)
		if inspectErr != nil {
			return Submission{}, inspectErr
		}

		if err := validateDiscovery(value, ref); err != nil {
			return Submission{}, err
		}

		if err := s.catalog.Upsert(ctx, value); err != nil {
			return Submission{}, err
		}

		return value, nil
	} else if errors.Is(err, ErrInvalidReference) {
		return Submission{}, err
	} else if !errors.Is(err, ErrNotFound) {
		return Submission{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}

	if s.origin == nil {
		return Submission{}, ErrNotFound
	}

	reader, err := s.origin.Fetch(ctx, ref)
	if err != nil {
		return Submission{}, err
	}

	defer func() { _ = reader.Close() }()

	object, err := s.store.Put(ctx, ref.FilingPath, reader)
	if err != nil {
		return Submission{}, err
	}

	value, err := inspectObject(ctx, s.store, object, s.tempDir)
	if err != nil {
		return Submission{}, err
	}

	if err := validateDiscovery(value, ref); err != nil {
		return Submission{}, err
	}

	if err := s.catalog.Upsert(ctx, value); err != nil {
		return Submission{}, err
	}

	return value, nil
}

// Get reads catalog metadata by accession.
func (s *Service) Get(ctx context.Context, accession string) (
	Submission, error,
) {
	return s.catalog.Get(ctx, accession)
}

// Search queries the disposable catalog.
func (s *Service) Search(ctx context.Context, filter SearchFilter) (
	SubmissionPage, error,
) {
	if strings.TrimSpace(filter.CIK) != "" {
		cik, err := constituents.NormalizeCIK(filter.CIK)
		if err != nil {
			return SubmissionPage{}, fmt.Errorf("%w: %w",
				ErrInvalidReference, err)
		}

		filter.CIK = cik
	}

	return s.catalog.Search(ctx, filter)
}

// Open streams one original, uncompressed submission.
func (s *Service) Open(ctx context.Context, accession string) (Content, error) {
	value, err := s.catalog.Get(ctx, accession)
	if err != nil {
		return Content{}, err
	}

	reader, _, err := s.store.Open(ctx, value.FilingPath)
	if err != nil {
		return Content{}, err
	}

	return Content{Submission: value, Reader: reader}, nil
}

// Stats returns catalog-backed disk-space statistics.
func (s *Service) Stats(ctx context.Context) (Stats, error) {
	return s.catalog.Stats(ctx)
}

// Import reads uncompressed SEC submissions without changing their source.
func (s *Service) Import(ctx context.Context, root string) (int64, error) {
	var (
		imported        int64
		pendingImported int64
	)

	batch := make([]Submission, 0, catalogBatchSize)
	flush := func() error {
		if err := s.catalog.UpsertBatch(ctx, batch); err != nil {
			return err
		}

		imported += pendingImported
		pendingImported = 0
		batch = batch[:0]

		return nil
	}

	err := walkRawSubmissions(ctx, root, func(path, key string) error {
		_, statErr := s.store.Stat(ctx, key)

		exists := statErr == nil
		if statErr != nil && !errors.Is(statErr, ErrNotFound) {
			return statErr
		}

		file, err := openRaw(path)
		if err != nil {
			return err
		}

		object, putErr := s.store.Put(ctx, key, file)
		_ = file.Close()

		if putErr != nil {
			return putErr
		}

		value, inspectErr := inspectObject(ctx, s.store, object, s.tempDir)
		if inspectErr != nil {
			return inspectErr
		}

		if !exists {
			pendingImported++
		}

		batch = append(batch, value)
		if len(batch) == catalogBatchSize {
			return flush()
		}

		return nil
	})
	if err == nil {
		err = flush()
	}

	return imported, err
}

func validateDiscovery(value Submission, ref SubmissionRef) error {
	if value.Accession != ref.Accession || value.CIK != ref.CIK ||
		value.FormType != ref.FormType || value.FilingDate != ref.FilingDate ||
		value.FilingPath != ref.FilingPath {
		return fmt.Errorf("%w: SEC header differs from discovery metadata",
			ErrCorruptObject)
	}

	return nil
}
