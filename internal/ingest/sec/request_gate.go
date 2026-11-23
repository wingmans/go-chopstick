package sec

import (
	"context"
	"net/http"
	"sync"
	"time"
)

const (
	defaultThrottleBackoff = time.Minute
	forbiddenBackoff       = 10 * time.Minute
)

// RequestGate coordinates the aggregate SEC request rate across concurrent
// application workers.
type RequestGate struct {
	mu      sync.Mutex
	next    time.Time
	changed chan struct{}
}

// NewRequestGate creates one process-local SEC request gate.
func NewRequestGate() *RequestGate {
	return &RequestGate{changed: make(chan struct{})}
}

// Wait reserves the next process-wide SEC request slot.
func (g *RequestGate) Wait(ctx context.Context) error {
	if g == nil {
		return nil
	}

	for {
		g.mu.Lock()

		wait := time.Until(g.next)
		if wait <= 0 {
			g.next = time.Now().Add(requestBudget)
			g.mu.Unlock()

			return nil
		}

		if g.changed == nil {
			g.changed = make(chan struct{})
		}

		changed := g.changed
		g.mu.Unlock()

		timer := time.NewTimer(wait)

		select {
		case <-ctx.Done():
			_ = timer.Stop()

			return ctx.Err()
		case <-timer.C:
		case <-changed:
			_ = timer.Stop()
		}
	}
}

// Backoff pauses new reservations after an SEC throttle response.
func (g *RequestGate) Backoff(delay time.Duration) {
	if g == nil {
		return
	}

	if delay <= 0 {
		delay = defaultThrottleBackoff
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	next := time.Now().Add(delay)
	if next.After(g.next) {
		g.next = next
		if g.changed != nil {
			close(g.changed)
		}

		g.changed = make(chan struct{})
	}
}

// ThrottleDelay returns the quiet period required for an SEC throttle
// response. SEC.gov commonly uses 403 for an IP-level fair-access block.
func ThrottleDelay(statusCode int, retryAfter time.Duration) time.Duration {
	switch statusCode {
	case http.StatusForbidden:
		return max(retryAfter, forbiddenBackoff)
	case http.StatusTooManyRequests:
		return max(retryAfter, defaultThrottleBackoff)
	default:
		return 0
	}
}

// Throttle pauses the gate when statusCode represents an SEC throttle.
func (g *RequestGate) Throttle(statusCode int,
	retryAfter time.Duration,
) time.Duration {
	delay := ThrottleDelay(statusCode, retryAfter)
	if delay > 0 {
		g.Backoff(delay)
	}

	return delay
}
