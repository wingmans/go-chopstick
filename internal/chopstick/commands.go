package chopstick

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"wingman.com/chopstick/internal/chopstick/ingest"
	pipeline "wingman.com/chopstick/internal/chopstick/pipeline"
	"wingman.com/chopstick/internal/transport"
)

type adminHandler struct {
	config    Config
	startedAt time.Time
	logger    *slog.Logger
	mu        sync.Mutex
	active    *transport.IndexImportStatus
	importer  func(context.Context, ingest.Config, *slog.Logger) error
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
	case transport.CommandImportIndex:
		var accepted bool

		accepted, err = h.startIndexImport(ctx, request.IndexImport)
		response.IndexImport = &transport.IndexImportResponse{
			Accepted: accepted,
		}
	case transport.CommandIndexStatus:
		response.IndexStatus, err = h.indexStatus()
	default:
		err = fmt.Errorf("unknown command %q", request.Command)
	}

	return encodeCommandResponse(response, err)
}

func (h *adminHandler) indexStatus() (*transport.IndexStatusResponse, error) {
	inventory, err := ingest.Inspect(
		h.config.IndexDirectory, h.config.MasterPath,
	)
	if err != nil {
		return nil, err
	}

	result := &transport.IndexStatusResponse{
		Directory: inventory.Directory, MasterPath: inventory.MasterPath,
		MasterState:      inventory.MasterState,
		AvailablePeriods: inventory.AvailablePeriods,
		MissingPeriods:   inventory.MissingPeriods,
		ImportedPeriods:  inventory.ImportedPeriods,
		ActiveImport:     h.activeIndexImport(),
	}

	if inventory.LastImport != nil {
		result.LastImport = &transport.IndexImportSummary{
			CommittedAt: inventory.LastImport.CommittedAt,
			Files:       inventory.LastImport.Files,
			Periods:     inventory.LastImport.Periods,
			Examined:    inventory.LastImport.Examined,
			Queued:      inventory.LastImport.Queued,
			Duplicates:  inventory.LastImport.Duplicates,
		}
	}

	return result, nil
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
	store, err := pipeline.Open(ctx, h.config.PollingDB)
	if err != nil {
		return transport.Status{}, err
	}
	defer func() { _ = store.Close() }()

	stats, err := store.Stats(ctx)
	if err != nil {
		return transport.Status{}, err
	}

	bus, err := pipeline.Inspect(h.config.NATSURL)
	if err != nil {
		return transport.Status{}, err
	}
	defer func() { _ = bus.Close() }()

	stream, err := bus.JetStream().StreamInfo(pipeline.StreamName)
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
		Consumers: nil, IndexImport: h.activeIndexImport(),
	}

	for _, durable := range []string{
		pipeline.SelectionDurable, pipeline.SubmissionDurable,
		pipeline.ExtractionDurable, pipeline.OwnershipDurable,
		pipeline.NewsDurable,
	} {
		consumer, consumerErr := bus.JetStream().ConsumerInfo(
			pipeline.StreamName, durable,
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

func (h *adminHandler) startIndexImport(ctx context.Context,
	request *transport.IndexImportRequest,
) (bool, error) {
	if request == nil {
		return false, errors.New("index import request is required")
	}

	config := ingest.Config{
		IndexesDirectory: h.config.IndexDirectory,
		MasterPath:       h.config.MasterPath,
		PollingDB:        h.config.PollingDB,
		NATSURL:          h.config.NATSURL,
		File:             request.File,
		Latest:           request.Latest,
		Year:             request.Year,
		FromYear:         request.FromYear,
		ToYear:           request.ToYear,
		DryRun:           request.DryRun,
		Progress:         nil,
	}
	if err := config.Validate(); err != nil {
		return false, err
	}

	now := time.Now().UTC()

	h.mu.Lock()
	if h.active != nil {
		h.mu.Unlock()

		return false, errors.New("an index import is already running")
	}

	h.active = &transport.IndexImportStatus{
		State: "accepted", Phase: "starting",
		StartedAt: now, UpdatedAt: now,
	}
	h.mu.Unlock()

	config.Progress = h.updateIndexImport

	go h.runIndexImport(ctx, config)

	return true, nil
}

func (h *adminHandler) runIndexImport(ctx context.Context,
	config ingest.Config,
) {
	logger := h.logger
	if logger == nil {
		logger = slog.Default()
	}

	logger = logger.With("role", "index-import")
	logger.Info("index import accepted")

	importer := h.importer
	if importer == nil {
		importer = ingest.RunIndex
	}

	err := importer(ctx, config, logger)
	if err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("index import failed", "error", err)
	} else if err == nil {
		logger.Info("index import complete")
	}

	h.mu.Lock()
	h.active = nil
	h.mu.Unlock()
}

func (h *adminHandler) updateIndexImport(progress ingest.Progress) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.active == nil {
		return
	}

	h.active.State = "running"
	h.active.Phase = progress.Phase
	h.active.UpdatedAt = time.Now().UTC()
	h.active.Files = progress.Files
	h.active.Examined = progress.Examined
	h.active.Queued = progress.Queued
	h.active.Duplicates = progress.Duplicates
}

func (h *adminHandler) activeIndexImport() *transport.IndexImportStatus {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.active == nil {
		return nil
	}

	active := *h.active

	return &active
}

func (h *adminHandler) listFailures(ctx context.Context,
	request transport.Request,
) ([]transport.Failure, error) {
	if request.Limit <= 0 || request.Limit > 500 {
		request.Limit = 50
	}

	store, err := pipeline.Open(ctx, h.config.PollingDB)
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

	store, err := pipeline.Open(ctx, h.config.PollingDB)
	if err != nil {
		return 0, err
	}
	defer func() { _ = store.Close() }()

	bus, err := pipeline.Connect(h.config.NATSURL)
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
