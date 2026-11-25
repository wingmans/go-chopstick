package pipeline

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/nats-io/nats.go"
)

// ErrIncompatibleResources indicates that existing JetStream resources have
// settings that would prevent Chopstick from delivering or receiving events.
var ErrIncompatibleResources = errors.New("incompatible JetStream configuration")

// ErrNewsReplayRequired indicates that an existing consumer has a cursor
// that cannot be used with a replacement news database.
var ErrNewsReplayRequired = errors.New("existing news consumer needs replay for the replacement database")

func consumerConfigs() []nats.ConsumerConfig {
	configs := []nats.ConsumerConfig{
		{Durable: SelectionDurable, FilterSubject: DiscoverySubject + ".>"},
		{Durable: SubmissionDurable, FilterSubject: SelectedSubject + ".>"},
		{Durable: ExtractionDurable, FilterSubject: DownloadedSubject + ".>"},
		{Durable: OwnershipDurable, FilterSubject: DiscoverySubject + ".>"},
		{Durable: NewsDurable, FilterSubject: SelectionSubject + ".>"},
	}

	for i := range configs {
		configs[i].AckPolicy = nats.AckExplicitPolicy

		configs[i].DeliverPolicy = nats.DeliverAllPolicy
		if configs[i].Durable != NewsDurable {
			configs[i].AckWait = 30 * time.Minute
		}
	}

	return configs
}

// Provision creates the EDGAR stream and any missing durable consumers.
// Replay news when provisioning a replacement for a missing projection.
func Provision(ctx context.Context, url string, missingNews, allowNewsReplay bool) error {
	bus, err := Inspect(ctx, url)
	if err != nil {
		return err
	}
	defer bus.CloseImmediately()

	if err := bus.ensureStream(ctx); err != nil {
		return err
	}

	if err := bus.checkStream(ctx); err != nil {
		return err
	}

	for _, cfg := range consumerConfigs() {
		info, err := bus.jetstream.ConsumerInfo(StreamName, cfg.Durable, nats.Context(ctx))
		if err == nil && cfg.Durable == NewsDurable && missingNews && info.Delivered.Stream > 0 {
			if !allowNewsReplay {
				return ErrNewsReplayRequired
			}

			if err := bus.jetstream.DeleteConsumer(StreamName, NewsDurable, nats.Context(ctx)); err != nil {
				return fmt.Errorf("reset news consumer for replay: %w", err)
			}

			err = nats.ErrConsumerNotFound
		}

		if errors.Is(err, nats.ErrConsumerNotFound) {
			info, err = bus.jetstream.AddConsumer(StreamName, &cfg, nats.Context(ctx))
		}

		if err := checkConsumer(info, cfg, err); err != nil {
			return err
		}
	}

	return nil
}

// CheckResources verifies the stream and durable consumers used by chopd.
func (n *NATS) CheckResources(ctx context.Context) error {
	issues := n.ResourceIssues(ctx)
	failures := make([]error, len(issues))

	for index, issue := range issues {
		failures[index] = issue.Err
	}

	return errors.Join(failures...)
}

// ResourceIssue identifies a missing or incompatible JetStream resource.
type ResourceIssue struct {
	Name string
	Err  error
}

// ResourceIssues inspects the configured stream and consumers without making
// any changes.
func (n *NATS) ResourceIssues(ctx context.Context) []ResourceIssue {
	if err := n.checkStream(ctx); err != nil {
		return []ResourceIssue{{Name: StreamName, Err: err}}
	}

	var issues []ResourceIssue

	for _, cfg := range consumerConfigs() {
		info, err := n.jetstream.ConsumerInfo(StreamName, cfg.Durable, nats.Context(ctx))
		if err := checkConsumer(info, cfg, err); err != nil {
			issues = append(issues, ResourceIssue{Name: cfg.Durable, Err: err})
		}
	}

	return issues
}

func (n *NATS) checkStream(ctx context.Context) error {
	info, err := n.jetstream.StreamInfo(StreamName, nats.Context(ctx))
	if err != nil {
		return fmt.Errorf("stream %s: %w", StreamName, err)
	}

	if !slices.Contains(info.Config.Subjects, StreamSubjects) {
		return fmt.Errorf("%w: stream %s captures %v, expected %s",
			ErrIncompatibleResources, StreamName, info.Config.Subjects, StreamSubjects)
	}

	return nil
}

func checkConsumer(info *nats.ConsumerInfo, want nats.ConsumerConfig, err error) error {
	if err != nil {
		return fmt.Errorf("consumer %s: %w", want.Durable, err)
	}

	cfg := info.Config
	if cfg.FilterSubject != want.FilterSubject || cfg.DeliverSubject != "" ||
		cfg.AckPolicy != want.AckPolicy || cfg.DeliverPolicy != want.DeliverPolicy ||
		(want.AckWait > 0 && cfg.AckWait != want.AckWait) {
		return fmt.Errorf("%w: consumer %s delivery settings (filter %q, expected %q)",
			ErrIncompatibleResources, want.Durable, cfg.FilterSubject, want.FilterSubject)
	}

	return nil
}
