package pipeline

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"testing"
	"time"

	"wingman.com/chopstick/internal/edgar"
)

func TestConsumerReportUsesIntervalActivity(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, time.November, 2, 12, 0, 0, 0, time.UTC)
	stats := consumerStats{lastReport: started}

	report := stats.nextReport(consumerSnapshot{pending: 10}, started)
	if report.level != slog.LevelInfo || report.message != "consumer progress" {
		t.Fatalf("initial report = %+v", report)
	}

	report = stats.nextReport(consumerSnapshot{
		scanned: 4, completed: 4, pending: 6,
	}, started.Add(5*time.Second))
	if report.level != slog.LevelInfo || report.message != "consumer progress" ||
		math.Abs(report.rate-0.8) > 0.0001 {
		t.Fatalf("activity report = %+v", report)
	}

	report = stats.nextReport(consumerSnapshot{
		scanned: 4, completed: 4, pending: 6,
	}, started.Add(10*time.Second))
	if report.level != slog.LevelDebug ||
		report.message != "consumer heartbeat" || report.rate != 0 {
		t.Fatalf("idle report = %+v", report)
	}

	report = stats.nextReport(consumerSnapshot{
		scanned: 10, completed: 10,
	}, started.Add(15*time.Second))
	if report.level != slog.LevelInfo ||
		report.message != "consumer backlog drained" ||
		math.Abs(report.rate-1.2) > 0.0001 {
		t.Fatalf("drained report = %+v", report)
	}
}

func TestWorkRetryDelayBacksOffThrottledRequests(t *testing.T) {
	t.Parallel()

	throttled := fmt.Errorf("download: %w", &edgar.HTTPError{
		StatusCode: http.StatusTooManyRequests,
	})
	if got := workRetryDelay(throttled, 3); got != 4*time.Minute {
		t.Fatalf("retry delay = %s, want 4m", got)
	}

	withHeader := &edgar.HTTPError{
		StatusCode: http.StatusTooManyRequests,
		RetryAfter: 10 * time.Minute,
	}
	if got := workRetryDelay(withHeader, 2); got != 10*time.Minute {
		t.Fatalf("retry delay = %s, want 10m", got)
	}

	forbidden := &edgar.HTTPError{StatusCode: http.StatusForbidden}
	if got := workRetryDelay(forbidden, 1); got != 10*time.Minute {
		t.Fatalf("forbidden retry delay = %s, want 10m", got)
	}

	if got := workRetryDelay(errors.New("temporary"), 4); got != time.Minute {
		t.Fatalf("ordinary retry delay = %s, want 1m", got)
	}
}

func TestIdleConsumerStartsAtDebug(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, time.November, 2, 12, 0, 0, 0, time.UTC)
	stats := consumerStats{lastReport: started}

	report := stats.nextReport(consumerSnapshot{}, started)
	if report.level != slog.LevelDebug ||
		report.message != "consumer heartbeat" || report.rate != 0 {
		t.Fatalf("initial idle report = %+v", report)
	}
}
