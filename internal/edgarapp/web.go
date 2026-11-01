package edgarapp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"wingman.com/chopstick/internal/appdata"
	"wingman.com/chopstick/internal/eventbus"
	"wingman.com/chopstick/internal/news"
	"wingman.com/chopstick/internal/web"
)

func runWeb(ctx context.Context, bus *eventbus.NATS, config Config,
	logger *slog.Logger,
) error {
	roleCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	store, replay, err := news.Open(roleCtx, config.NewsDB)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	application, err := web.NewConfiguredServerWithDatabases(roleCtx,
		config.ParsedDirectory, config.AnalysisDB, config.OwnershipDB,
		appdata.Path("sets"), config.SetName, logger)
	if err != nil {
		return err
	}
	defer func() { _ = application.Close() }()

	application.MountNews(web.NewNewsServer(store, bus, logger))

	consumerErr := make(chan error, 1)
	go func() {
		consumerErr <- news.Consume(roleCtx, bus, store, replay, logger)
	}()

	server := &http.Server{
		Addr: config.HTTPAddress, Handler: application,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() { serverErr <- server.ListenAndServe() }()

	var result error

	waitForConsumer := false

	select {
	case <-ctx.Done():
	case result = <-consumerErr:
	case err := <-serverErr:
		waitForConsumer = true

		if !errors.Is(err, http.ErrServerClosed) {
			result = err
		}
	}

	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(
		context.WithoutCancel(ctx), newsShutdownTimeout,
	)
	defer shutdownCancel()

	shutdownErr := server.Shutdown(shutdownCtx)

	if waitForConsumer {
		select {
		case consumerResult := <-consumerErr:
			if result == nil && !errors.Is(consumerResult, context.Canceled) {
				result = consumerResult
			}
		case <-time.After(newsShutdownTimeout):
			bus.CloseImmediately()

			consumerResult := <-consumerErr
			if result == nil && !errors.Is(consumerResult, context.Canceled) {
				result = consumerResult
			}
		}
	} else {
		serverResult := <-serverErr
		if result == nil && ctx.Err() == nil &&
			!errors.Is(serverResult, http.ErrServerClosed) {
			result = serverResult
		}
	}

	if result != nil {
		return errors.Join(result, shutdownErr)
	}

	if shutdownErr != nil && !errors.Is(shutdownErr, http.ErrServerClosed) {
		return shutdownErr
	}

	return nil
}
