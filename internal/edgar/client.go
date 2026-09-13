package edgar

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

type edgarClient struct {
	httpClient *http.Client
	userAgent  string
}

func newEDGARClient(client *http.Client, userAgent string) edgarClient {
	if client == nil {
		client = http.DefaultClient
	}

	return edgarClient{httpClient: client, userAgent: userAgent}
}

func (c edgarClient) get(ctx context.Context, requestURL string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request for %s: %w", requestURL, err)
	}

	request.Header.Set("User-Agent", c.userAgent)

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", requestURL, err)
	}

	if response.StatusCode == http.StatusOK {
		return response, nil
	}

	upstreamError := &HTTPError{
		URL:        requestURL,
		StatusCode: response.StatusCode,
		Status:     response.Status,
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
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("EDGAR rejected %s with HTTP %s", e.URL, e.Status)
}
