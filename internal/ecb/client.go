package ecb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"wingman.com/fetch-ecb/internal/xerr"
)

const defaultBaseURL = "https://data-api.ecb.europa.eu"

const defaultSeriesPattern = "D.%s.%s.SP00.A"

type upstreamError struct {
	Status  string
	Message string
}

func (e *upstreamError) Error() string {
	return fmt.Sprintf("unexpected HTTP status %s: %s", e.Status, e.Message)
}

func buildRequestURL(cfg Config) string {
	query := url.Values{}
	if cfg.StartPeriod != "" {
		query.Set("startPeriod", cfg.StartPeriod)
	}

	if cfg.EndPeriod != "" {
		query.Set("endPeriod", cfg.EndPeriod)
	}

	requestURL := fmt.Sprintf(
		"%s/service/data/%s/%s",
		defaultBaseURL,
		url.PathEscape(Dataset),
		url.PathEscape(fmt.Sprintf(defaultSeriesPattern, cfg.Currency, cfg.CurrencyDenom)),
	)

	if encoded := query.Encode(); encoded != "" {
		requestURL += "?" + encoded
	}

	return requestURL
}

func fetchGenericData(ctx context.Context, client *http.Client, requestURL string) (*genericData, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("request creation failed: %w", err)
	}

	req.Header.Set("Accept", "application/xml")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

		message := strings.TrimSpace(string(body))
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}

		upstreamErr := &upstreamError{Status: resp.Status, Message: message}
		if closeErr := resp.Body.Close(); closeErr != nil {
			return nil, errors.Join(upstreamErr, fmt.Errorf("response body close failed: %w", closeErr))
		}

		return nil, upstreamErr
	}

	parsed, parseErr := parseGenericData(resp.Body)

	if closeErr := resp.Body.Close(); closeErr != nil {
		closeErr = fmt.Errorf("response body close failed: %w", closeErr)
		if parseErr != nil {
			return nil, errors.Join(parseErr, closeErr)
		}

		return nil, closeErr
	}

	return parsed, parseErr
}

// FetchAndWrite fetches ECB data and writes it in the configured format.
func FetchAndWrite(ctx context.Context, client *http.Client, cfg Config, stdout io.Writer) error {
	parsed, err := fetchGenericData(ctx, client, buildRequestURL(cfg))
	if err != nil {
		return classifyFetchError(ctx, cfg, err)
	}

	if err := parsed.writeOutput(stdout, Dataset, cfg.Output); err != nil {
		return xerr.Wrap(xerr.Internal, "OUTPUT_FAILED", "failed to write output", err)
	}

	return nil
}

func classifyFetchError(ctx context.Context, cfg Config, err error) error {
	details := map[string]any{
		"dataset":        Dataset,
		"currency":       cfg.Currency,
		"currency_denom": cfg.CurrencyDenom,
	}

	var upstreamErr *upstreamError

	switch {
	case errors.As(err, &upstreamErr):
		details["status"] = upstreamErr.Status

		return xerr.WithDetails(xerr.Wrap(
			xerr.Unavailable,
			"ECB_UNAVAILABLE",
			"ECB service returned an error",
			err,
		), details)
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
		return xerr.WithDetails(xerr.Wrap(
			xerr.DeadlineExceeded,
			"ECB_REQUEST_TIMEOUT",
			"ECB request timed out",
			err,
		), details)
	case errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled):
		return xerr.WithDetails(xerr.Wrap(
			xerr.Canceled,
			"ECB_REQUEST_CANCELED",
			"ECB request was canceled",
			err,
		), details)
	case isNetworkError(err):
		return xerr.WithDetails(xerr.Wrap(
			xerr.Unavailable,
			"ECB_UNAVAILABLE",
			"ECB service is unavailable",
			err,
		), details)
	default:
		return xerr.WithDetails(xerr.Wrap(
			xerr.Internal,
			"ECB_FETCH_FAILED",
			"failed to fetch ECB data",
			err,
		), details)
	}
}

func isNetworkError(err error) bool {
	var netErr net.Error

	return errors.As(err, &netErr)
}
