package operator

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wingman.com/chopstick/internal/chopstick"
	"wingman.com/chopstick/internal/edgar/pipeline"
	"wingman.com/chopstick/internal/platform/appdata"
	"wingman.com/chopstick/internal/platform/outbound"
	"wingman.com/chopstick/internal/platform/transport"
	"wingman.com/chopstick/internal/platform/xerr"
)

func TestClassifyErrorIdentifiesNATSPublishTimeout(t *testing.T) {
	t.Parallel()

	cause := fmt.Errorf("discover stage: %w", &pipeline.PublishAckTimeoutError{
		Subject: "sec.filing.discovered.4",
		Timeout: 10 * time.Second,
	})
	err := ClassifyError(t.Context(), cause)

	var classified *xerr.Error
	if !errors.As(err, &classified) {
		t.Fatalf("ClassifyError() error = %T, want *xerr.Error", err)
	}

	if classified.Code != xerr.Unavailable ||
		classified.Reason != "NATS_PUBLISH_TIMEOUT" ||
		classified.Message != "NATS publication timed out" {
		t.Fatalf("ClassifyError() = %+v", classified)
	}
}

func TestPathSizeAndFormatBytes(t *testing.T) {
	directory := t.TempDir()

	path := filepath.Join(directory, "payload")
	if err := os.WriteFile(path, make([]byte, 1536), 0o600); err != nil {
		t.Fatal(err)
	}

	bytes, err := pathSize(directory)
	if err != nil {
		t.Fatalf("pathSize returned error: %v", err)
	}

	if bytes != 1536 {
		t.Fatalf("pathSize = %d, want 1536", bytes)
	}

	if got := formatBytes(bytes); got != "1.5 KiB" {
		t.Fatalf("formatBytes = %q, want %q", got, "1.5 KiB")
	}
}

func TestDoctorStorageShowsConfiguredPathsAndAlignedSizes(t *testing.T) {
	t.Setenv(appdata.EnvName, t.TempDir())

	config, err := chopstick.ConfigForSet(new(""))
	if err != nil {
		t.Fatal(err)
	}

	config.AnalysisDB = appdata.Path("custom analysis.db")
	if err := os.WriteFile(config.AnalysisDB, make([]byte, 1536), 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	printStorageUsage(&output, DoctorConfig{Config: config})

	lines := strings.Split(strings.TrimRight(output.String(), "\n"), "\n")
	for _, line := range lines[2:] {
		if len(line) != len(lines[1]) {
			t.Errorf("storage size column is not aligned: %q", line)
		}
	}

	report := output.String()
	for _, want := range []string{config.AnalysisDB, "1.5 KiB", "not created"} {
		if !strings.Contains(report, want) {
			t.Errorf("storage report missing %q:\n%s", want, report)
		}
	}

	if strings.Contains(report, "1536 bytes") {
		t.Errorf("storage report includes a raw byte count:\n%s", report)
	}
}

func TestConfigurationReportsMissingSet(t *testing.T) {
	t.Setenv(appdata.EnvName, t.TempDir())
	t.Setenv(edgarSetEnv, "missing")

	if _, err := chopstick.ConfigFromEnvironment(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("resolve configuration: got %v, want missing set error", err)
	}
}

func TestDoctorRepairDryRunReportsSettingsSeparately(t *testing.T) {
	t.Setenv(appdata.EnvName, t.TempDir())
	t.Setenv("EDGAR_SET", "other-set")

	config, settingsErr := chopstick.ConfigForSet(new("missing"))
	config.NATSURL = "nats://127.0.0.1:0"

	var output bytes.Buffer
	if err := RepairDoctor(t.Context(), &output, config, true, true, settingsErr); err == nil {
		t.Fatal("expected invalid settings to block repair")
	}

	report := output.String()
	for _, line := range []string{
		"Infrastructure\n  Would create polling SQLite database",
		"Settings\n  Constituent set: missing (from --set)",
		"Invalid: constituent set",
		"Dry run. No repairs applied.",
	} {
		if !strings.Contains(report, line) {
			t.Errorf("report missing %q:\n%s", line, report)
		}
	}

	if _, err := os.Stat(config.PollingDB); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run created a database: %v", err)
	}
}

func TestUpstreamProbeReportsHTTPStatusThroughGateway(t *testing.T) {
	t.Setenv(appdata.EnvName, t.TempDir())
	t.Setenv(outbound.GatewayUserEnv, "probe-user")
	t.Setenv(outbound.GatewayPassEnv, "probe-password")

	gateway := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		user, password, ok := request.BasicAuth()
		if !ok || user != "probe-user" || password != "probe-password" || request.Host != "configured.example" {
			response.WriteHeader(http.StatusUnauthorized)

			return
		}

		if request.URL.Path == "/unavailable" {
			response.WriteHeader(http.StatusBadGateway)

			return
		}

		response.WriteHeader(http.StatusOK)
	}))
	defer gateway.Close()

	t.Setenv(outbound.GatewayURLEnv, gateway.URL)

	report := doctorReport{}
	checkUpstreams(t.Context(), &report, []string{
		"https://configured.example/ready",
		"https://configured.example/unavailable",
	}, "test admin@example.com")

	if report.ok != 1 || report.failures != 1 {
		t.Fatalf("upstream probe report = %+v", report)
	}
}

func TestSubmitIndexImportSendsTypedRequest(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(),
		"tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()

	received := make(chan transport.Request, 1)
	serverErr := make(chan error, 1)

	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErr <- acceptErr

			return
		}
		defer func() { _ = connection.Close() }()

		payload, readErr := io.ReadAll(connection)
		if readErr != nil {
			serverErr <- readErr

			return
		}

		var request transport.Request
		if decodeErr := json.Unmarshal(payload, &request); decodeErr != nil {
			serverErr <- decodeErr

			return
		}

		received <- request

		_, serverErrValue := connection.Write(
			[]byte(`{"index_import":{"accepted":true}}`))
		serverErr <- serverErrValue
	}()

	err = SubmitIndexImport(t.Context(), IndexImportConfig{
		Address: listener.Addr().String(), File: "/tmp/2025-QTR1.tsv",
		Latest: false, Year: 2025, FromYear: 0, ToYear: 0, DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	request := <-received
	if request.Command != transport.CommandImportIndex ||
		request.IndexImport == nil || request.IndexImport.Year != 2025 ||
		!request.IndexImport.DryRun {
		t.Fatalf("request = %+v", request)
	}

	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}
