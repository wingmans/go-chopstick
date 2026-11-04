package yahoo

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	marketstore "wingman.com/chopstick/internal/market/store"
)

func TestManifestDerivesDistinctShareClassIDs(t *testing.T) {
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

	first := manifest.Members[0].SecurityID

	second := manifest.Members[1].SecurityID

	if first == second || first == "" || second == "" {
		t.Fatalf("derived security IDs = %q and %q", first, second)
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
		ID: "0000789019", Symbol: "MSFT", Name: "Microsoft Corporation",
	}

	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	if err := store.RegisterSecurities(t.Context(), providerYahoo, []marketstore.Security{security}, now); err != nil {
		t.Fatalf("register security: %v", err)
	}

	client := testYahooClient(server)

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
