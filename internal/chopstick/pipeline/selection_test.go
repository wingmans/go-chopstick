package pipeline

import (
	"testing"
)

func TestSelectionDecision(t *testing.T) {
	t.Parallel()

	ciks := map[string]struct{}{"0000320193": {}}
	forms := map[string]struct{}{"10-K": {}, "10-Q": {}, "8-K": {}}

	tests := []struct {
		name     string
		event    Event
		selected bool
		reason   string
	}{
		{name: "selected", event: Event{
			CIK: "320193", FormType: "8-K",
		}, selected: true,
			reason: "supported form and member of EDGAR_SET=us-gaap"},
		{name: "outside set", event: Event{
			CIK: "1", FormType: "10-K",
		}, selected: false,
			reason: "company is outside EDGAR_SET=us-gaap"},
		{name: "unsupported", event: Event{
			CIK: "320193", FormType: "S-1",
		}, selected: false,
			reason: "form type \"S-1\" is not supported by the financial filing parser"},
		{name: "not enabled", event: Event{
			CIK: "320193", FormType: "10-Q",
		}, selected: false,
			reason: "form type \"10-Q\" is not enabled by EDGAR_FORM_TYPES"},
	}

	delete(forms, "10-Q")

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			selected, reason := selectionDecision(test.event, "us-gaap",
				ciks, forms)
			if selected != test.selected || reason != test.reason {
				t.Fatalf("selected=%t reason=%q, want selected=%t reason=%q",
					selected, reason, test.selected, test.reason)
			}
		})
	}
}
