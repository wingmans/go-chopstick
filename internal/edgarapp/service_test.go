package edgarapp

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"wingman.com/chopstick/internal/appdata"
	"wingman.com/chopstick/internal/edgar"
	"wingman.com/chopstick/internal/indeximport"
	"wingman.com/chopstick/internal/transport"
)

func TestConfigFromEnvironment(t *testing.T) {
	root := t.TempDir()
	t.Setenv(appdata.EnvName, root)
	t.Setenv("EDGAR_SET", "")
	t.Setenv("EDGAR_FORM_TYPES", "10-K,10-Q")
	t.Setenv("EDGAR_FROM_YEAR", "2022")
	t.Setenv("EDGAR_HTTP_ADDRESS", "127.0.0.1:9090")

	config, err := ConfigFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}

	if config.PollingDB != filepath.Join(root, "polling.db") {
		t.Fatalf("polling database = %q", config.PollingDB)
	}

	if config.HTTPAddress != "127.0.0.1:9090" {
		t.Fatalf("HTTP address = %q", config.HTTPAddress)
	}

	if config.FromYear != 2022 {
		t.Fatalf("from year = %d", config.FromYear)
	}

	if len(config.FormTypes) != 2 {
		t.Fatalf("form types = %v", config.FormTypes)
	}
}

func TestConfigRejectsInvalidServiceValues(t *testing.T) {
	config := validConfig(t)
	config.FromYear = edgar.EarliestPipelineYear - 1

	if err := config.Validate(); err == nil {
		t.Fatal("Validate accepted an invalid from year")
	}
}

func TestAdminHandlerRejectsUnknownCommand(t *testing.T) {
	handler := &adminHandler{}

	payload, err := json.Marshal(transport.Request{Command: "missing"})
	if err != nil {
		t.Fatal(err)
	}

	encoded := handler.Handle(context.Background(), payload)

	var response transport.Response
	if err := json.Unmarshal(encoded, &response); err != nil {
		t.Fatal(err)
	}

	if response.Error != `unknown command "missing"` {
		t.Fatalf("error = %q", response.Error)
	}
}

func TestAdminHandlerAcceptsOneBackgroundIndexImport(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})

	handler := &adminHandler{
		config: validConfig(t),
		logger: slog.New(slog.DiscardHandler),
		importer: func(_ context.Context, config indeximport.Config,
			_ *slog.Logger,
		) error {
			config.Progress(indeximport.Progress{
				Phase: "read", Files: 2, Examined: 10,
				Queued: 7, Duplicates: 3,
			})
			close(started)
			<-release
			close(finished)

			return nil
		},
	}

	request := transport.Request{
		Command: transport.CommandImportIndex,
		IndexImport: &transport.IndexImportRequest{
			Latest: true,
		},
	}
	encoded := handler.Handle(t.Context(), encodeRequest(t, request))

	response := decodeResponse(t, encoded)
	if response.IndexImport == nil || !response.IndexImport.Accepted {
		t.Fatalf("index import was not accepted: %s", response.Error)
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background import did not start")
	}

	if active := handler.activeIndexImport(); active == nil ||
		active.Phase != "read" ||
		active.Examined != 10 || active.Queued != 7 ||
		active.Duplicates != 3 {
		t.Fatalf("active operation = %+v", active)
	}

	busy := decodeResponse(t,
		handler.Handle(t.Context(), encodeRequest(t, request)))
	if busy.Error != "an index import is already running" {
		t.Fatalf("second import error = %q", busy.Error)
	}

	close(release)

	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("background import did not finish")
	}

	waitForNoActiveIndexImport(t, handler)
}

func TestAdminHandlerCancelsIndexImportWithDaemon(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	canceled := make(chan struct{})

	handler := &adminHandler{
		config: validConfig(t),
		logger: slog.New(slog.DiscardHandler),
		importer: func(ctx context.Context, _ indeximport.Config,
			_ *slog.Logger,
		) error {
			<-ctx.Done()
			close(canceled)

			return ctx.Err()
		},
	}

	request := transport.Request{
		Command: transport.CommandImportIndex,
		IndexImport: &transport.IndexImportRequest{
			Latest: true,
		},
	}

	response := decodeResponse(t,
		handler.Handle(ctx, encodeRequest(t, request)))
	if response.IndexImport == nil || !response.IndexImport.Accepted {
		t.Fatalf("index import was not accepted: %s", response.Error)
	}

	cancel()

	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("background import was not canceled")
	}

	waitForNoActiveIndexImport(t, handler)
}

func TestAdminHandlerReportsIndexStatus(t *testing.T) {
	config := validConfig(t)
	if err := os.MkdirAll(config.IndexDirectory, 0o750); err != nil {
		t.Fatal(err)
	}

	quarterly := filepath.Join(config.IndexDirectory, "2025-QTR1.tsv")
	if err := os.WriteFile(quarterly, []byte("quarter\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(config.MasterPath, []byte("quarter\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	handler := &adminHandler{config: config}
	response := decodeResponse(t, handler.Handle(t.Context(),
		encodeRequest(t, transport.Request{
			Command: transport.CommandIndexStatus,
		})))

	if response.Error != "" || response.IndexStatus == nil {
		t.Fatalf("index status response = %+v", response)
	}

	status := response.IndexStatus
	if status.Directory != config.IndexDirectory ||
		status.MasterPath != config.MasterPath ||
		status.MasterState != "current" ||
		len(status.AvailablePeriods) != 1 ||
		status.AvailablePeriods[0] != "2025-QTR1" {
		t.Fatalf("index status = %+v", status)
	}
}

func TestResetDerivedStatePreservesRetainedInputs(t *testing.T) {
	root := t.TempDir()
	t.Setenv(appdata.EnvName, root)

	removed := []string{
		"parsed/filing.json",
		"validation/report.json",
		"tmp/artifact.txt",
		"cache/indexes/master.tsv",
		"cache/indexes/import-state.json",
		"polling.db",
		"analysis.db-wal",
		"feed.db-shm",
	}
	preserved := []string{
		"cache/filings/submission.txt",
		"cache/indexes/quarterly/2026-QTR1.tsv",
		"cache/index-zips/2026-QTR1.zip",
		"sets/test.json",
		"ownership.db",
		"market.db",
	}

	for _, name := range append(removed, preserved...) {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := resetDerivedState(); err != nil {
		t.Fatal(err)
	}

	for _, name := range removed {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed, got %v", name, err)
		}
	}

	for _, name := range preserved {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("expected %s to be preserved: %v", name, err)
		}
	}
}

func validConfig(t *testing.T) Config {
	t.Helper()

	root := t.TempDir()

	return Config{
		NATSURL:           "nats://127.0.0.1:4222",
		CommandAddress:    transport.DefaultAddress,
		PollingDB:         filepath.Join(root, "polling.db"),
		NewsDB:            filepath.Join(root, "feed.db"),
		FilingsDirectory:  filepath.Join(root, "filings"),
		ParsedDirectory:   filepath.Join(root, "parsed"),
		AnalysisDB:        filepath.Join(root, "analysis.db"),
		OwnershipDB:       filepath.Join(root, "ownership.db"),
		MasterPath:        filepath.Join(root, "master.tsv"),
		IndexDirectory:    filepath.Join(root, "indexes"),
		IndexZipDirectory: filepath.Join(root, "index-zips"),
		UserAgent:         "test test@example.com",
		HTTPAddress:       "127.0.0.1:8083",
		Interval:          1,
		FormTypes:         []string{"10-K"},
		FromYear:          edgar.EarliestPipelineYear,
		MaxAttempts:       5,
	}
}

func encodeRequest(t *testing.T, request transport.Request) []byte {
	t.Helper()

	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}

	return payload
}

func decodeResponse(t *testing.T, payload []byte) transport.Response {
	t.Helper()

	var response transport.Response
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}

	return response
}

func waitForNoActiveIndexImport(t *testing.T, handler *adminHandler) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for handler.activeIndexImport() != nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	if active := handler.activeIndexImport(); active != nil {
		t.Fatalf("active operation was not cleared: %+v", active)
	}
}
