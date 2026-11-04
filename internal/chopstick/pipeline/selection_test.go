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
	}{
		{name: "selected", event: Event{
			CIK: "320193", FormType: "8-K",
		}, selected: true},
		{name: "outside set", event: Event{
			CIK: "1", FormType: "10-K",
		}, selected: false},
		{name: "unsupported", event: Event{
			CIK: "320193", FormType: "S-1",
		}, selected: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			selected, reason := selectionDecision(test.event, "us-gaap",
				ciks, forms)
			if selected != test.selected || reason == "" {
				t.Fatalf("selected=%t reason=%q", selected, reason)
			}
		})
	}
}
