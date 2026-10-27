package analysisstore

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
	analysisSchemaVersion    = 3
	analysisSchemaRetryLimit = 20
	analysisSchemaRetryDelay = 25 * time.Millisecond
)

//go:embed sql/schema.sql
var analysisSchema string

func createSchema(ctx context.Context, db *sql.DB) error {
	for attempt := 0; ; attempt++ {
		err := applySchema(ctx, db)
		if err == nil || !analysisSchemaIsLocked(err) ||
			attempt == analysisSchemaRetryLimit {
			return err
		}

		timer := time.NewTimer(analysisSchemaRetryDelay)

		select {
		case <-ctx.Done():
			timer.Stop()

			return fmt.Errorf("wait to initialize analysis schema: %w",
				ctx.Err())
		case <-timer.C:
		}
	}
}

func applySchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin analysis schema transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(
		&version,
	); err != nil {
		return fmt.Errorf("read analysis schema version: %w", err)
	}

	if version == analysisSchemaVersion {
		return tx.Commit()
	}

	if version != 0 {
		return outdatedSchemaError(version)
	}

	var tables int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'table'
		  AND name NOT LIKE 'sqlite_%'`).Scan(&tables); err != nil {
		return fmt.Errorf("inspect analysis schema: %w", err)
	}

	if tables != 0 {
		return outdatedSchemaError(version)
	}

	if _, err := tx.ExecContext(ctx, analysisSchema); err != nil {
		return fmt.Errorf("create analysis schema: %w", err)
	}

	if _, err := tx.ExecContext(ctx, "PRAGMA user_version = 3"); err != nil {
		return fmt.Errorf("record analysis schema version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit analysis schema: %w", err)
	}

	return nil
}

func analysisSchemaIsLocked(err error) bool {
	var sqliteError sqlite3.Error
	if !errors.As(err, &sqliteError) {
		return false
	}

	return sqliteError.Code == sqlite3.ErrBusy ||
		sqliteError.Code == sqlite3.ErrLocked
}

func outdatedSchemaError(version int) error {
	return fmt.Errorf(
		"analysis database schema version is %d, want %d; "+
			"run 'make rebuild-edgar'",
		version,
		analysisSchemaVersion,
	)
}
