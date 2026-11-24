// Package sqlite contains shared helpers for opening provisioned databases.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ExistingDSN opens a database in read/write mode without creating the file
// or changing its journal mode.
func ExistingDSN(path, options string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}

	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular database file", path)
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	dsn := url.URL{
		Scheme: "file", Path: filepath.ToSlash(absolute),
		RawQuery: "mode=rw&" + strings.TrimPrefix(options, "?"),
	}

	return dsn.String(), nil
}

// CheckSchema verifies the on-disk schema version.
func CheckSchema(ctx context.Context, db *sql.DB, want int) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read database schema version: %w", err)
	}

	if version != want {
		return fmt.Errorf("database schema version is %d, want %d", version, want)
	}

	return nil
}
