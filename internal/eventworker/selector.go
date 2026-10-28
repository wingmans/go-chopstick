package eventworker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/nats-io/nats.go"

	"wingman.com/chopstick/internal/constituents"
	"wingman.com/chopstick/internal/edgar"
	"wingman.com/chopstick/internal/eventbus"
	"wingman.com/chopstick/internal/pollingworker"
)

const SelectionDurable = "edgar-selection-v1"

// SelectionConfig defines the intentionally small automatic processing scope.
type SelectionConfig struct {
	SetName   string
	CIKs      []string
	FormTypes []string
	Drain     bool
}

// Select observes every discovery and publishes one durable decision. Selected
// decisions are the only input consumed by the download worker.
func Select(ctx context.Context, bus *eventbus.NATS, cfg SelectionConfig,
	logger *slog.Logger,
) error {
	subscription, err := pullSubscription(bus, eventbus.DiscoverySubject+".>",
		SelectionDurable)
	if err != nil {
		return err
	}

	ciks := make(map[string]struct{}, len(cfg.CIKs))
	for _, cik := range cfg.CIKs {
		normalized, normalizeErr := constituents.NormalizeCIK(cik)
		if normalizeErr != nil {
			return normalizeErr
		}

		ciks[normalized] = struct{}{}
	}

	forms := make(map[string]struct{}, len(cfg.FormTypes))
	for _, form := range cfg.FormTypes {
		forms[strings.ToUpper(strings.TrimSpace(form))] = struct{}{}
	}

	return consume(ctx, subscription, logger, "selection", cfg.Drain, 1, nil,
		func(message *nats.Msg, event pollingworker.FilingEvent) error {
			selected, reason := selectionDecision(event, cfg.SetName, ciks,
				forms)
			event.Selected = &selected
			event.SelectionReason = reason
			event.SelectionMode = "automatic"
			event.SelectionSet = cfg.SetName

			payload, marshalErr := json.Marshal(event)
			if marshalErr != nil {
				return marshalErr
			}

			base := eventbus.SkippedSubject
			if selected {
				base = eventbus.SelectedSubject
			}

			subject := eventbus.SubjectFor(base, event.FormType)

			messageID := fmt.Sprintf("filing.selection:%s:%t",
				event.AccessionNumber, selected)
			if publishErr := bus.Publish(ctx, subject, messageID,
				payload); publishErr != nil {
				return publishErr
			}

			logger.Debug("filing selection decided",
				"accession", event.AccessionNumber, "selected", selected,
				"reason", reason)

			return message.Ack()
		})
}

func selectionDecision(event pollingworker.FilingEvent, setName string,
	ciks, forms map[string]struct{},
) (bool, string) {
	form := strings.ToUpper(strings.TrimSpace(event.FormType))
	if !edgar.SupportsFormType(form) {
		return false, "form is not supported by the parser"
	}

	if _, enabled := forms[form]; !enabled {
		return false, "form is not enabled by EDGAR_FORM_TYPES"
	}

	if strings.TrimSpace(setName) == "" {
		return false, "EDGAR_SET is not configured"
	}

	cik, err := constituents.NormalizeCIK(event.CIK)
	if err != nil {
		return false, "filing has an invalid CIK"
	}

	if _, included := ciks[cik]; !included {
		return false, "company is outside EDGAR_SET=" + setName
	}

	return true, "supported form and member of EDGAR_SET=" + setName
}
