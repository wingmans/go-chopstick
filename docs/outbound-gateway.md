# Outbound HTTP gateway

Chopstick sends every production request to an external HTTPS service through
the local throttling gateway. The gateway is the aggregate rate-limit and
destination-allowlist boundary for all concurrently running Chopstick
processes.

The default gateway address is `http://127.0.0.1:8090`. Override it with
`CHOP_OUTBOUND_GATEWAY_URL` when the gateway is intentionally available at a
different address. There is no configuration that falls back to a direct
external connection.

The development lifecycle generates disposable Basic Auth credentials in
`data/run/outbound-gateway.env`. The proxy receives the username and password
hash, while Chopstick loads the username and plaintext password automatically.
The ignored runtime file is a development coordination mechanism, not a local
confidentiality boundary.

## Policy

Chopstick owns [the gateway policy](../infra/outbound.rules). Its initial
limits are:

- `sec.gov` and its subdomains share one eight-request-per-second budget;
- Yahoo Finance receives five requests per second;
- the ECB Data API receives five requests per second;
- English Wikipedia receives five requests per second;
- every other destination is rejected.

Add a destination to the policy before adding application code that contacts
it. Restart the gateway with `make gateway-start` to apply a policy change.

## Development lifecycle

The gateway source checkout defaults to:

```text
../go-proxy-app/throttling
```

Override `CHOP_GATEWAY_DIR` when the checkout is elsewhere. Manage it with:

```bash
make gateway-start
make gateway-status
make gateway-stop
make gateway-reset
```

`make gateway-reset` replaces the local credentials and rebuilds the proxy.
No gateway username or password needs to be configured manually.

`make setup` tries to start the gateway. `chopd` checks gateway health and
credentials in the background and can serve viewers during a gateway outage.
Price download commands require a working gateway. These gateway checks use
a host denied by the allowlist and make no upstream request.

To test actual upstream requests through the gateway, pass one or more HTTPS
URLs to `chop doctor`. The command prints the HTTP status for each URL.

```bash
./bin/chop doctor \
  --upstream https://www.sec.gov/files/company_tickers.json \
  --upstream 'https://query1.finance.yahoo.com/v8/finance/chart/AAPL?range=5d&interval=1d'
```

An HTTP error can come from the gateway or the upstream. Use
`make gateway-status` to check the gateway and inspect its logs for routing
or authentication errors.

## Request path

The concrete client in `internal/platform/outbound` preserves the intended
upstream host, path, query, method, body, and headers while connecting to the
local gateway. The gateway validates the host, reserves a rate-limit slot,
and creates the HTTPS connection to the upstream service.

The client retries `429 Too Many Requests` responses with bounded exponential
backoff and jitter. It respects `Retry-After` up to the configured maximum
delay. If retries are exhausted, the final `429` response is returned to the
calling workflow.

Application unit tests may continue to inject local HTTP clients. Production
entry points and nil-client fallbacks use the gateway-backed client.

## Verification

The `go-proxy-app` test policy uses `example.com`; automated rate-limit tests
do not generate a burst against SEC. Before running a real workflow, use
`make gateway-status` to confirm that the gateway container is healthy.
