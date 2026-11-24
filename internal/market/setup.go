package market

import (
	"context"
	"fmt"
	"os"
	"strings"

	marketstore "wingman.com/chopstick/internal/market/store"
	"wingman.com/chopstick/internal/platform/appdata"
	"wingman.com/chopstick/internal/platform/outbound"
	"wingman.com/chopstick/internal/platform/xerr"
)

// Setup creates local market storage without contacting NATS or Yahoo.
func Setup(ctx context.Context, database, directory string) error {
	batches, err := newCSVBatchStore(directory)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(batches.root, 0o750); err != nil {
		return fmt.Errorf("create batch directory %s: %w", batches.root, err)
	}

	store, err := marketstore.Open(ctx, database)
	if err != nil {
		return fmt.Errorf("initialize market database at %s: %w", database, err)
	}

	return store.Close()
}

func storageError(cfg config, problem string) error {
	command := "./bin/chop setup --market"
	if cfg.databasePath != appdata.Path("market.db") {
		command += " --database " + xerr.ShellArg(cfg.databasePath)
	}

	if cfg.batchDir != appdata.Path("market", "yahoo", "batches") {
		command += " --batch-dir " + xerr.ShellArg(cfg.batchDir)
	}

	return &xerr.PreflightError{
		Title: "Market storage is unavailable", Problems: []string{problem},
		Hints: []string{"Initialize the configured market storage:", "  " + command},
	}
}

func gatewayError(err error) error {
	report := &xerr.PreflightError{
		Title: "Outbound gateway is unavailable", Problems: []string{err.Error()},
	}

	address := strings.TrimSpace(os.Getenv(outbound.GatewayURLEnv))
	if address == "" || address == outbound.DefaultGatewayURL {
		report.Hints = []string{"Start the local gateway:", "  make gateway-start"}
	} else {
		report.Hints = []string{"Check " + outbound.GatewayURLEnv + " and its credentials."}
	}

	return report
}
