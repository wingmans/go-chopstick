package main

import (
	"path/filepath"
	"testing"
)

func TestDefaultConstituentOutputIsInDataSets(t *testing.T) {
	t.Setenv("CHOPSTICK_DATA_DIR", "")

	want := filepath.Join("data", "sets", "sp500.json")
	if got := defaultConstituentOutput(); got != want {
		t.Fatalf("default constituent output = %q, want %q", got, want)
	}
}

func TestDefaultConstituentOutputUsesConfiguredDataDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CHOPSTICK_DATA_DIR", root)

	want := filepath.Join(root, "sets", "sp500.json")
	if got := defaultConstituentOutput(); got != want {
		t.Fatalf("configured constituent output = %q, want %q", got, want)
	}
}
