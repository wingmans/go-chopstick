package main

import (
	"context"
	"errors"
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

	out := make(chan pw.FilingEvent, 256)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-out:
				logger.Debug("polling event emitted", "accession", ev.AccessionNumber,
					"cik", ev.CIK, "form", ev.FormType, "date", ev.FilingDate)
			}
		}
	}()

	fetcher := NewSECFetcher(*userAgent)

	store, err := pw.NewSQLiteStore(*dbPath)
	if err != nil {
		stop()
		log.Fatal(err)
	}

	worker := pw.NewWorker(fetcher, store, out, *interval)

	if err := worker.Run(ctx); err != nil &&
		!errors.Is(err, context.Canceled) {
		stop()

		_ = store.Close()

		log.Fatal(err)
	}

	_ = store.Close()

	stop()
	logger.Debug("polling service stopped")
}
