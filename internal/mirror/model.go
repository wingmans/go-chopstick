// Package mirror stores reconstructible, compressed SEC submissions.
package mirror

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

var (
	ErrNotFound         = errors.New("mirror submission not found")
	ErrInvalidReference = errors.New("invalid mirror submission reference")
	ErrUnavailable      = errors.New("mirror unavailable")
	ErrCorruptObject    = errors.New("corrupt mirror object")
	ErrCatalogSchema    = errors.New("incompatible mirror catalog schema")
)

// SubmissionRef identifies one SEC submission and its origin path.
type SubmissionRef struct {
	Accession  string `json:"accession"`
	CIK        string `json:"cik"`
	FormType   string `json:"form_type"`
	FilingDate string `json:"filing_date"`
	FilingPath string `json:"filing_path"`
}

// Submission describes one mirrored raw SEC submission.
type Submission struct {
	SubmissionRef

	FilingYear   int    `json:"filing_year"`
	LogicalBytes int64  `json:"logical_bytes"`
	StoredBytes  int64  `json:"stored_bytes"`
	SHA256       string `json:"sha256"`
}

// ObjectInfo describes one physical compressed object.
type ObjectInfo struct {
	Key          string
	LogicalBytes int64
	StoredBytes  int64
	SHA256       string
}

// Content is an uncompressed submission stream and its metadata.
type Content struct {
	Submission Submission
	Reader     io.ReadCloser
}

// SearchFilter limits catalog results.
type SearchFilter struct {
	CIK        string
	FormType   string
	FilingYear int
	Limit      int
	Cursor     string
}

// SubmissionPage is one stable catalog page.
type SubmissionPage struct {
	Items      []Submission `json:"items"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

// Stats summarizes compressed mirror storage.
type Stats struct {
	Objects      int64   `json:"objects"`
	LogicalBytes int64   `json:"logical_bytes"`
	StoredBytes  int64   `json:"stored_bytes"`
	SavedBytes   int64   `json:"saved_bytes"`
	Ratio        float64 `json:"compression_ratio"`
}

func validateRef(ref SubmissionRef) error {
	if strings.TrimSpace(ref.Accession) == "" ||
		strings.TrimSpace(ref.CIK) == "" ||
		strings.TrimSpace(ref.FilingPath) == "" {
		return fmt.Errorf("%w: accession, CIK, and filing path are required",
			ErrInvalidReference)
	}

	return nil
}
