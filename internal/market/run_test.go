package market

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	sourceyahoo "wingman.com/chopstick/internal/ingest/yahoo"
	marketstore "wingman.com/chopstick/internal/market/store"
)

func TestManifestAllowsDistinctShareClassesForOneCIK(t *testing.T) {
	manifest := constituentManifest{
		Name: "index", AsOf: "2026-10-26", Source: "test",
		Members: []member{
			{CIK: "0001652044", Ticker: "GOOG", Name: "Alphabet Class C"},
			{CIK: "0001652044", Ticker: "GOOGL", Name: "Alphabet Class A"},
		},
	}

	if err := validateManifest(&manifest); err != nil {
		t.Fatalf("validate manifest: %v", err)
	}

	if manifest.Members[0].Ticker == manifest.Members[1].Ticker {
		t.Fatalf("share-class tickers were not distinct: %+v", manifest.Members)
	}
}

func TestExecuteFetchBootstrapsOneSecurity(t *testing.T) {
	date := mustDate(t, "2026-10-23")

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(response, yahooFixture([]int64{
			date.Add(14 * time.Hour).Unix(),
		}))
	}))
	defer server.Close()

	root := t.TempDir()

	store, err := marketstore.Open(context.Background(), filepath.Join(root, "market.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	defer func() { _ = store.Close() }()

	security := marketstore.Security{
		CIK: "0000789019", Symbol: "MSFT", Name: "Microsoft Corporation",
	}

	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	if err := store.RegisterSecurities(t.Context(), providerYahoo, []marketstore.Security{security}, now); err != nil {
		t.Fatalf("register security: %v", err)
	}

	client := sourceyahoo.NewClient(
		server.Client(), server.URL, slog.New(slog.DiscardHandler),
	)

	batches, err := newCSVBatchStore(filepath.Join(root, "batches"))
	if err != nil {
		t.Fatalf("create CSV batch store: %v", err)
	}

	cfg := config{
		from: mustDate(t, "2020-01-01"), to: date, maxSymbols: 1,
		databasePath: "test.db", batchDir: filepath.Join(root, "batches"),
	}

	var output bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&output, nil))

	err = executeFetch(
		t.Context(), &output, logger, store, batches, client,
		[]marketstore.Security{security}, cfg,
	)
	if err != nil {
		t.Fatalf("execute fetch: %v\n%s", err, output.String())
	}

	if !strings.Contains(output.String(), "completed 1") {
		t.Fatalf("output = %q", output.String())
	}

	batchPath := filepath.Join(
		root, "batches", "MSFT", "bootstrap_2020-01-01_2026-10-23.csv",
	)
	if _, err := os.Stat(batchPath); err != nil {
		t.Fatalf("inspect retained CSV batch: %v", err)
	}

	pending, err := store.PendingWork(
		t.Context(), providerYahoo, []marketstore.Security{security},
		cfg.from, cfg.to,
	)
	if err != nil {
		t.Fatalf("plan after bootstrap: %v", err)
	}

	if len(pending) != 0 {
		t.Fatalf("pending after bootstrap = %+v", pending)
	}
}

func yahooFixture(timestamps []int64) string {
	opens := make([]string, len(timestamps))
	closes := make([]string, len(timestamps))
	adjusted := make([]string, len(timestamps))
	formattedTimestamps := make([]string, len(timestamps))

	for index, timestamp := range timestamps {
		opens[index] = strconv.Itoa(25 + index)
		closes[index] = strconv.Itoa(26 + index)
		adjusted[index] = fmt.Sprintf("0.%02d", 6+index)
		formattedTimestamps[index] = strconv.FormatInt(timestamp, 10)
	}

	return fmt.Sprintf(`{
  "chart": {
    "result": [{
      "meta": {"currency": "USD", "symbol": "MSFT"},
      "timestamp": [%s],
      "indicators": {
        "quote": [{"open": [%s], "close": [%s]}],
        "adjclose": [{"adjclose": [%s]}]
      }
    }],
    "error": null
  }
}`,
		strings.Join(formattedTimestamps, ","),
		strings.Join(opens, ","),
		strings.Join(closes, ","),
		strings.Join(adjusted, ","),
	)
}
