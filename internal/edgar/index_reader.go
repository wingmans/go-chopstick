package edgar

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

const indexDateLayout = "2006-01-02"

// EdgarIndex is one filing record from an EDGAR master index.
//
// CIK is kept as a string because it is an identifier, not a number. Filing
// and index paths are relative to the SEC Archives base URL.
type EdgarIndex struct {
	// CIK is the filer's Central Index Key, the unique identifier assigned by
	// EDGAR to the filer.
	CIK string

	// CompanyName is the company or other filer's name associated with the
	// filing.
	CompanyName string

	// FormType is the SEC form type submitted, such as 10-K, 10-Q, or 8-K.
	FormType string

	// DateFiled is the date the filing was submitted to the SEC.
	DateFiled time.Time

	// FilingPath is the SEC master-index "File Name" value, including its
	// folder path. It points to the raw submission text file.
	FilingPath string

	// IndexPath is the derived path to the SEC HTML filing index. It is not an
	// original master-index field; this repository adds it when extracting
	// quarterly indexes.
	IndexPath string
}

// IndexFilter limits the records returned by an IndexReader. Empty fields do
// not filter. FormTypes is matched exactly and case-sensitively.
type IndexFilter struct {
	CIK       string
	FormTypes []string
}

// IndexReader reads EDGAR index records one row at a time.
type IndexReader struct {
	scanner *bufio.Scanner
	filter  IndexFilter
	line    int
}

// NewIndexReader creates a streaming reader over an EDGAR index file.
//
// The reader accepts the five source columns and the six-column format
// written by extractIndex, which includes the derived index path.
func NewIndexReader(source io.Reader, filter IndexFilter) *IndexReader {
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	return &IndexReader{
		scanner: scanner,
		filter:  filter,
		line:    0,
	}
}

// Next returns the next record matching the reader's filter, or io.EOF when
// the input is exhausted.
func (r *IndexReader) Next() (EdgarIndex, error) {
	for r.scanner.Scan() {
		r.line++

		line := strings.TrimSuffix(r.scanner.Text(), "\r")
		if line == "" {
			continue
		}

		record, err := parseIndexLine(line)
		if err != nil {
			return EdgarIndex{}, fmt.Errorf("parse EDGAR index line %d: %w", r.line, err)
		}

		if matchesIndexFilter(record, r.filter) {
			return record, nil
		}
	}

	if err := r.scanner.Err(); err != nil {
		return EdgarIndex{}, fmt.Errorf("read EDGAR index line %d: %w", r.line+1, err)
	}

	return EdgarIndex{}, io.EOF
}

func parseIndexLine(line string) (EdgarIndex, error) {
	fields := strings.Split(line, separator)
	if len(fields) != 5 && len(fields) != 6 {
		return EdgarIndex{}, fmt.Errorf("expected 5 or 6 fields, got %d", len(fields))
	}

	dateFiled, err := time.Parse(indexDateLayout, strings.TrimSpace(fields[3]))
	if err != nil {
		return EdgarIndex{}, fmt.Errorf("invalid date filed %q: %w", fields[3], err)
	}

	record := EdgarIndex{
		CIK:         strings.TrimSpace(fields[0]),
		CompanyName: strings.TrimSpace(fields[1]),
		FormType:    strings.TrimSpace(fields[2]),
		DateFiled:   dateFiled,
		FilingPath:  strings.TrimSpace(fields[4]),
		IndexPath:   "",
	}

	if len(fields) == 6 {
		record.IndexPath = strings.TrimSpace(fields[5])
	}

	if record.CIK == "" {
		return EdgarIndex{}, errors.New("CIK is empty")
	}

	if record.FormType == "" {
		return EdgarIndex{}, errors.New("form type is empty")
	}

	if record.FilingPath == "" {
		return EdgarIndex{}, errors.New("filing path is empty")
	}

	return record, nil
}

func matchesIndexFilter(record EdgarIndex, filter IndexFilter) bool {
	if filter.CIK != "" && record.CIK != filter.CIK {
		return false
	}

	if len(filter.FormTypes) == 0 {
		return true
	}

	return slices.Contains(filter.FormTypes, record.FormType)
}
