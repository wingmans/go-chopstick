package edgarapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"

	"wingman.com/chopstick/internal/eventbus"
	"wingman.com/chopstick/internal/eventworker"
	filingwork "wingman.com/chopstick/internal/filing/work"
	"wingman.com/chopstick/internal/news"
	"wingman.com/chopstick/internal/transport"
)

type adminHandler struct {
	config    Config
	startedAt time.Time
}

// Handle decodes one local CLI command and delegates it to application logic.
func (h *adminHandler) Handle(ctx context.Context, payload []byte) []byte {
	var request transport.Request
	if err := json.Unmarshal(payload, &request); err != nil {
		return encodeCommandResponse(transport.Response{},
			fmt.Errorf("decode command: %w", err))
	}

	response := transport.Response{}

	var err error

	switch request.Command {
	case transport.CommandStatus:
		var status transport.Status

		status, err = h.status(ctx)
		response.Status = &status
	case transport.CommandListFailures:
		response.Failures, err = h.listFailures(ctx, request)
	case transport.CommandRetryFailures:
		response.Retried, err = h.retryFailures(ctx, request.Stage)
	default:
		err = fmt.Errorf("unknown command %q", request.Command)
	}

	return encodeCommandResponse(response, err)
}

func encodeCommandResponse(response transport.Response, responseErr error) []byte {
	if responseErr != nil {
		response.Error = responseErr.Error()
	}

	payload, err := json.Marshal(response)
	if err != nil {
		return []byte(`{"error":"encode command response"}`)
	}

	return payload
}

func (h *adminHandler) status(ctx context.Context) (transport.Status, error) {
	store, err := filingwork.Open(ctx, h.config.PollingDB)
	if err != nil {
		return transport.Status{}, err
	}
	defer func() { _ = store.Close() }()

	stats, err := store.Stats(ctx)
	if err != nil {
		return transport.Status{}, err
	}

	bus, err := eventbus.Inspect(h.config.NATSURL)
	if err != nil {
		return transport.Status{}, err
	}
	defer func() { _ = bus.Close() }()

	stream, err := bus.JetStream().StreamInfo(eventbus.StreamName)
	if err != nil {
		return transport.Status{}, err
	}

	result := transport.Status{
		StartedAt: h.startedAt, HTTPAddress: h.config.HTTPAddress,
		Pipeline: transport.PipelineStats{
			Accessions: stats.Accessions, OutboxPending: stats.OutboxPending,
			OutboxPublished: stats.OutboxPublished, Failures: stats.Failures,
		},
		StreamMessages: stream.State.Msgs, StreamBytes: stream.State.Bytes,
		Consumers: nil,
	}

	for _, durable := range []string{
		eventworker.SelectionDurable, eventworker.DownloadDurable,
		eventworker.ProcessDurable, eventworker.OwnershipDurable,
		news.DurableName,
	} {
		consumer, consumerErr := bus.JetStream().ConsumerInfo(
			eventbus.StreamName, durable,
		)
		if errors.Is(consumerErr, nats.ErrConsumerNotFound) {
			continue
		}

		if consumerErr != nil {
			return transport.Status{}, consumerErr
		}

		result.Consumers = append(result.Consumers, transport.ConsumerStatus{
			Name: durable, Pending: consumer.NumPending,
			InFlight:    consumer.NumAckPending,
			Redelivered: consumer.NumRedelivered,
		})
	}

	return result, nil
}

func (h *adminHandler) listFailures(ctx context.Context,
	request transport.Request,
) ([]transport.Failure, error) {
	if request.Limit <= 0 || request.Limit > 500 {
		request.Limit = 50
	}

	store, err := filingwork.Open(ctx, h.config.PollingDB)
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()

	rows, err := store.WorkFailures(ctx, request.Stage)
	if err != nil {
		return nil, err
	}

	if len(rows) > request.Limit {
		rows = rows[:request.Limit]
	}

	result := make([]transport.Failure, 0, len(rows))
	for _, row := range rows {
		result = append(result, transport.Failure{
			Stage: row.Stage, AccessionNumber: row.AccessionNumber,
			Attempts: row.Attempts, LastFailedAt: row.LastFailedAt,
			LastError: row.LastError,
		})
	}

	return result, nil
}

func (h *adminHandler) retryFailures(ctx context.Context,
	stage string,
) (int, error) {
	if stage != "download" && stage != "process" {
		return 0,
			errors.New("stage must be download or process")
	}

	store, err := filingwork.Open(ctx, h.config.PollingDB)
	if err != nil {
		return 0, err
	}
	defer func() { _ = store.Close() }()

	bus, err := eventbus.Connect(h.config.NATSURL)
	if err != nil {
		return 0, err
	}
	defer func() { _ = bus.Close() }()

	count, err := store.RetryWorkFailures(ctx, stage, bus)
	if err != nil {
		return 0, err
	}

	return count, nil
}
