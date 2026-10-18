// Package eventbus provides the durable transport used between EDGAR roles.
package eventbus

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	DefaultURL        = nats.DefaultURL
	StreamName        = "EDGAR_EVENTS"
	StreamSubjects    = "edgar.filing.>"
	DiscoverySubject  = "edgar.filing.discovered"
	DownloadedSubject = "edgar.filing.downloaded"
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

// NATS owns one connection and the JetStream context derived from it.
type NATS struct {
	connection *nats.Conn
	jetstream  nats.JetStreamContext
}

// Connect opens a NATS connection and ensures the EDGAR event stream exists.
func Connect(url string) (*NATS, error) {
	if strings.TrimSpace(url) == "" {
		return nil, errors.New("NATS URL is required")
	}

	connection, err := nats.Connect(url,
		nats.Name("chopstick"),
		nats.Timeout(5*time.Second),
		nats.MaxReconnects(-1),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to NATS: %w", err)
	}

	jetstream, err := connection.JetStream()
	if err != nil {
		connection.Close()

		return nil, fmt.Errorf("open JetStream context: %w", err)
	}

	bus := &NATS{connection: connection, jetstream: jetstream}
	if err := bus.ensureStream(); err != nil {
		connection.Close()

		return nil, err
	}

	return bus, nil
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
	message := nats.NewMsg(subject)

	message.Data = payload
	if messageID != "" {
		message.Header.Set(nats.MsgIdHdr, messageID)
	}

	if _, err := n.jetstream.PublishMsg(message, nats.Context(ctx)); err != nil {
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
