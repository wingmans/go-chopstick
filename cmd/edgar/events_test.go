package main

import (
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
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

		return nil, nil
	})}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	if err := ensureMasterIndex(t.Context(), client, eventCommandConfig{
		masterPath: masterPath,
	}, logger); err != nil {
		t.Fatal(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (
	*http.Response, error,
) {
	return fn(request)
}
