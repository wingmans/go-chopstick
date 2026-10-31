package applock

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestAcquireRejectsSecondOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "edgard.lock")

	first, err := Acquire(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()

	if _, err := Acquire(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("second Acquire returned %v", err)
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Acquire(path)
	if err != nil {
		t.Fatalf("Acquire after release returned %v", err)
	}
	defer func() { _ = second.Close() }()
}
