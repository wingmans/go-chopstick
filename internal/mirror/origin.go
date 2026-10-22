package mirror

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"wingman.com/fetch-ecb/internal/edgar"
)

// Origin retrieves authoritative SEC submissions on confirmed mirror misses.
type Origin interface {
	Fetch(ctx context.Context, ref SubmissionRef) (io.ReadCloser, error)
}

// SECOrigin is one globally paced SEC archive client.
type SECOrigin struct {
	client    *http.Client
	baseURL   string
	userAgent string
	mu        sync.Mutex
	last      time.Time
}

// NewSECOrigin creates a paced SEC origin.
func NewSECOrigin(client *http.Client, baseURL, userAgent string) (*SECOrigin,
	error,
) {
	if strings.TrimSpace(userAgent) == "" {
		return nil, errors.New("SEC user agent is required")
	}

	if client == nil {
		client = http.DefaultClient
	}

	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = edgar.DefaultBaseURL
	}

	return &SECOrigin{
		client: client, baseURL: baseURL, userAgent: userAgent,
	}, nil
}

// Fetch retrieves one submission while respecting the shared request pace.
func (o *SECOrigin) Fetch(ctx context.Context, ref SubmissionRef) (
	io.ReadCloser, error,
) {
	if err := validateRef(ref); err != nil {
		return nil, err
	}

	o.mu.Lock()

	wait := 100*time.Millisecond - time.Since(o.last)
	if wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			o.mu.Unlock()

			return nil, ctx.Err()
		case <-timer.C:
		}
	}

	o.last = time.Now()
	o.mu.Unlock()

	requestURL := o.baseURL + "/" +
		strings.TrimLeft(strings.TrimSpace(ref.FilingPath), "/")

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL,
		nil)
	if err != nil {
		return nil, fmt.Errorf("create SEC mirror request: %w", err)
	}

	request.Header.Set("User-Agent", o.userAgent)

	response, err := o.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request SEC mirror object: %w", err)
	}

	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()

		return nil, &edgar.HTTPError{
			URL: requestURL, StatusCode: response.StatusCode,
			Status: response.Status,
		}
	}

	return response.Body, nil
}
