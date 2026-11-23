package mirror

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
	mirrorSchemaVersion    = 1
	mirrorSchemaRetryLimit = 20
	mirrorSchemaRetryDelay = 25 * time.Millisecond
)

//go:embed sql/schema.sql
var mirrorSchema string

func createCatalogSchema(ctx context.Context, db *sql.DB) error {
	for attempt := 0; ; attempt++ {
		err := applyCatalogSchema(ctx, db)
		if err == nil || !catalogSchemaIsLocked(err) ||
			attempt == mirrorSchemaRetryLimit {
			return err
		}

		timer := time.NewTimer(mirrorSchemaRetryDelay)

		select {
		case <-ctx.Done():
			timer.Stop()

			return fmt.Errorf("wait to initialize mirror catalog: %w",
				ctx.Err())
		case <-timer.C:
		}
	}
}

func applyCatalogSchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin mirror catalog schema transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(
		&version,
	); err != nil {
		return fmt.Errorf("read mirror catalog schema version: %w", err)
	}

	if version == mirrorSchemaVersion {
		return tx.Commit()
	}

	if version != 0 {
		return outdatedCatalogSchemaError(version)
	}

	var tables int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'table'
		  AND name NOT LIKE 'sqlite_%'`).Scan(&tables); err != nil {
		return fmt.Errorf("inspect mirror catalog schema: %w", err)
	}

	if tables != 0 {
		return outdatedCatalogSchemaError(version)
	}

	if _, err := tx.ExecContext(ctx, mirrorSchema); err != nil {
		return fmt.Errorf("create mirror catalog schema: %w", err)
	}

	if _, err := tx.ExecContext(ctx, "PRAGMA user_version = 1"); err != nil {
		return fmt.Errorf("record mirror catalog schema version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit mirror catalog schema: %w", err)
	}

	return nil
}

func catalogSchemaIsLocked(err error) bool {
	var sqliteError sqlite3.Error
	if !errors.As(err, &sqliteError) {
		return false
	}

	return sqliteError.Code == sqlite3.ErrBusy ||
		sqliteError.Code == sqlite3.ErrLocked
}

func outdatedCatalogSchemaError(version int) error {
	return fmt.Errorf(
		"%w: version is %d, want %d; rebuild the disposable catalog",
		ErrCatalogSchema,
		version,
		mirrorSchemaVersion,
	)
}
