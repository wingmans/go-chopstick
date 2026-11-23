package market

import (
	"testing"
	"time"
)

func mustDate(t *testing.T, value string) time.Time {
	t.Helper()

	date, err := time.Parse(dateLayout, value)
	if err != nil {
		t.Fatalf("parse date: %v", err)
	}

	return date
}
