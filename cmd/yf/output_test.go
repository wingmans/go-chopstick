package main

import (
	"context"
	"encoding/csv"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestDownloadUpdatesYearlyPartitionsIncrementally(t *testing.T) {
	firstDate := mustDate(t, "2024-01-02")
	secondDate := mustDate(t, "2024-01-03")
	requestStarts := make([]string, 0, 2)

	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter, request *http.Request,
	) {
		requestStarts = append(requestStarts, request.URL.Query().Get("period1"))

		date := firstDate
		if len(requestStarts) == 2 {
			date = secondDate
		}

		_, _ = io.WriteString(response, yahooFixture([]int64{
			date.Add(14 * time.Hour).Unix(),
		}))
	}))
	defer server.Close()

	outputDir := filepath.Join(t.TempDir(), "store")

	store, err := newCSVPriceStore(outputDir)
	if err != nil {
		t.Fatalf("create price store: %v", err)
	}

	var (
		reader priceReader = store
		writer priceWriter = store
	)

	cfg := config{
		manifestPath: "golden/us-gaap.json",
		outputDir:    outputDir,
		from:         firstDate,
		to:           firstDate,
		tickers:      []string{"MSFT"},
	}
	members := testManifest().Members
	logger := slog.New(slog.DiscardHandler)

	result, err := download(
		context.Background(), testYahooClient(server), cfg, members,
		reader, writer, logger,
	)
	if err != nil {
		t.Fatalf("first download: %v", err)
	}

	if result.observations != 1 || result.partitions != 1 {
		t.Fatalf("first result = %+v", result)
	}

	partitionPath := filepath.Join(outputDir, pricesDirectory, "2024.csv")
	assertCandidateRows(t, partitionPath, 1)
	assertNoDirectory(t, filepath.Join(outputDir, "raw"))
	assertNoDirectory(t, filepath.Join(outputDir, "runs"))

	cfg.to = secondDate

	result, err = download(
		context.Background(), testYahooClient(server), cfg, members,
		reader, writer, logger,
	)
	if err != nil {
		t.Fatalf("second download: %v", err)
	}

	if result.observations != 1 || result.partitions != 1 {
		t.Fatalf("second result = %+v", result)
	}

	assertCandidateRows(t, partitionPath, 2)

	wantSecondStart := strconv.FormatInt(secondDate.Unix(), 10)
	if len(requestStarts) != 2 || requestStarts[1] != wantSecondStart {
		t.Fatalf("request starts = %#v, want second start %s", requestStarts, wantSecondStart)
	}

	result, err = download(
		context.Background(), testYahooClient(server), cfg, members,
		reader, writer, logger,
	)
	if err != nil {
		t.Fatalf("third download: %v", err)
	}

	if result.observations != 0 || result.partitions != 0 {
		t.Fatalf("third result = %+v, want no changes", result)
	}

	if len(requestStarts) != 2 {
		t.Fatalf("request count = %d, want 2", len(requestStarts))
	}
}

func TestCSVPriceStoreUpsertsObservation(t *testing.T) {
	store, err := newCSVPriceStore(filepath.Join(t.TempDir(), "store"))
	if err != nil {
		t.Fatalf("create price store: %v", err)
	}

	date := mustDate(t, "2024-01-02")
	row := candidateRow{
		CIK:      "0000789019",
		Ticker:   "MSFT",
		Name:     "Microsoft Corporation",
		Date:     date,
		Open:     25,
		Close:    26,
		AdjClose: 24,
	}
	other := row
	other.CIK = "0000320193"
	other.Ticker = "AAPL"
	other.Name = "Apple Inc."

	if _, err := store.UpsertPrices(
		context.Background(), priceSequence(row, other),
	); err != nil {
		t.Fatalf("insert observation: %v", err)
	}

	row.Close = 27
	row.AdjClose = 25

	if _, err := store.UpsertPrices(context.Background(), priceSequence(row)); err != nil {
		t.Fatalf("update observation: %v", err)
	}

	path := filepath.Join(store.directory, "2024.csv")

	records := readCSV(t, path)
	if len(records) != 3 {
		t.Fatalf("CSV data row count = %d, want 2", len(records)-1)
	}

	if records[2][6] != "27" || records[2][7] != "25" {
		t.Fatalf("updated prices = %v, want close 27 and adjusted close 25", records[2])
	}
}

func TestCSVPriceStoreCutsStreamIntoYearlyPartitions(t *testing.T) {
	store, err := newCSVPriceStore(filepath.Join(t.TempDir(), "store"))
	if err != nil {
		t.Fatalf("create price store: %v", err)
	}

	first := candidateRow{
		CIK:      "0000789019",
		Ticker:   "MSFT",
		Name:     "Microsoft Corporation",
		Date:     mustDate(t, "2023-12-29"),
		Open:     25,
		Close:    26,
		AdjClose: 24,
	}
	second := first
	second.Date = mustDate(t, "2024-01-02")

	partitions, err := store.UpsertPrices(
		context.Background(), priceSequence(first, second),
	)
	if err != nil {
		t.Fatalf("write observations: %v", err)
	}

	if partitions != 2 {
		t.Fatalf("partition count = %d, want 2", partitions)
	}

	assertCandidateRows(t, filepath.Join(store.directory, "2023.csv"), 1)
	assertCandidateRows(t, filepath.Join(store.directory, "2024.csv"), 1)
	assertNoStagingDirectories(t, store.directory)
}

func TestParseConfigSelectsMSFTAndInclusiveRange(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

	cfg, err := parseConfig([]string{
		"fetch",
		"--ticker", "msft",
		"--from", "1986-03-13",
		"--to", "2026-10-25",
	}, io.Discard, now)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}

	if len(cfg.tickers) != 1 || cfg.tickers[0] != "MSFT" {
		t.Fatalf("tickers = %#v, want MSFT", cfg.tickers)
	}

	if cfg.from.Format(dateLayout) != "1986-03-13" ||
		cfg.to.Format(dateLayout) != "2026-10-25" {
		t.Fatalf("range = %s through %s", cfg.from, cfg.to)
	}
}

func TestValidateManifestNormalizesMembers(t *testing.T) {
	manifest := constituentManifest{
		Name:   " us-gaap ",
		AsOf:   "2026-09-14",
		Source: " fixture ",
		Members: []member{{
			CIK: " 0000789019 ", Ticker: " msft ", Name: " Microsoft ",
		}},
	}

	if err := validateManifest(&manifest); err != nil {
		t.Fatalf("validate manifest: %v", err)
	}

	if manifest.Name != "us-gaap" || manifest.Source != "fixture" ||
		manifest.Members[0].Ticker != "MSFT" ||
		manifest.Members[0].CIK != "0000789019" {
		t.Fatalf("manifest was not normalized: %+v", manifest)
	}
}

func assertCandidateRows(t *testing.T, path string, wantRows int) {
	t.Helper()

	records := readCSV(t, path)
	if len(records) != wantRows+1 {
		t.Fatalf("CSV data row count = %d, want %d", len(records)-1, wantRows)
	}

	seen := make(map[string]struct{}, wantRows)

	for _, record := range records[1:] {
		key := record[1] + ":" + record[3]
		if _, duplicate := seen[key]; duplicate {
			t.Fatalf("duplicate candidate row %s", key)
		}

		seen[key] = struct{}{}
	}
}

func assertNoDirectory(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected directory %s", path)
	}
}

func assertNoStagingDirectories(t *testing.T, path string) {
	t.Helper()

	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("read price directory: %v", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatalf("unexpected staging directory %s", entry.Name())
		}
	}
}

func testManifest() constituentManifest {
	return constituentManifest{
		Name:   "us-gaap-coverage",
		AsOf:   "2026-09-14",
		Source: "test",
		Members: []member{{
			CIK: "0000789019", Ticker: "MSFT", Name: "Microsoft Corporation",
		}},
	}
}

func priceSequence(rows ...candidateRow) priceRows {
	return func(yield func(candidateRow, error) bool) {
		for _, row := range rows {
			if !yield(row, nil) {
				return
			}
		}
	}
}

func readCSV(t *testing.T, path string) [][]string {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open CSV: %v", err)
	}
	defer func() { _ = file.Close() }()

	records, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatalf("read CSV: %v", err)
	}

	return records
}
