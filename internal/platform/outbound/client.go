// Package outbound provides Chopstick's mandatory external HTTP client.
package outbound

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"wingman.com/chopstick/internal/platform/appdata"
	"wingman.com/chopstick/internal/platform/envfile"
)

const (
	DefaultGatewayURL = "http://127.0.0.1:8090"
	GatewayURLEnv     = "CHOP_OUTBOUND_GATEWAY_URL"
	GatewayUserEnv    = "CHOP_OUTBOUND_GATEWAY_USER"
	GatewayPassEnv    = "CHOP_OUTBOUND_GATEWAY_PASS"

	defaultMaxRetries = 4
	defaultBaseDelay  = 250 * time.Millisecond
	defaultMaxDelay   = 30 * time.Second
)

// NewClient returns a concrete HTTP client that can reach external HTTPS
// services only through the configured gateway.
func NewClient(timeout time.Duration) *http.Client {
	loadErr := envfile.Load(appdata.Path(
		"run", "outbound-gateway.env",
	))

	return newClientWithError(
		timeout, gatewayURLFromEnvironment(), loadErr,
	)
}

func newClient(timeout time.Duration, rawGatewayURL string) *http.Client {
	return newClientWithError(timeout, rawGatewayURL, nil)
}

func newClientWithError(
	timeout time.Duration, rawGatewayURL string, loadErr error,
) *http.Client {
	gatewayURL, configErr := parseGatewayURL(rawGatewayURL)
	user, password, credentialsErr := gatewayCredentialsFromEnvironment()
	configErr = errors.Join(loadErr, configErr, credentialsErr)

	return &http.Client{
		Timeout: timeout,
		Transport: &retryTransport{
			next: &gatewayTransport{
				gatewayURL: gatewayURL,
				configErr:  configErr,
				user:       user,
				password:   password,
				next:       directTransport(),
			},
			maxRetries: defaultMaxRetries,
			baseDelay:  defaultBaseDelay,
			maxDelay:   defaultMaxDelay,
			jitter:     randomJitter,
		},
	}
}

func gatewayCredentialsFromEnvironment() (string, string, error) {
	user := strings.TrimSpace(os.Getenv(GatewayUserEnv))
	password := strings.TrimSpace(os.Getenv(GatewayPassEnv))

	if user == "" && password == "" {
		return "", "", nil
	}

	if user == "" || password == "" {
		return "", "", fmt.Errorf(
			"%s and %s must be configured together",
			GatewayUserEnv, GatewayPassEnv,
		)
	}

	return user, password, nil
}

func directTransport() *http.Transport {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Transport{}
	}

	cloned := transport.Clone()
	cloned.Proxy = nil

	return cloned
}

func gatewayURLFromEnvironment() string {
	if value := strings.TrimSpace(os.Getenv(GatewayURLEnv)); value != "" {
		return value
	}

	return DefaultGatewayURL
}

func parseGatewayURL(value string) (*url.URL, error) {
	gatewayURL, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", GatewayURLEnv, err)
	}

	if gatewayURL.Scheme != "http" && gatewayURL.Scheme != "https" {
		return nil, fmt.Errorf("%s must use http or https", GatewayURLEnv)
	}

	if gatewayURL.Host == "" {
		return nil, fmt.Errorf("%s must include a host", GatewayURLEnv)
	}

	if gatewayURL.User != nil || gatewayURL.Path != "" ||
		gatewayURL.RawQuery != "" || gatewayURL.Fragment != "" {
		return nil, fmt.Errorf(
			"%s must not include credentials, a path, query, or fragment",
			GatewayURLEnv,
		)
	}

	return gatewayURL, nil
}

type gatewayTransport struct {
	gatewayURL *url.URL
	configErr  error
	user       string
	password   string
	next       http.RoundTripper
}

func (t *gatewayTransport) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	if t.configErr != nil {
		return nil, t.configErr
	}

	if request.URL.Scheme != "https" {
		return nil, fmt.Errorf(
			"outbound gateway only supports HTTPS destinations: %s",
			request.URL.Redacted(),
		)
	}

	if port := request.URL.Port(); port != "" && port != "443" {
		return nil, fmt.Errorf(
			"outbound gateway only supports HTTPS port 443: %s",
			request.URL.Redacted(),
		)
	}

	forwarded := request.Clone(request.Context())
	forwarded.URL.Scheme = t.gatewayURL.Scheme
	forwarded.URL.Host = t.gatewayURL.Host
	forwarded.URL.User = nil

	forwarded.Host = request.URL.Hostname()
	if t.user != "" {
		forwarded.SetBasicAuth(t.user, t.password)
	}

	return t.next.RoundTrip(forwarded)
}

type retryTransport struct {
	next       http.RoundTripper
	maxRetries int
	baseDelay  time.Duration
	maxDelay   time.Duration
	jitter     func(time.Duration) time.Duration
}

func (t *retryTransport) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		current, err := cloneRequest(request)
		if err != nil {
			return nil, err
		}

		response, err := t.next.RoundTrip(current)
		if err != nil {
			return nil, err
		}

		if response.StatusCode != http.StatusTooManyRequests ||
			attempt >= t.maxRetries || !canRetry(request) {
			return response, nil
		}

		if err := discardAndClose(response.Body); err != nil {
			return nil, err
		}

		delay := t.retryDelay(response, attempt)

		timer := time.NewTimer(delay)
		select {
		case <-request.Context().Done():
			timer.Stop()

			return nil, request.Context().Err()
		case <-timer.C:
		}
	}
}

func cloneRequest(request *http.Request) (*http.Request, error) {
	cloned := request.Clone(request.Context())
	if request.Body == nil || request.Body == http.NoBody {
		return cloned, nil
	}

	if request.GetBody == nil {
		return cloned, nil
	}

	body, err := request.GetBody()
	if err != nil {
		return nil, fmt.Errorf("recreate outbound request body: %w", err)
	}

	cloned.Body = body

	return cloned, nil
}

func canRetry(request *http.Request) bool {
	if request.Body != nil && request.Body != http.NoBody &&
		request.GetBody == nil {
		return false
	}

	switch request.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func (t *retryTransport) retryDelay(
	response *http.Response, attempt int,
) time.Duration {
	if delay := retryAfter(response.Header.Get("Retry-After"), time.Now()); delay > 0 {
		return min(delay, t.maxDelay)
	}

	delay := t.baseDelay * time.Duration(1<<attempt)

	delay = min(delay, t.maxDelay)
	if t.jitter != nil {
		delay += t.jitter(delay)
	}

	return min(delay, t.maxDelay)
}

func retryAfter(value string, now time.Time) time.Duration {
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

func randomJitter(delay time.Duration) time.Duration {
	maximum := max(delay/4, time.Millisecond)

	value, err := rand.Int(rand.Reader, big.NewInt(int64(maximum)))
	if err != nil {
		return 0
	}

	return time.Duration(value.Int64())
}

func discardAndClose(body io.ReadCloser) error {
	if body == nil {
		return nil
	}

	_, readErr := io.Copy(io.Discard, body)
	closeErr := body.Close()

	return errors.Join(readErr, closeErr)
}

var (
	_ http.RoundTripper = (*gatewayTransport)(nil)
	_ http.RoundTripper = (*retryTransport)(nil)
)
