// Package control defines the NATS control plane shared by chop and chopd.
package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	StatusSubject        = "chop.control.status"
	FailuresSubject      = "chop.control.failures.list"
	RetryFailuresSubject = "chop.control.failures.retry"
	requestTimeout       = 5 * time.Second
)

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

// Status is the live state reported by chopd.
type Status struct {
	StartedAt      time.Time        `json:"started_at"`
	HTTPAddress    string           `json:"http_address"`
	Pipeline       PipelineStats    `json:"pipeline"`
	StreamMessages uint64           `json:"stream_messages"`
	StreamBytes    uint64           `json:"stream_bytes"`
	Consumers      []ConsumerStatus `json:"consumers"`
}

// Failure is the operator-facing view of an unresolved pipeline failure.
type Failure struct {
	Stage           string    `json:"stage"`
	AccessionNumber string    `json:"accession_number"`
	Attempts        int       `json:"attempts"`
	LastFailedAt    time.Time `json:"last_failed_at"`
	LastError       string    `json:"last_error"`
}

// FailureRequest filters and limits failure results.
type FailureRequest struct {
	Stage string `json:"stage,omitempty"`
	Limit int    `json:"limit"`
}

// RetryRequest selects the failure stage to requeue.
type RetryRequest struct {
	Stage string `json:"stage"`
}

// RetryResult reports how many failures were requeued.
type RetryResult struct {
	Count int `json:"count"`
}

type response[T any] struct {
	Data  T      `json:"data"`
	Error string `json:"error,omitempty"`
}

// Handler implements live administrative operations inside chopd.
type Handler interface {
	Status(ctx context.Context) (Status, error)
	Failures(ctx context.Context, request FailureRequest) ([]Failure, error)
	RetryFailures(ctx context.Context, request RetryRequest) (RetryResult, error)
}

// Server serves control requests over a dedicated NATS connection.
type Server struct {
	connection *nats.Conn
	handler    Handler
}

// NewServer connects the chopd control plane to NATS.
func NewServer(url string, handler Handler) (*Server, error) {
	if strings.TrimSpace(url) == "" {
		return nil, errors.New("NATS URL is required")
	}

	connection, err := nats.Connect(url,
		nats.Name("chopd-control"),
		nats.Timeout(requestTimeout),
		nats.MaxReconnects(-1),
	)
	if err != nil {
		return nil, fmt.Errorf("connect control plane to NATS: %w", err)
	}

	return &Server{connection: connection, handler: handler}, nil
}

// Run subscribes to control subjects until the context is canceled.
func (s *Server) Run(ctx context.Context) error {
	subscriptions := make([]*nats.Subscription, 0, 3)

	subscribe := func(subject string, handle nats.MsgHandler) error {
		subscription, err := s.connection.QueueSubscribe(
			subject, "chopd-control", handle,
		)
		if err != nil {
			return fmt.Errorf("subscribe to %s: %w", subject, err)
		}

		subscriptions = append(subscriptions, subscription)

		return nil
	}

	if err := subscribe(StatusSubject, s.handleStatus); err != nil {
		return err
	}

	if err := subscribe(FailuresSubject, s.handleFailures); err != nil {
		return err
	}

	if err := subscribe(RetryFailuresSubject, s.handleRetry); err != nil {
		return err
	}

	if err := s.connection.Flush(); err != nil {
		return fmt.Errorf("activate control subscriptions: %w", err)
	}

	select {
	case <-ctx.Done():
	case <-s.connection.StatusChanged(nats.CLOSED):
		return errors.New("NATS control connection closed")
	}

	for _, subscription := range subscriptions {
		_ = subscription.Unsubscribe()
	}

	return nil
}

// Close closes the control connection immediately after Run has stopped.
func (s *Server) Close() {
	s.connection.Close()
}

func (s *Server) handleStatus(message *nats.Msg) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	value, err := s.handler.Status(ctx)
	s.respond(message, value, err)
}

func (s *Server) handleFailures(message *nats.Msg) {
	var request FailureRequest
	if err := json.Unmarshal(message.Data, &request); err != nil {
		s.respond(message, []Failure(nil), fmt.Errorf("decode request: %w", err))

		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	value, err := s.handler.Failures(ctx, request)
	s.respond(message, value, err)
}

func (s *Server) handleRetry(message *nats.Msg) {
	var request RetryRequest
	if err := json.Unmarshal(message.Data, &request); err != nil {
		s.respond(message, RetryResult{}, fmt.Errorf("decode request: %w", err))

		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	value, err := s.handler.RetryFailures(ctx, request)
	s.respond(message, value, err)
}

func (s *Server) respond(message *nats.Msg, value any, responseErr error) {
	payload := struct {
		Data  any    `json:"data"`
		Error string `json:"error,omitempty"`
	}{Data: value}
	if responseErr != nil {
		payload.Error = responseErr.Error()
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return
	}

	_ = message.Respond(encoded)
}

// Client sends operator requests to a running chopd instance.
type Client struct {
	connection *nats.Conn
}

// NewClient connects an operator client to NATS.
func NewClient(url string) (*Client, error) {
	connection, err := nats.Connect(url,
		nats.Name("chop-control-cli"),
		nats.Timeout(requestTimeout),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to NATS control plane: %w", err)
	}

	return &Client{connection: connection}, nil
}

// Close closes the client connection.
func (c *Client) Close() {
	c.connection.Close()
}

// Status requests live application status.
func (c *Client) Status(ctx context.Context) (Status, error) {
	return request[Status](ctx, c.connection, StatusSubject, struct{}{})
}

// Failures requests unresolved pipeline failures.
func (c *Client) Failures(ctx context.Context,
	value FailureRequest,
) ([]Failure, error) {
	return request[[]Failure](ctx, c.connection, FailuresSubject, value)
}

// RetryFailures asks chopd to requeue unresolved failures.
func (c *Client) RetryFailures(ctx context.Context,
	value RetryRequest,
) (RetryResult, error) {
	return request[RetryResult](ctx, c.connection, RetryFailuresSubject, value)
}

func request[T any](ctx context.Context, connection *nats.Conn,
	subject string, value any,
) (T, error) {
	var zero T

	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, requestTimeout)
		defer cancel()
	}

	payload, err := json.Marshal(value)
	if err != nil {
		return zero, fmt.Errorf("encode control request: %w", err)
	}

	message, err := connection.RequestWithContext(ctx, subject, payload)
	if errors.Is(err, nats.ErrNoResponders) {
		return zero, errors.New("chopd is not running or not responding")
	}

	if err != nil {
		return zero, fmt.Errorf("request %s: %w", subject, err)
	}

	var result response[T]
	if err := json.Unmarshal(message.Data, &result); err != nil {
		return zero, fmt.Errorf("decode control response: %w", err)
	}

	if result.Error != "" {
		return zero, errors.New(result.Error)
	}

	return result.Data, nil
}
