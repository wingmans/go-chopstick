# Go proxy follow-up

Use the following prompt for the follow-up work in
`wingmans/go-proxy-app`. The relevant application is the `throttling`
experiment, not the separate `authorization` proof of concept.

## Prompt

Harden the `throttling` component as a mandatory outbound HTTPS gateway for
Chopstick development. Keep the implementation small. Chopstick runs on the
host, and the gateway runs in a Docker container. A sidecar deployment is not
needed.

The gateway is a safety boundary. Chopstick must never make a direct connection
to an external HTTP service, and a newly introduced external service must not
work until an explicit proxy rule is added. Keep the proxy implementation
generic: application-specific hosts and limits belong to a supplied policy,
not to the gateway image or its source code.

Support a small, declarative rule file that can express:

- a named rate bucket;
- a rate for that bucket;
- one or more exact hosts or wildcard host patterns assigned to the bucket;
- rejection of every destination absent from the policy.

The policy format should make shared budgets explicit. The following is an
illustrative format, not a required parser syntax:

```text
sec        8r/s  sec.gov  *.sec.gov
yahoo      5r/s  query1.finance.yahoo.com
ecb        5r/s  data-api.ecb.europa.eu
wikipedia  5r/s  en.wikipedia.org
```

The bucket name is significant. All hosts assigned to `sec` must consume one
aggregate budget rather than receiving independent per-host budgets. Return
HTTP `429 Too Many Requests` when an allowed destination exceeds its limit.
Do not queue excess requests.

Do not bake the illustrative Chopstick policy into the gateway image. Accept a
policy path through a Make variable such as `RULES_FILE`. Fail with a useful
message when no policy is supplied; never fall back to an unrestricted proxy.
The proxy repository may include a neutral example policy using reserved
example domains and deterministic test policies.

The later Chopstick integration will own its real policy file. It will contain
the SEC, Yahoo, ECB, and Wikipedia hosts and limits shown above. A different
application must be able to reuse the same locally built gateway image with a
different policy, without rebuilding the gateway binary.

Do not use a standard HTTPS forward proxy: connection reuse hides individual
requests inside a CONNECT tunnel and defeats request-rate enforcement. Accept
local HTTP requests that preserve the intended HTTPS upstream in the `Host`
header, apply the policy to every request, and reverse proxy to that host over
HTTPS. Reject non-HTTPS upstream destinations.

Remove basic authentication from the throttling gateway. Authentication in the
separate `authorization` experiment solves a different problem and should not
be changed. Publish the throttling gateway only on the host loopback interface,
using `127.0.0.1:8090` by default. It must not be reachable from the LAN.

Improve the container lifecycle:

- Pin the Caddy base image and the rate-limit `xcaddy` module version.
- Do not build or run production-facing artifacts from `latest` tags.
- Make the container healthcheck exercise the running gateway instead of only
  validating the Caddy configuration file.
- Preserve simple Make targets for build, start, stop, status, logs, and a
  curl-based smoke test.
- Make `make up` idempotent.
- Build a named, versioned local image rather than an anonymous or `latest`
  image.
- Keep the normal workflow entirely local: clone the repository, run
  `make build`, and start it with a supplied policy file.
- Keep future registry publication possible, but do not add GitHub Actions or
  require GHCR as part of this work.

Add integration coverage at the proxy boundary. A `make run-curl` style test
is sufficient; do not build application-level dependency injection or a proxy
test framework. Verify at least:

- permitted local requests are forwarded to the intended HTTPS upstream;
- each configured test service is limited by its intended rule;
- multiple hosts assigned to one bucket share the same budget;
- an excess request receives `429`;
- an unknown destination is rejected;
- startup fails when the rule file is missing or invalid;
- changing only the rule file does not require rebuilding the image;
- requests fail when the proxy is stopped;
- the published port is bound only to `127.0.0.1`.

Prefer deterministic local test upstreams where practical. Do not send a
large burst of test traffic to SEC merely to prove the limiter works. A small
manual connectivity check may contact SEC, but the automated rate-limit test
must not depend on SEC availability.

Update the throttling README with:

- the policy-file format and validation rules;
- named aggregate rate buckets;
- the default gateway URL and client request contract;
- the fail-closed behavior for unknown destinations;
- build, start, verification, and troubleshooting commands;
- local image naming and versioning;
- optional future image publication, clearly marked as unnecessary;
- instructions for adding a new allowed destination intentionally.

Do not modify Chopstick as part of this task. The later Chopstick integration
will use `http://127.0.0.1:8090` as its default mandatory proxy, add bounded
backoff for `429` responses, and arrange for `make run` to start and verify the
gateway before starting Chopstick. Its concrete HTTP client will rewrite the
connection destination to the gateway while preserving the upstream host,
path, query, method, body, and headers. It will never fall back to a direct
connection. Chopstick will also supply its rule file to the gateway lifecycle
command.


For your current single-machine development setup, I recommend local builds:
- Pin the Caddy base image and plugin versions.
- Build a named local image, such as chopstick-throttling-proxy:0.1.0.
- Let Docker cache the expensive xcaddy build layers.
- Have make up build automatically when the image is missing.
- Keep make build available for deliberate rebuilds.
- Record the source commit and configuration in the image metadata if useful.
- Make Chopstick’s make run call the proxy repository’s make up.
- Do not add GitHub Actions or GHCR yet.
A registry becomes worthwhile when you need reproducible installation on
multiple machines, faster onboarding without compiler builds, CI deployment,
or versioned releases independent of a source checkout.

One qualification: a locally built image is reproducible only if the base
image and Caddy module versions are pinned. Without that, rebuilding the same
Git commit later may produce a different proxy.

So I would remove GHCR publication from the immediate requirements and retain
it as an optional future step. The local clone is sufficient for the safety
net you need now.
