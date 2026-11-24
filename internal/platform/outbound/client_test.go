package outbound

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientRewritesRequestToGateway(t *testing.T) {
	var received *http.Request

	gateway := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter, request *http.Request,
	) {
		received = request.Clone(request.Context())

		response.WriteHeader(http.StatusOK)
	}))
	defer gateway.Close()

	client := newClient(time.Second, gateway.URL)

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		"https://api.example.com/path?value=1", nil)
	if err != nil {
		t.Fatal(err)
	}

	request.Header.Set("User-Agent", "outbound-test")

	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	}()

	if received == nil {
		t.Fatal("gateway did not receive request")
	}

	if received.Host != "api.example.com" {
		t.Errorf("Host = %q, want api.example.com", received.Host)
	}

	if received.URL.RequestURI() != "/path?value=1" {
		t.Errorf("request URI = %q", received.URL.RequestURI())
	}

	if received.Header.Get("User-Agent") != "outbound-test" {
		t.Errorf("User-Agent = %q", received.Header.Get("User-Agent"))
	}
}

func TestClientAuthenticatesToGateway(t *testing.T) {
	t.Setenv(GatewayUserEnv, "generated-user")
	t.Setenv(GatewayPassEnv, "generated-password")

	var received *http.Request

	gateway := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter, request *http.Request,
	) {
		received = request.Clone(request.Context())

		response.WriteHeader(http.StatusOK)
	}))
	defer gateway.Close()

	client := newClient(time.Second, gateway.URL)

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		"https://api.example.com/value", nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()

	if received == nil {
		t.Fatal("gateway did not receive request")
	}

	user, password, ok := received.BasicAuth()
	if !ok || user != "generated-user" || password != "generated-password" {
		t.Fatalf("gateway Basic Auth = %q, %q, %t", user, password, ok)
	}
}

func TestClientRejectsPartialGatewayCredentials(t *testing.T) {
	t.Setenv(GatewayUserEnv, "generated-user")
	t.Setenv(GatewayPassEnv, "")

	client := newClient(time.Second, DefaultGatewayURL)

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		"https://api.example.com/value", nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := client.Do(request)
	if err == nil {
		_ = response.Body.Close()
		t.Fatal("request succeeded with partial gateway credentials")
	}

	if !strings.Contains(err.Error(), "must be configured together") {
		t.Fatalf("request error = %v", err)
	}
}

func TestClientRetriesTooManyRequests(t *testing.T) {
	var attempts atomic.Int32

	gateway := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter, _ *http.Request,
	) {
		if attempts.Add(1) < 3 {
			response.WriteHeader(http.StatusTooManyRequests)

			return
		}

		response.WriteHeader(http.StatusOK)
	}))
	defer gateway.Close()

	gatewayURL, err := url.Parse(gateway.URL)
	if err != nil {
		t.Fatal(err)
	}

	client := &http.Client{Transport: &retryTransport{
		next: &gatewayTransport{
			gatewayURL: gatewayURL, next: directTransport(),
		},
		maxRetries: 3, baseDelay: time.Millisecond, maxDelay: time.Second,
		jitter: func(time.Duration) time.Duration { return 0 },
	}}

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		"https://api.example.com/value", nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	}()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}

	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d, want 3", attempts.Load())
	}
}

func TestClientReturnsFinalTooManyRequests(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter, _ *http.Request,
	) {
		response.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(response, "slow down")
	}))
	defer gateway.Close()

	gatewayURL, err := url.Parse(gateway.URL)
	if err != nil {
		t.Fatal(err)
	}

	client := &http.Client{Transport: &retryTransport{
		next: &gatewayTransport{
			gatewayURL: gatewayURL, next: directTransport(),
		},
		maxRetries: 1, baseDelay: time.Millisecond, maxDelay: time.Second,
		jitter: func(time.Duration) time.Duration { return 0 },
	}}

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		"https://api.example.com/value", nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	}()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}

	if response.StatusCode != http.StatusTooManyRequests ||
		string(body) != "slow down" {
		t.Fatalf("status = %d, body = %q", response.StatusCode, body)
	}
}

func TestClientNeverFallsBackToDestination(t *testing.T) {
	client := newClient(time.Second, "http://127.0.0.1:1")

	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		"https://example.com/should-not-be-contacted", nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := client.Do(request)
	if response != nil {
		defer func() {
			if closeErr := response.Body.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		}()
	}

	if err == nil {
		t.Fatal("request succeeded with unavailable gateway")
	}

	if !strings.Contains(err.Error(), "127.0.0.1:1") {
		t.Fatalf("error = %q, want gateway address", err)
	}
}

func TestClientRejectsDirectHTTPDestination(t *testing.T) {
	client := newClient(time.Second, DefaultGatewayURL)

	request, err := http.NewRequestWithContext(context.Background(),
		http.MethodGet, "http://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}

	response, err := client.Do(request)
	if response != nil {
		defer func() {
			if closeErr := response.Body.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		}()
	}

	if err == nil || !strings.Contains(err.Error(), "only supports HTTPS") {
		t.Fatalf("error = %v", err)
	}
}

func TestParseGatewayURLRejectsUnsafeValues(t *testing.T) {
	for _, value := range []string{
		"", "ftp://127.0.0.1:8090", "http://", "http://user@host",
		"http://host/path", "http://host?query=1", "http://host#fragment",
	} {
		t.Run(value, func(t *testing.T) {
			if _, err := parseGatewayURL(value); err == nil {
				t.Fatalf("parseGatewayURL(%q) succeeded", value)
			}
		})
	}
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, time.November, 16, 12, 0, 0, 0, time.UTC)
	if got := retryAfter("3", now); got != 3*time.Second {
		t.Fatalf("seconds delay = %s, want 3s", got)
	}

	when := now.Add(5 * time.Second).Format(http.TimeFormat)
	if got := retryAfter(when, now); got != 5*time.Second {
		t.Fatalf("date delay = %s, want 5s", got)
	}
}
