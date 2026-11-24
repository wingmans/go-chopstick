package outbound

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"wingman.com/chopstick/internal/platform/appdata"
	"wingman.com/chopstick/internal/platform/envfile"
)

// Check verifies gateway configuration, HTTP health, and authentication
// without requesting data from an upstream provider.
func Check(ctx context.Context) error {
	if err := envfile.Load(appdata.Path("run", "outbound-gateway.env")); err != nil {
		return err
	}

	gateway, err := parseGatewayURL(gatewayURLFromEnvironment())
	if err != nil {
		return err
	}

	user, password, err := gatewayCredentialsFromEnvironment()
	if err != nil {
		return err
	}

	client := &http.Client{
		Timeout:   3 * time.Second,
		Transport: directTransport(),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	defer client.CloseIdleConnections()

	if err := checkResponse(ctx, client, gateway.String()+"/healthz",
		"", "", "", http.StatusOK); err != nil {
		return fmt.Errorf("gateway %s health check: %w", gateway, err)
	}

	// The gateway authenticates before checking its host allowlist. This
	// reserved host never needs an upstream and should be denied with 403.
	if err := checkResponse(ctx, client, gateway.String()+"/",
		"chopstick.invalid", user, password, http.StatusForbidden); err != nil {
		return fmt.Errorf("gateway %s authentication check: %w", gateway, err)
	}

	return nil
}

func checkResponse(ctx context.Context, client *http.Client, address, host,
	user, password string, want int,
) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}

	request.Host = host
	if user != "" {
		request.SetBasicAuth(user, password)
	}

	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusProxyAuthRequired {
		return errors.New("gateway rejected the configured credentials")
	}

	if response.StatusCode != want {
		return fmt.Errorf("HTTP %d, expected %d", response.StatusCode, want)
	}

	return nil
}
