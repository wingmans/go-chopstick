package news

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"time"

	"github.com/mattn/go-sqlite3"
)

const (
	newsSchemaVersion    = 1
	newsSchemaRetryLimit = 20
	newsSchemaRetryDelay = 25 * time.Millisecond
)

//go:embed sql/schema.sql
var newsSchema string

func createSchema(ctx context.Context, db *sql.DB) (bool, error) {
	for attempt := 0; ; attempt++ {
		created, err := applySchema(ctx, db)
		if err == nil || !newsSchemaIsLocked(err) ||
			attempt == newsSchemaRetryLimit {
			return created, err
		}

		timer := time.NewTimer(newsSchemaRetryDelay)

		select {
		case <-ctx.Done():
			timer.Stop()

			return false, fmt.Errorf("wait to initialize news schema: %w",
				ctx.Err())
		case <-timer.C:
		}
	}
}

func applySchema(ctx context.Context, db *sql.DB) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin news schema transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(
		&version,
	); err != nil {
		return false, fmt.Errorf("read news schema version: %w", err)
	}

	if version == newsSchemaVersion {
		return false, tx.Commit()
	}

	if version != 0 {
		return false, outdatedSchemaError(version)
	}

	var tables int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'table'
		  AND name NOT LIKE 'sqlite_%'`).Scan(&tables); err != nil {
		return false, fmt.Errorf("inspect news schema: %w", err)
	}

	if tables != 0 {
		return false, outdatedSchemaError(version)
	}

	if _, err := tx.ExecContext(ctx, newsSchema); err != nil {
		return false, fmt.Errorf("create news schema: %w", err)
	}

	if _, err := tx.ExecContext(ctx, "PRAGMA user_version = 1"); err != nil {
		return false, fmt.Errorf("record news schema version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit news schema: %w", err)
	}

	return true, nil
}

func newsSchemaIsLocked(err error) bool {
	var sqliteError sqlite3.Error
	if !errors.As(err, &sqliteError) {
		return false
	}

	return sqliteError.Code == sqlite3.ErrBusy ||
		sqliteError.Code == sqlite3.ErrLocked
}

func outdatedSchemaError(version int) error {
	return fmt.Errorf(
		"news database schema version is %d, want %d; "+
			"run 'make rebuild-edgar'",
		version,
		newsSchemaVersion,
	)
}
