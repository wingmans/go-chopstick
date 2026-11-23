package envfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPreservesExistingEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("EXISTING=file\nNEW_VALUE='from file'\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("EXISTING", "shell")

	if err := os.Unsetenv("NEW_VALUE"); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.Unsetenv("NEW_VALUE") })

	if err := Load(path); err != nil {
		t.Fatal(err)
	}

	if value := os.Getenv("EXISTING"); value != "shell" {
		t.Fatalf("EXISTING = %q", value)
	}

	if value := os.Getenv("NEW_VALUE"); value != "from file" {
		t.Fatalf("NEW_VALUE = %q", value)
	}
}

func TestLoadAllowsMissingFile(t *testing.T) {
	if err := Load(filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Fatal(err)
	}
}
