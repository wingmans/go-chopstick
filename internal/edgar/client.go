package edgar

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"wingman.com/chopstick/internal/ctxlog"
	"wingman.com/chopstick/internal/outbound"
)

const maxHTTPErrorBody = 4096

type edgarClient struct {
	httpClient  *http.Client
	userAgent   string
	requestGate *RequestGate
}

func newEDGARClient(client *http.Client, userAgent string) edgarClient {
	return newEDGARClientWithGate(client, userAgent, nil)
}

func newEDGARClientWithGate(client *http.Client, userAgent string,
	requestGate *RequestGate,
) edgarClient {
	if client == nil {
		client = outbound.NewClient(30 * time.Second)
	}

	if requestGate == nil {
		requestGate = NewRequestGate()
	}

	return edgarClient{
		httpClient: client, userAgent: userAgent, requestGate: requestGate,
	}
}

func (c edgarClient) get(ctx context.Context, requestURL string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request for %s: %w", requestURL, err)
	}

	request.Header.Set("User-Agent", c.userAgent)

	if err := c.requestGate.Wait(ctx); err != nil {
		return nil, err
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", requestURL, err)
	}

	if response.StatusCode == http.StatusOK {
		return response, nil
	}

	upstreamError := NewHTTPError(response, requestURL)
	if delay := c.requestGate.Throttle(upstreamError.StatusCode,
		upstreamError.RetryAfter); delay > 0 {
		upstreamError.RetryAfter = delay
		ctxlog.FromContext(ctx).Warn("SEC request blocked; request gate paused",
			"status", upstreamError.StatusCode, "retry_in", delay,
			"url", requestURL)
	}

	if closeErr := response.Body.Close(); closeErr != nil {
		return nil, errors.Join(upstreamError,
			fmt.Errorf("close rejected response body: %w", closeErr))
	}

	return nil, upstreamError
}

// HTTPError describes a non-success response from the EDGAR service.
type HTTPError struct {
	URL        string
	StatusCode int
	Status     string
	RetryAfter time.Duration
	Message    string
}

// NewHTTPError consumes a bounded response-body prefix and describes a
// rejected SEC request. The caller remains responsible for closing the body.
func NewHTTPError(response *http.Response, requestURL string) *HTTPError {
	message := ""

	if response.Body != nil {
		body, err := io.ReadAll(io.LimitReader(response.Body, maxHTTPErrorBody))
		if err == nil {
			message = strings.Join(strings.Fields(
				strings.ToValidUTF8(string(body), "")), " ")
		}
	}

	return &HTTPError{
		URL: requestURL, StatusCode: response.StatusCode, Status: response.Status,
		RetryAfter: parseRetryAfter(response.Header.Get("Retry-After"), time.Now()),
		Message:    message,
	}
}

func (e *HTTPError) Error() string {
	message := fmt.Sprintf("EDGAR rejected %s with HTTP %s", e.URL, e.Status)
	if e.Message != "" {
		message += ": " + e.Message
	}

	return message
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}

	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds > 0 {
			return time.Duration(seconds) * time.Second
		}

		return 0
	}

	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}

	return when.Sub(now)
}
