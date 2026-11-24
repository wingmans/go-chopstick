package chopstick

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"wingman.com/chopstick/internal/edgar/pipeline"
	"wingman.com/chopstick/internal/platform/appdata"
	"wingman.com/chopstick/internal/platform/outbound"
)

func TestSetupProvisionsWithoutStartupCreatingResources(t *testing.T) {
	t.Setenv(appdata.EnvName, t.TempDir())
	t.Setenv("EDGAR_SET", "")

	gateway := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer gateway.Close()

	t.Setenv(outbound.GatewayURLEnv, gateway.URL)
	t.Setenv(outbound.GatewayUserEnv, "user")
	t.Setenv(outbound.GatewayPassEnv, "pass")
	t.Setenv("EDGAR_NATS_URL", startTestJetStream(t))

	config, err := ConfigFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	logger := slog.New(slog.DiscardHandler)

	err = Run(ctx, config, logger)
	if err == nil || !strings.Contains(err.Error(), "hint:   ./bin/chop setup") ||
		!strings.Contains(err.Error(), pipeline.StreamName) {
		t.Fatalf("startup error = %v", err)
	}

	if _, err := os.Stat(config.PollingDB); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("startup created polling database: %v", err)
	}

	bus, err := pipeline.Inspect(ctx, config.NATSURL)
	if err != nil {
		t.Fatal(err)
	}
	defer bus.CloseImmediately()

	if _, err := bus.JetStream().StreamInfo(pipeline.StreamName); !errors.Is(err, nats.ErrStreamNotFound) {
		t.Fatalf("startup created JetStream stream: %v", err)
	}

	if _, err := bus.JetStream().AddStream(&nats.StreamConfig{
		Name: pipeline.StreamName, Subjects: []string{"edgar.filing.>"},
	}); err != nil {
		t.Fatal(err)
	}

	if err := Setup(ctx, config); err == nil || !strings.Contains(err.Error(), "captures [edgar.filing.>]") {
		t.Fatalf("expected setup to report incompatible stream: %v", err)
	}

	if _, err := os.Stat(config.PollingDB); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("incompatible stream allowed setup to create polling database: %v", err)
	}

	if err := bus.JetStream().DeleteStream(pipeline.StreamName); err != nil {
		t.Fatal(err)
	}

	if err := Setup(ctx, config); err != nil {
		t.Fatalf("setup: %v", err)
	}

	before, err := bus.JetStream().ConsumerInfo(pipeline.StreamName, pipeline.NewsDurable)
	if err != nil {
		t.Fatal(err)
	}

	if err := Setup(ctx, config); err != nil {
		t.Fatalf("repeat setup: %v", err)
	}

	after, err := bus.JetStream().ConsumerInfo(pipeline.StreamName, pipeline.NewsDurable)
	if err != nil || !before.Created.Equal(after.Created) {
		t.Fatalf("repeat setup replaced news consumer: before=%v after=%v error=%v", before, after, err)
	}

	if err := CheckInfrastructure(ctx, config); err != nil {
		t.Fatalf("check infrastructure: %v", err)
	}

	verifyViewerStarts(t, config, logger)
}

func verifyViewerStarts(t *testing.T, config Config, logger *slog.Logger) {
	t.Helper()

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	config.HTTPAddress = listener.Addr().String()
	config.CommandAddress = "127.0.0.1:0"

	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	finished := make(chan error, 1)
	go func() { finished <- Run(ctx, config, logger) }()

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case err := <-finished:
			t.Fatalf("daemon exited before serving the viewer: %v", err)
		case <-ctx.Done():
			t.Fatal("viewer did not start with the gateway unavailable")
		case <-ticker.C:
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+config.HTTPAddress+"/", nil)
			if err != nil {
				t.Fatal(err)
			}

			response, err := http.DefaultClient.Do(request)
			if err != nil {
				continue
			}
			_ = response.Body.Close()

			if response.StatusCode != http.StatusOK {
				t.Fatalf("viewer returned HTTP %d", response.StatusCode)
			}

			cancel()

			if err := <-finished; err != nil {
				t.Fatalf("stop daemon: %v", err)
			}

			return
		}
	}
}

func startTestJetStream(t *testing.T) string {
	t.Helper()

	const binary = "../../.tools/bin/nats-server"

	if _, err := os.Stat(binary); errors.Is(err, os.ErrNotExist) {
		t.Skip("local nats-server is unavailable; install it with make deps")
	} else if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()

	path, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	server := exec.CommandContext(ctx, binary, "-js", "-a", "127.0.0.1", "-p", "-1",
		"-sd", "stream", "--ports_file_dir", ".")
	server.Path = path

	server.Dir = dir
	if err := server.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}

	t.Cleanup(func() {
		cancel()

		_ = server.Wait()
	})

	portFile := filepath.Join(dir, "nats-server_"+strconv.Itoa(server.Process.Pid)+".ports")

	ready, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for {
		var ports struct {
			NATS []string `json:"nats"`
		}

		contents, err := os.ReadFile(portFile)
		if err == nil && json.Unmarshal(contents, &ports) == nil && len(ports.NATS) > 0 {
			return ports.NATS[0]
		}

		select {
		case <-ready.Done():
			t.Fatalf("read NATS ports file %s: %v", portFile, err)
		case <-ticker.C:
		}
	}
}
