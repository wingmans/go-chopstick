package main

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnsureMasterIndexSkipsExistingFile(t *testing.T) {
	t.Parallel()

	masterPath := filepath.Join(t.TempDir(), "master.tsv")
	if err := os.WriteFile(masterPath, []byte("existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (
		*http.Response, error,
	) {
		t.Fatal("existing master index triggered a network request")

		return nil, errors.New("unexpected network request")
	})}
	logger := slog.New(slog.DiscardHandler)

	if err := ensureMasterIndex(t.Context(), client, eventCommandConfig{
		masterPath: masterPath,
	}, logger); err != nil {
		t.Fatal(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func TestNextRoleRetryDelay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		current  time.Duration
		run      time.Duration
		expected time.Duration
	}{
		{name: "increase", current: time.Second, expected: 2 * time.Second},
		{
			name: "cap", current: 20 * time.Second,
			expected: maximumRoleRetryDelay,
		},
		{
			name: "reset after stable run", current: maximumRoleRetryDelay,
			run: stableRoleRun, expected: initialRoleRetryDelay,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			actual := nextRoleRetryDelay(test.current, test.run)
			if actual != test.expected {
				t.Fatalf("delay = %s, want %s", actual, test.expected)
			}
		})
	}
}

func (fn roundTripFunc) RoundTrip(request *http.Request) (
	*http.Response, error,
) {
	return fn(request)
}
