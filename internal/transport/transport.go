// Package transport provides the local command connection between chop and
// chopd.
package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

const (
	// DefaultAddress is the local chopd command address.
	DefaultAddress = "127.0.0.1:8084"
	requestTimeout = 5 * time.Second

	CommandStatus        = "status"
	CommandListFailures  = "failures.list"
	CommandRetryFailures = "failures.retry"
	CommandImportIndex   = "index.import"
	CommandIndexStatus   = "index.status"
)

// Request is one command sent from chop to chopd.
type Request struct {
	Command     string              `json:"command"`
	Stage       string              `json:"stage,omitempty"`
	Limit       int                 `json:"limit,omitempty"`
	IndexImport *IndexImportRequest `json:"index_import,omitempty"`
}

// Response is one command result returned by chopd.
type Response struct {
	Status      *Status              `json:"status,omitempty"`
	IndexImport *IndexImportResponse `json:"index_import,omitempty"`
	IndexStatus *IndexStatusResponse `json:"index_status,omitempty"`
	Failures    []Failure            `json:"failures,omitempty"`
	Retried     int                  `json:"retried,omitempty"`
	Error       string               `json:"error,omitempty"`
}

// IndexImportRequest selects local quarterly index files for ingestion.
type IndexImportRequest struct {
	File     string `json:"file,omitempty"`
	Latest   bool   `json:"latest,omitempty"`
	Year     int    `json:"year,omitempty"`
	FromYear int    `json:"from_year,omitempty"`
	ToYear   int    `json:"to_year,omitempty"`
	DryRun   bool   `json:"dry_run,omitempty"`
}

// IndexImportResponse acknowledges a daemon-owned import.
type IndexImportResponse struct {
	Accepted bool `json:"accepted"`
}

// IndexStatusResponse describes local index inventory and import coverage.
type IndexStatusResponse struct {
	Directory        string              `json:"directory"`
	MasterPath       string              `json:"master_path"`
	MasterState      string              `json:"master_state"`
	AvailablePeriods []string            `json:"available_periods"`
	MissingPeriods   []string            `json:"missing_periods"`
	ImportedPeriods  []string            `json:"imported_periods"`
	LastImport       *IndexImportSummary `json:"last_import,omitempty"`
	ActiveImport     *IndexImportStatus  `json:"active_import,omitempty"`
}

// IndexImportSummary describes the last selection committed to the work store.
type IndexImportSummary struct {
	CommittedAt time.Time `json:"committed_at"`
	Files       []string  `json:"files"`
	Periods     []string  `json:"periods"`
	Examined    int       `json:"examined"`
	Queued      int       `json:"queued"`
	Duplicates  int       `json:"duplicates"`
}

// PipelineStats contains durable ingestion and failure counts.
type PipelineStats struct {
	Accessions      int64 `json:"accessions"`
	OutboxPending   int64 `json:"outbox_pending"`
	OutboxPublished int64 `json:"outbox_published"`
	Failures        int64 `json:"failures"`
}

// ConsumerStatus describes one durable JetStream consumer.
type ConsumerStatus struct {
	Name        string `json:"name"`
	Pending     uint64 `json:"pending"`
	InFlight    int    `json:"in_flight"`
	Redelivered int    `json:"redelivered"`
}

// IndexImportStatus is the currently running index import, when present.
type IndexImportStatus struct {
	State      string    `json:"state"`
	Phase      string    `json:"phase"`
	StartedAt  time.Time `json:"started_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	Files      int       `json:"files"`
	Examined   int       `json:"examined"`
	Queued     int       `json:"queued"`
	Duplicates int       `json:"duplicates"`
}

// Status is the live state reported by chopd.
type Status struct {
	StartedAt      time.Time          `json:"started_at"`
	HTTPAddress    string             `json:"http_address"`
	Pipeline       PipelineStats      `json:"pipeline"`
	StreamMessages uint64             `json:"stream_messages"`
	StreamBytes    uint64             `json:"stream_bytes"`
	Consumers      []ConsumerStatus   `json:"consumers"`
	IndexImport    *IndexImportStatus `json:"index_import,omitempty"`
}

// Failure is the operator-facing view of an unresolved pipeline failure.
type Failure struct {
	Stage           string    `json:"stage"`
	AccessionNumber string    `json:"accession_number"`
	Attempts        int       `json:"attempts"`
	LastFailedAt    time.Time `json:"last_failed_at"`
	LastError       string    `json:"last_error"`
}

// Server listens for local command requests.
type Server struct {
	address  string
	listener net.Listener
}

// NewServer initializes the transport layer without over-abstracting.
func NewServer(address string) *Server {
	return &Server{address: address}
}

// Start begins listening for incoming connections directly.
func (s *Server) Start(
	ctx context.Context,
	handler func([]byte) []byte,
) error {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", s.address)
	if err != nil {
		return fmt.Errorf("failed to bind transport: %w", err)
	}

	s.listener = listener

	go func() {
		<-ctx.Done()

		_ = listener.Close()
	}()

	for {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			continue
		}

		go handleConnection(connection, handler)
	}
}

func handleConnection(connection net.Conn, handler func([]byte) []byte) {
	defer func() { _ = connection.Close() }()

	request, err := io.ReadAll(io.LimitReader(connection, 64*1024))
	if err != nil {
		return
	}

	_, _ = connection.Write(handler(request))
}

// Send sends one command to chopd and waits for its response.
func Send(
	ctx context.Context,
	address string,
	request Request,
) (Response, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, requestTimeout)
		defer cancel()
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return Response{}, fmt.Errorf("encode command: %w", err)
	}

	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return Response{}, fmt.Errorf("connect to chopd: %w", err)
	}
	defer func() { _ = connection.Close() }()

	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			return Response{}, fmt.Errorf("set command deadline: %w", err)
		}
	}

	if _, err := connection.Write(payload); err != nil {
		return Response{}, fmt.Errorf("send command: %w", err)
	}

	tcpConnection, ok := connection.(*net.TCPConn)
	if ok {
		if err := tcpConnection.CloseWrite(); err != nil {
			return Response{}, fmt.Errorf("finish command request: %w", err)
		}
	}

	encoded, err := io.ReadAll(connection)
	if err != nil {
		return Response{}, fmt.Errorf("read command response: %w", err)
	}

	var response Response
	if err := json.Unmarshal(encoded, &response); err != nil {
		return Response{}, fmt.Errorf("decode command response: %w", err)
	}

	if response.Error != "" {
		return Response{}, errors.New(response.Error)
	}

	return response, nil
}
