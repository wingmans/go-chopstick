package appdata

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestAcquireLockRejectsSecondOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chopd.lock")

	first, err := AcquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()

	if _, err := AcquireLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("second AcquireLock returned %v", err)
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock after release returned %v", err)
	}
	defer func() { _ = second.Close() }()
}
