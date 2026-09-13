package edgar

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEDGARClientGetSetsUserAgent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.Header.Get("User-Agent"), "Example contact@example.test"; got != want {
			t.Errorf("User-Agent = %q, want %q", got, want)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	response, err := newEDGARClient(server.Client(), "Example contact@example.test").get(t.Context(), server.URL)
	if err != nil {
		t.Fatalf("get returned error: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
}

func TestEDGARClientGetReturnsTypedHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	_, err := newEDGARClient(server.Client(),
		"Example contact@example.test").get(t.Context(), server.URL) //nolint:bodyclose // rejected responses are closed by edgarClient.get.

	upstreamError, ok := errors.AsType[*HTTPError](err)
	if !ok {
		t.Fatalf("get error = %v, want *HTTPError", err)
	}

	if upstreamError.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status code = %d, want %d", upstreamError.StatusCode, http.StatusTooManyRequests)
	}

	if upstreamError.URL != server.URL {
		t.Fatalf("URL = %q, want %q", upstreamError.URL, server.URL)
	}
}
