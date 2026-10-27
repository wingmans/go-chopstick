package marketstore

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
	marketSchemaVersion    = 1
	marketSchemaRetryLimit = 20
	marketSchemaRetryDelay = 25 * time.Millisecond
)

//go:embed sql/schema.sql
var marketSchema string

func createSchema(ctx context.Context, db *sql.DB) error {
	for attempt := 0; ; attempt++ {
		err := applySchema(ctx, db)
		if err == nil || !marketSchemaIsLocked(err) ||
			attempt == marketSchemaRetryLimit {
			return err
		}

		timer := time.NewTimer(marketSchemaRetryDelay)

		select {
		case <-ctx.Done():
			timer.Stop()

			return fmt.Errorf("wait to initialize market schema: %w",
				ctx.Err())
		case <-timer.C:
		}
	}
}

func applySchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin market schema transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(
		&version,
	); err != nil {
		return fmt.Errorf("read market schema version: %w", err)
	}

	if version == marketSchemaVersion {
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
			'market_securities',
			'market_prices',
			'market_corporate_actions',
			'market_sync_state',
			'market_repairs'
		  )`).Scan(&tables); err != nil {
		return fmt.Errorf("inspect market schema: %w", err)
	}

	if tables != 0 {
		return outdatedSchemaError(version)
	}

	if _, err := tx.ExecContext(ctx, marketSchema); err != nil {
		return fmt.Errorf("create market schema: %w", err)
	}

	if _, err := tx.ExecContext(ctx, "PRAGMA user_version = 1"); err != nil {
		return fmt.Errorf("record market schema version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit market schema: %w", err)
	}

	return nil
}

func marketSchemaIsLocked(err error) bool {
	var sqliteError sqlite3.Error
	if !errors.As(err, &sqliteError) {
		return false
	}

	return sqliteError.Code == sqlite3.ErrBusy ||
		sqliteError.Code == sqlite3.ErrLocked
}

func outdatedSchemaError(version int) error {
	return fmt.Errorf(
		"market database schema version is %d, want %d; "+
			"run 'make rebuild-market'",
		version,
		marketSchemaVersion,
	)
}
