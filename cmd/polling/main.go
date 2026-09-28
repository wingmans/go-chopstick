package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"wingman.com/fetch-ecb/internal/ctxlog"
	pw "wingman.com/fetch-ecb/internal/pollingworker"
)

func main() {
	interval := flag.Duration("interval", time.Hour, "poll interval")
	dbPath := flag.String("db-path", "./var/polling.db", "sqlite db path")
	userAgent := flag.String("user-agent", "", "SEC user-agent")

	flag.Parse()

	if *userAgent == "" {
		log.Fatal("-user-agent is required")
	}

	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	logger := ctxlog.New()
	ctx = ctxlog.WithLogger(ctx, logger)
	logger.Debug("polling service starting", "interval", interval.String(), "db_path", *dbPath)

	if err := pw.Run(ctx, pw.ServiceConfig{
		Interval:  *interval,
		DBPath:    *dbPath,
		UserAgent: *userAgent,
	}); err != nil {
		stop()

		log.Fatal(err)
	}

	stop()
	logger.Debug("polling service stopped")
}
