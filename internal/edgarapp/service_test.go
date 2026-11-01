package edgarapp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"wingman.com/chopstick/internal/appdata"
	"wingman.com/chopstick/internal/edgar"
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

func TestResetDerivedStatePreservesRetainedInputs(t *testing.T) {
	root := t.TempDir()
	t.Setenv(appdata.EnvName, root)

	removed := []string{
		"parsed/filing.json",
		"validation/report.json",
		"tmp/artifact.txt",
		"cache/indexes/master.tsv",
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
