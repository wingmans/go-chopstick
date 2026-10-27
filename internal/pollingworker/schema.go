package pollingworker

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
	pollingSchemaVersion = 1
	schemaRetryLimit     = 20
	schemaRetryDelay     = 25 * time.Millisecond
)

//go:embed sql/schema.sql
var pollingSchema string

func createSchema(ctx context.Context, db *sql.DB) error {
	for attempt := 0; ; attempt++ {
		err := applySchema(ctx, db)
		if err == nil || !schemaIsLocked(err) || attempt == schemaRetryLimit {
			return err
		}

		timer := time.NewTimer(schemaRetryDelay)

		select {
		case <-ctx.Done():
			timer.Stop()

			return fmt.Errorf("wait to initialize polling schema: %w",
				ctx.Err())
		case <-timer.C:
		}
	}
}

func applySchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin polling schema transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(
		&version,
	); err != nil {
		return fmt.Errorf("read polling schema version: %w", err)
	}

	if version == pollingSchemaVersion {
		return tx.Commit()
	}

	if version != 0 {
		return outdatedSchemaError(version)
	}

	var tables int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'table'
		  AND name IN (
			'processed_accessions',
			'polling_dead_letters',
			'polling_outbox',
			'work_failures'
		  )`).Scan(&tables); err != nil {
		return fmt.Errorf("inspect polling schema: %w", err)
	}

	if tables != 0 {
		return outdatedSchemaError(version)
	}

	if _, err := tx.ExecContext(ctx, pollingSchema); err != nil {
		return fmt.Errorf("create polling schema: %w", err)
	}

	if _, err := tx.ExecContext(ctx, "PRAGMA user_version = 1"); err != nil {
		return fmt.Errorf("record polling schema version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit polling schema: %w", err)
	}

	return nil
}

func schemaIsLocked(err error) bool {
	var sqliteError sqlite3.Error
	if !errors.As(err, &sqliteError) {
		return false
	}

	return sqliteError.Code == sqlite3.ErrBusy ||
		sqliteError.Code == sqlite3.ErrLocked
}

func outdatedSchemaError(version int) error {
	return fmt.Errorf(
		"polling database schema version is %d, want %d; "+
			"run 'make rebuild-edgar'",
		version,
		pollingSchemaVersion,
	)
}
