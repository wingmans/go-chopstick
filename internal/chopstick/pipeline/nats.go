// Package pipeline owns durable event delivery and the serial application
// steps driven by those events.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"wingman.com/chopstick/internal/ctxlog"
)

const (
	DefaultURL        = nats.DefaultURL
	StreamName        = "EDGAR_EVENTS"
	StreamSubjects    = "sec.filing.>"
	DiscoverySubject  = "sec.filing.discovered"
	SelectionSubject  = "sec.filing.selection"
	SelectedSubject   = "sec.filing.selection.selected"
	SkippedSubject    = "sec.filing.selection.skipped"
	DownloadedSubject = "sec.filing.downloaded"
	publishAckTimeout = 10 * time.Second
)

// SubjectFor adds a routing-safe form type to a base filing subject.
func SubjectFor(base, formType string) string {
	formType = strings.NewReplacer(
		".", "_", "/", "_", " ", "_", "*", "_", ">", "_",
	).Replace(strings.TrimSpace(formType))
	if formType == "" {
		formType = "unknown"
	}

	return base + "." + formType
}

// Publisher stores an event durably and waits for the server acknowledgement.
type Publisher interface {
	Publish(ctx context.Context, subject, messageID string, payload []byte) error
}

// PublishAckTimeoutError reports that JetStream did not acknowledge a publish
// before its bounded wait expired.
type PublishAckTimeoutError struct {
	Subject string
	Timeout time.Duration
}

func (e *PublishAckTimeoutError) Error() string {
	return fmt.Sprintf("publish %s: acknowledgement timed out after %s: %s",
		e.Subject, e.Timeout, context.DeadlineExceeded)
}

func (e *PublishAckTimeoutError) Unwrap() error {
	return context.DeadlineExceeded
}

// NATS owns one connection and the JetStream context derived from it.
type NATS struct {
	connection *nats.Conn
	jetstream  nats.JetStreamContext
}

// Connect opens a NATS connection and ensures the EDGAR event stream exists.
func Connect(ctx context.Context, url string) (*NATS, error) {
	bus, err := Inspect(ctx, url)
	if err != nil {
		return nil, err
	}

	if err := bus.ensureStream(); err != nil {
		_ = bus.Close()

		return nil, err
	}

	return bus, nil
}

// Inspect opens a NATS connection without creating or changing JetStream
// resources. Operational commands use it for read-only checks.
func Inspect(ctx context.Context, url string) (*NATS, error) {
	if strings.TrimSpace(url) == "" {
		return nil, errors.New("NATS URL is required")
	}

	logger := ctxlog.FromContext(ctx)

	connection, err := nats.Connect(url,
		nats.Name("chopstick"),
		nats.Timeout(5*time.Second),
		nats.MaxReconnects(-1),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				logger.Warn("NATS disconnected", "error", err)
			}
		}),
		nats.ReconnectHandler(func(connection *nats.Conn) {
			logger.Info("NATS reconnected", "url", connection.ConnectedUrl())
		}),
		nats.ClosedHandler(func(connection *nats.Conn) {
			if err := connection.LastError(); err != nil && ctx.Err() == nil {
				logger.Warn("NATS connection closed", "error", err)
			}
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to NATS: %w", err)
	}

	jetstream, err := connection.JetStream()
	if err != nil {
		connection.Close()

		return nil, fmt.Errorf("open JetStream context: %w", err)
	}

	return &NATS{connection: connection, jetstream: jetstream}, nil
}

// ResetStream removes all retained EDGAR events and durable consumers. The
// next Connect call recreates the stream.
func ResetStream(ctx context.Context, url string) error {
	bus, err := Inspect(ctx, url)
	if err != nil {
		return err
	}
	defer func() { _ = bus.Close() }()

	if err := bus.jetstream.DeleteStream(StreamName); err != nil &&
		!errors.Is(err, nats.ErrStreamNotFound) {
		return fmt.Errorf("delete EDGAR event stream: %w", err)
	}

	return nil
}

//nolint:funcorder // Kept beside Connect, which invokes it during construction.
func (n *NATS) ensureStream() error {
	if _, err := n.jetstream.StreamInfo(StreamName); err == nil {
		return nil
	} else if !errors.Is(err, nats.ErrStreamNotFound) {
		return fmt.Errorf("inspect JetStream stream: %w", err)
	}

	_, err := n.jetstream.AddStream(&nats.StreamConfig{
		Name:       StreamName,
		Subjects:   []string{StreamSubjects},
		Retention:  nats.LimitsPolicy,
		Storage:    nats.FileStorage,
		Discard:    nats.DiscardOld,
		Duplicates: 24 * time.Hour,
	})
	if err != nil {
		return fmt.Errorf("create JetStream stream: %w", err)
	}

	return nil
}

// Publish stores a message and returns only after JetStream acknowledges it.
func (n *NATS) Publish(ctx context.Context, subject, messageID string,
	payload []byte,
) error {
	publishCtx, cancel := context.WithTimeout(ctx, publishAckTimeout)
	defer cancel()

	message := nats.NewMsg(subject)

	message.Data = payload
	if messageID != "" {
		message.Header.Set(nats.MsgIdHdr, messageID)
	}

	if _, err := n.jetstream.PublishMsg(message,
		nats.Context(publishCtx)); err != nil {
		if errors.Is(publishCtx.Err(), context.DeadlineExceeded) {
			return &PublishAckTimeoutError{
				Subject: subject,
				Timeout: publishAckTimeout,
			}
		}

		return fmt.Errorf("publish %s: %w", subject, err)
	}

	return nil
}

// JetStream exposes the context to durable consumers.
//
//nolint:ireturn // The upstream NATS API intentionally exposes this interface.
func (n *NATS) JetStream() nats.JetStreamContext {
	return n.jetstream
}

// Close drains the connection so buffered protocol messages are flushed.
func (n *NATS) Close() error {
	if err := n.connection.Drain(); err != nil {
		n.connection.Close()

		return err
	}

	return nil
}

// CloseImmediately closes the connection without waiting for buffered work.
// It is reserved for shutdown recovery after cancellation was not sufficient.
func (n *NATS) CloseImmediately() {
	n.connection.Close()
}
