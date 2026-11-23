package sec

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestRequestGateSpacesRequests(t *testing.T) {
	gate := NewRequestGate()
	if err := gate.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}

	started := time.Now()

	if err := gate.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}

	if elapsed := time.Since(started); elapsed < 100*time.Millisecond {
		t.Fatalf("second request waited %s, want at least 100ms", elapsed)
	}
}

func TestRequestGateBackoffPostponesWaitingRequest(t *testing.T) {
	gate := NewRequestGate()
	if err := gate.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	result := make(chan error, 1)

	go func() { result <- gate.Wait(t.Context()) }()

	time.Sleep(10 * time.Millisecond)
	gate.Backoff(200 * time.Millisecond)

	if err := <-result; err != nil {
		t.Fatal(err)
	}

	if elapsed := time.Since(started); elapsed < 190*time.Millisecond {
		t.Fatalf("request resumed after %s, want at least 190ms", elapsed)
	}
}

func TestRequestGateWaitHonorsCancellation(t *testing.T) {
	gate := NewRequestGate()
	if err := gate.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := gate.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait error = %v, want context cancellation", err)
	}
}

func TestThrottleDelayUsesSECQuietPeriods(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		retryAfter time.Duration
		want       time.Duration
	}{
		{
			name: "forbidden", statusCode: http.StatusForbidden,
			want: 10 * time.Minute,
		},
		{
			name: "longer forbidden header", statusCode: http.StatusForbidden,
			retryAfter: 15 * time.Minute, want: 15 * time.Minute,
		},
		{
			name: "too many requests", statusCode: http.StatusTooManyRequests,
			want: time.Minute,
		},
		{
			name:       "too many requests header",
			statusCode: http.StatusTooManyRequests,
			retryAfter: 2 * time.Minute, want: 2 * time.Minute,
		},
		{name: "ordinary failure", statusCode: http.StatusServiceUnavailable},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := ThrottleDelay(test.statusCode,
				test.retryAfter); got != test.want {
				t.Fatalf("ThrottleDelay() = %s, want %s", got, test.want)
			}
		})
	}
}
