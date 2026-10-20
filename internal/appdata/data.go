// Package appdata resolves paths in the application's data directory.
package appdata

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	// EnvName configures the directory containing all application data.
	EnvName = "CHOPSTICK_DATA_DIR"

	// DefaultRoot is the project-local application data directory.
	DefaultRoot = "data"
)

// Root returns the configured application data directory.
func Root() string {
	root := strings.TrimSpace(os.Getenv(EnvName))
	if root == "" {
		return DefaultRoot
	}

	return filepath.Clean(root)
}

// Path returns a path below the configured application data directory.
func Path(elements ...string) string {
	return filepath.Join(append([]string{Root()}, elements...)...)
}
