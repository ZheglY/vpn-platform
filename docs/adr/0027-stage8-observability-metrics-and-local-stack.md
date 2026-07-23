# ADR 0027: Stage 8 observability metrics and local stack

- Status: Accepted for Stage 8 implementation
- Date: 2026-07-23
- Owners: platform and all HTTP service owners
- Security impact: High
- Contract impact: operational metrics only; no public business API change
- Extends: ADR 0013

## Context

Stage 8 needs actionable service health and latency data without turning monitoring into a source of subscription bearer tokens, user identifiers, payment identifiers, VPN credentials, Telegram data, or browsing history. The Access endpoint necessarily receives a secret in `/s/{token}`, and `net/http` services also contain identifier-bearing internal paths. Raw request paths therefore cannot be metric labels.

The local platform already exposes per-process Prometheus registries, but they previously contained only Go runtime metrics, were not scraped, and had no shared label policy, dashboard, alerts, or protected monitoring identity.

## Decision

1. Every HTTP process records the RED baseline as:
   - `vpn_platform_http_server_requests_total`;
   - `vpn_platform_http_server_request_duration_seconds`;
   - `vpn_platform_http_server_in_flight_requests`.
2. The only labels are the bounded service name, an allowlisted HTTP method, the registered `net/http` route pattern, and a status class. Unknown methods become `OTHER`; unmatched routes become `unmatched`; exact status codes and raw URLs are not labels.
3. Route labels are read from `Request.Pattern` after `ServeMux` routing. The code never derives a label from `Request.URL.Path`, `RawPath`, query parameters, headers, bodies, or path values. A regression test proves `/s/top-secret-subscription-token` produces `GET /s/{token}` and never the token.
4. A panic is counted as `5xx` and continues to the existing recovery middleware. The metrics writer exposes `Unwrap` for standard `http.ResponseController` compatibility and does not buffer response bodies.
5. `/metrics` requires a verified environment-bound service identity `observability`. The Telegram public listener no longer serves metrics; its protected internal mTLS listener does. Node agents use the same monitoring identity instead of their health-check identity.
6. The latest reviewed official Prometheus release is `v3.13.1`, commit `3c2a2ff7aec85b531d74954cc99da75844dc106a`. Its source archive is pinned by SHA-256 `a9a56eb599bbcc7c100385cb7c66193a94b723aaab70d43b2f1702f4c9d439ff`; its official prebuilt web UI is separately pinned by SHA-256 `2194bfbb5d36457df2b8f480037ce89786ee32f03b5ee6ad5597989f14deafb0`. The official image still fails the HIGH gate with gRPC-Go `v1.81.1`, so the repository security rebuild uses the pinned Go 1.26.5 builder, changes only gRPC-Go to `v1.82.1`, runs the upstream asset packer plus `go mod tidy` and `go mod verify`, and copies the binaries into the pinned distroless non-root base. A version/source/dependency change requires a fresh review and scan.
7. The reviewed Grafana release is `v13.1.1`, commit `a9cee6e1724a455676bb6c05eef7fc54aa4b19f4`. Its source archive is pinned by SHA-256 `722f7374891d375f0900dbee6e638d8b692edeba5d2353dd1d9315a3372cf78c`, and its official runtime base is pinned by manifest digest `sha256:7cb8c64c4d57a57e734073f3cc94620adb24a0acb929bd80ba9f14017e3a975b`. The repository rebuild changes gRPC-Go to `v1.82.1`, replaces the full Tempo server module with only the verified `pkg/tempopb` tree from Grafana's pinned Tempo dependency, and removes unused bundled Zipkin and Elasticsearch backend executables. The Grafana server graph uses that dependency only for protobuf DTOs; excluding unrelated server and plugin code reduces the shipped attack surface without changing the Prometheus dashboard path. Module verification, the Grafana health/dashboard smoke, and a zero HIGH/CRITICAL image scan are mandatory.
8. The local `obs` Compose runtime contains both security-rebuilt images. Both drop all Linux capabilities, enable `no-new-privileges`, use read-only root filesystems, bind host ports to loopback, and persist only their explicit data directories. Prometheus runs as distroless UID 65532, which reads the Docker Desktop host-mode-`0600` development client key without a root initializer or permission weakening.
9. Prometheus scrapes control-plane and optional node-agent targets over mTLS. The development certificate is client-only. Grafana uses provisioned, immutable dashboards and anonymous Viewer access because the local listener is loopback-only; there is no committed default administrator password.
10. Local Prometheus retention is 15 days. No remote-write exporter is enabled. Alert rules include repository runbook URLs. Production receivers, authentication, storage, and retention are separate deployment configuration and cannot inherit anonymous local Grafana settings.
11. This first Stage 8 slice does not claim completion of Kafka/DB/domain metrics, SLO burn-rate alerts, OpenTelemetry traces, Loki/Tempo, production alert routing, retention jobs, backups, Ansible, rotation drills, load/chaos tests, SBOM generation, signing, or production deployment.

## Consequences

- HTTP availability, error ratio, latency, and concurrency can be inspected consistently across services.
- Cardinality remains bounded by reviewed source route patterns rather than user-controlled values.
- Possession of any platform certificate is not sufficient to scrape operational data.
- Adding or changing a route does not require a metric API change, but reviewers must continue to reject user-controlled route-pattern construction.
- The local dashboard is not a production authentication model. Production observability must use private management networking, authenticated Grafana access, environment-specific retention, and approved alert receivers.
- The reduced Grafana dependency graph intentionally does not ship Tempo server code or the unused Zipkin/Elasticsearch backend executables. Adding those data sources later requires restoring and rescanning their reviewed implementations.

## Rejected alternatives

- Use raw URL paths and sanitize known secrets: rejected because new identifier-bearing routes would fail open.
- Label by user, payment, subscription, credential, event, request, or correlation ID: rejected because of disclosure and unbounded cardinality.
- Expose Telegram metrics on its public HTTP listener: rejected because monitoring data is internal.
- Permit every trusted mTLS certificate to scrape: rejected because transport trust is not endpoint authorization.
- Commit a local Grafana administrator password: rejected because repository defaults are copied into real environments.
