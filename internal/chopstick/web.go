package chopstick

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"wingman.com/chopstick/internal/appdata"
	pipeline "wingman.com/chopstick/internal/chopstick/pipeline"
	"wingman.com/chopstick/internal/news"
	"wingman.com/chopstick/internal/web"
)

func runWeb(ctx context.Context, bus *pipeline.NATS, config Config,
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

	application.MountNews(web.NewNewsServer(
		store, newsDownloadRequester{publisher: bus}, logger,
	))

	consumerErr := make(chan error, 1)
	go func() {
		consumerErr <- pipeline.ProjectNews(
			roleCtx, bus, store, replay, logger,
		)
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

type newsDownloadRequester struct {
	publisher pipeline.Publisher
}

func (r newsDownloadRequester) RequestDownload(ctx context.Context,
	item news.Item,
) error {
	selected := true
	event := pipeline.Event{
		SchemaVersion:   pipeline.EventSchemaVersion,
		EventID:         "filing.requested:" + item.AccessionNumber,
		AccessionNumber: item.AccessionNumber,
		CIK:             item.CIK,
		CompanyName:     item.CompanyName,
		FormType:        item.FormType,
		FilingDate:      item.FilingDate,
		FilingPath:      item.FilingPath,
		FilingURL:       item.FilingURL,
		SourceFeed:      item.SourceFeed,
		Timestamp:       time.Now().UTC(),
		Selected:        &selected,
		SelectionReason: "requested from the news feed",
		SelectionMode:   "on-demand",
		SelectionSet:    item.SelectionSet,
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}

	return r.publisher.Publish(ctx,
		pipeline.SubjectFor(pipeline.SelectedSubject, event.FormType),
		event.EventID, payload)
}
