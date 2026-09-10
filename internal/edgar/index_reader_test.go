package edgar

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadMasterTSVByFormType(t *testing.T) {
	masterPath := filepath.Join("..", "..", "data", "master.tsv")

	file, err := os.Open(masterPath)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("master index not available at %s", masterPath)
		}

		t.Fatalf("open %s: %v", masterPath, err)
	}

	defer func() { _ = file.Close() }()

	formType := "10-K"
	reader := NewIndexReader(file, IndexFilter{FormTypes: []string{formType}})

	for {
		record, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			t.Fatalf("read %s: %v", masterPath, err)
		}

		fmt.Printf("%+v\n", record)
	}
}

func TestIndexReaderReadsAndFiltersRecords(t *testing.T) {
	source := strings.NewReader(strings.Join([]string{
		"0000123456|Example Corp|10-K|2024-01-02|edgar/data/123456/filing.txt|edgar/data/123456/filing-index.html",
		"0000123456|Example Corp|8-K|2024-01-03|edgar/data/123456/current.txt|edgar/data/123456/current-index.html",
		"0000654321|Other Corp|10-K|2024-01-04|edgar/data/654321/filing.txt",
	}, "\n"))

	reader := NewIndexReader(source, IndexFilter{
		CIK:       "0000123456",
		FormTypes: []string{"10-K"},
	})

	record, err := reader.Next()
	if err != nil {
		t.Fatalf("Next returned error: %v", err)
	}

	if record.CIK != "0000123456" || record.FormType != "10-K" {
		t.Fatalf("unexpected record identity: %+v", record)
	}

	if record.DateFiled != time.Date(2024, time.January, 2, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("unexpected filed date: %v", record.DateFiled)
	}

	if record.IndexPath == "" {
		t.Fatalf("expected derived index path")
	}

	if _, err := reader.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

func TestIndexReaderRejectsMalformedRows(t *testing.T) {
	reader := NewIndexReader(strings.NewReader("123|Example|10-K|not-a-date|filing.txt\n"), IndexFilter{})

	if _, err := reader.Next(); err == nil {
		t.Fatal("expected malformed date error")
	}
}

func TestIndexReaderSkipsBlankRows(t *testing.T) {
	reader := NewIndexReader(strings.NewReader("\n123|Example|10-K|2024-01-02|filing.txt\n"), IndexFilter{})

	if _, err := reader.Next(); err != nil {
		t.Fatalf("Next returned error: %v", err)
	}
}
