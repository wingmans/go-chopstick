package mirror

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"wingman.com/fetch-ecb/internal/constituents"
	"wingman.com/fetch-ecb/internal/parser"
)

func inspectObject(ctx context.Context, store SubmissionStore,
	object ObjectInfo, temporaryDirectory string,
) (Submission, error) {
	reader, physical, err := store.Open(ctx, object.Key)
	if err != nil {
		return Submission{}, err
	}
	defer func() { _ = reader.Close() }()

	if err := os.MkdirAll(temporaryDirectory, 0o750); err != nil {
		return Submission{}, fmt.Errorf("create mirror inspection directory: %w",
			err)
	}

	temporary, err := os.CreateTemp(temporaryDirectory, ".submission-*.txt")
	if err != nil {
		return Submission{}, fmt.Errorf("create mirror inspection file: %w", err)
	}

	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()

	hash := sha256.New()

	written, copyErr := io.Copy(io.MultiWriter(temporary, hash), reader)
	if copyErr != nil {
		_ = temporary.Close()

		return Submission{}, copyErr
	}

	if err := temporary.Close(); err != nil {
		return Submission{}, err
	}

	metadata, err := parser.ReadSubmissionMetadata(ctx, name)
	if err != nil {
		return Submission{}, fmt.Errorf("read mirrored SEC header: %w", err)
	}

	cik, err := constituents.NormalizeCIK(metadata.CIK)
	if err != nil {
		return Submission{}, err
	}

	date, err := normalizedFilingDate(metadata.FilingDate)
	if err != nil {
		return Submission{}, err
	}

	year, _ := filingYear(date)

	value := Submission{
		//nolint:modernize // Naming the embedded field keeps this clear.
		SubmissionRef: SubmissionRef{
			Accession: strings.TrimSpace(metadata.Accession), CIK: cik,
			FormType: strings.TrimSpace(metadata.FormType), FilingDate: date,
			FilingPath: object.Key,
		},
		FilingYear: year, LogicalBytes: written,
		StoredBytes: physical.StoredBytes,
		SHA256:      hex.EncodeToString(hash.Sum(nil)),
	}
	if err := validateObjectIdentity(value); err != nil {
		return Submission{}, err
	}

	return value, nil
}

func refFromKey(key string) (SubmissionRef, error) {
	accession := strings.TrimSuffix(filepath.Base(key), filepath.Ext(key))

	cik, err := constituents.NormalizeCIK(filepath.Base(filepath.Dir(key)))
	if err != nil {
		return SubmissionRef{}, fmt.Errorf("derive mirror CIK from %s: %w", key,
			err)
	}

	return SubmissionRef{
		Accession: accession, CIK: cik, FilingPath: key,
	}, nil
}

func validateObjectIdentity(value Submission) error {
	base := strings.TrimSuffix(filepath.Base(value.FilingPath),
		filepath.Ext(value.FilingPath))
	if base != value.Accession {
		return fmt.Errorf("%w: path accession %q differs from header %q",
			ErrCorruptObject, base, value.Accession)
	}

	parent := filepath.Base(filepath.Dir(value.FilingPath))

	pathCIK, err := constituents.NormalizeCIK(parent)
	if err != nil || pathCIK != value.CIK {
		return fmt.Errorf("%w: path CIK %q differs from header %q",
			ErrCorruptObject, parent, value.CIK)
	}

	return nil
}
