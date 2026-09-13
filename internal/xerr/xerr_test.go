package xerr

import (
	"errors"
	"testing"
)

func TestWithDetailsDoesNotMutateOriginal(t *testing.T) {
	cause := errors.New("root cause")
	original := Wrap(Internal, "TEST_FAILED", "test failed", cause)
	withDetails := original.WithDetails(map[string]any{"attempt": 1})

	if original.Details != nil {
		t.Fatalf("original error was mutated: %#v", original.Details)
	}

	if withDetails.Details["attempt"] != 1 {
		t.Fatalf("details were not attached: %#v", withDetails.Details)
	}

	if !errors.Is(withDetails, cause) {
		t.Fatal("wrapped cause was not preserved")
	}
}

func TestIsCode(t *testing.T) {
	err := Wrap(Unavailable, "UPSTREAM_FAILED", "upstream failed", errors.New("network"))

	if !IsCode(err, Unavailable) {
		t.Fatal("expected unavailable code")
	}

	if IsCode(err, Internal) {
		t.Fatal("unexpected internal code")
	}
}
