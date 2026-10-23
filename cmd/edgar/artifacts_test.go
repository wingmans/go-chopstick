package main

import (
	"flag"
	"testing"
)

func TestParseArtifactServeUsesLoopback(t *testing.T) {
	cfg, err := parseArtifactCommand([]string{
		"serve", "--cik", "1",
		"--accession", "test-accession",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.artifacts.address != "127.0.0.1:8090" {
		t.Fatalf("viewer address = %q", cfg.artifacts.address)
	}

	_, err = parseArtifactCommand([]string{
		"serve", "--cik", "1",
		"--accession", "test-accession",
		"--address", "0.0.0.0:8090",
	})
	if err != flag.ErrHelp {
		t.Fatalf("public viewer address error = %v, want flag.ErrHelp", err)
	}
}
