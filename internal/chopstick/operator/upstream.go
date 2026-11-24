package operator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"wingman.com/chopstick/internal/platform/outbound"
)

func checkUpstreams(ctx context.Context, report *doctorReport, addresses []string, userAgent string) {
	if len(addresses) == 0 {
		return
	}

	client := outbound.NewClient(5 * time.Second)

	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	defer client.CloseIdleConnections()

	for _, address := range addresses {
		target, err := url.Parse(address)
		if err != nil || target.Scheme != "https" || target.Hostname() == "" || target.User != nil {
			report.check("FAIL", "upstream", "provide an HTTPS URL without embedded credentials")

			continue
		}

		probeUpstream(ctx, client, report, target, userAgent)
	}
}

func probeUpstream(ctx context.Context, client *http.Client, report *doctorReport, target *url.URL, userAgent string) {
	label := target.Host + target.EscapedPath()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		report.check("FAIL", "upstream", label+": could not build request")

		return
	}

	request.Header.Set("User-Agent", userAgent)

	response, err := client.Do(request)
	if err != nil {
		if requestErr, ok := errors.AsType[*url.Error](err); ok {
			err = requestErr.Err
		}

		report.check("FAIL", "upstream", fmt.Sprintf("%s: %v", label, err))

		return
	}
	defer func() { _ = response.Body.Close() }()

	level := "OK"
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		level = "FAIL"
	}

	report.check(level, "upstream", fmt.Sprintf("%s returned HTTP %d via the gateway", label,
		response.StatusCode))
}
