package news

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"

	"wingman.com/fetch-ecb/internal/edgar"
	"wingman.com/fetch-ecb/internal/eventbus"
	"wingman.com/fetch-ecb/internal/pollingworker"
)

const DurableName = "edgar-news-v2"

// Consume maintains the disposable news database from discovery events.
func Consume(ctx context.Context, bus *eventbus.NATS, store *Store,
	replay bool, logger *slog.Logger,
) error {
	js := bus.JetStream()
	if replay {
		err := js.DeleteConsumer(eventbus.StreamName, DurableName)
		if err != nil && !errors.Is(err, nats.ErrConsumerNotFound) {
			return fmt.Errorf("reset news consumer: %w", err)
		}
	}

	subscription, err := js.PullSubscribe(
		eventbus.SelectionSubject+".>", DurableName,
		nats.BindStream(eventbus.StreamName),
		nats.ManualAck(),
		nats.AckExplicit(),
		nats.DeliverAll(),
	)
	if err != nil {
		return fmt.Errorf("subscribe to selection events: %w", err)
	}

	for {
		messages, err := subscription.Fetch(64, nats.MaxWait(time.Second))
		if errors.Is(err, nats.ErrTimeout) {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			continue
		}

		if err != nil {
			return fmt.Errorf("fetch discovery events: %w", err)
		}

		for _, message := range messages {
			var event pollingworker.FilingEvent
			if err := json.Unmarshal(message.Data, &event); err != nil {
				_ = message.Term()

				logger.Error("discarding invalid selection event", "error", err)

				continue
			}

			filed, err := time.Parse("2006-01-02", event.FilingDate)
			if err != nil {
				_ = message.Term()

				logger.Error("discarding event with invalid filing date",
					"filing_date", event.FilingDate,
					"accession", event.AccessionNumber)

				continue
			}

			if filed.Year() < edgar.EarliestPipelineYear {
				_ = message.Ack()

				continue
			}

			metadata, err := message.Metadata()
			if err != nil {
				return fmt.Errorf("read discovery metadata: %w", err)
			}

			if err := store.Apply(ctx, metadata.Sequence.Stream,
				event.EventID, event); err != nil {
				_ = message.Nak()

				return err
			}

			if err := message.Ack(); err != nil {
				return fmt.Errorf("acknowledge news event: %w", err)
			}
		}
	}
}
