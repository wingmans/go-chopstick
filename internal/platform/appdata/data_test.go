package appdata

import "testing"

func TestPathUsesConfiguredRoot(t *testing.T) {
	t.Setenv(EnvName, "/srv/chopstick")

	if got := Path("cache", "filings"); got != "/srv/chopstick/cache/filings" {
		t.Fatalf("Path returned %q", got)
	}
}

func TestPathUsesDefaultRoot(t *testing.T) {
	t.Setenv(EnvName, "")

	if got := Path("polling.db"); got != "data/polling.db" {
		t.Fatalf("Path returned %q", got)
	}
}
