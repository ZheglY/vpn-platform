# Observability

## Scope

This runbook covers the Stage 8 HTTP RED baseline and the local Prometheus/Grafana profile. It does not authorize production deployment or define a production alert receiver.

Never paste raw request paths, query strings, subscription URLs, VLESS UUIDs, Telegram payloads, provider payloads, destination IPs, DNS history, or packet data into a dashboard, alert, ticket, or diagnostic command.

## Start and validate

Generate local certificates and validate configuration:

```text
make observability-validate
```

Run the control-plane smoke with Prometheus and Grafana:

```text
make observability-smoke
```

For interactive local use, provide the normal local environment variables and start:

```text
docker compose --profile core --profile app --profile obs up --build
```

Local endpoints:

- Prometheus: `http://127.0.0.1:9090`
- Grafana: `http://127.0.0.1:3000`
- dashboard: folder `VPN Platform`, dashboard `VPN Platform Overview`

Grafana is anonymous Viewer-only and loopback-bound in the local profile. Do not expose port 3000 beyond the development host.

The repository Prometheus image is built from the integrity-checked official 3.13.1 source and prebuilt web UI with the approved gRPC-Go security override. Its distroless UID 65532 can read the Docker Desktop host-mode-`0600` development client key directly, so no root initializer or world-readable key is used.

Grafana is built from integrity-checked 13.1.1 source over its digest-pinned official runtime. The rebuild updates gRPC-Go, retains only the Tempo protobuf DTO dependency used by the Grafana server, and omits unused bundled Zipkin and Elasticsearch backend executables. Re-enabling either omitted data source requires a new dependency review, image scan, and dashboard smoke.

## Privacy checks

Allowed HTTP labels are `service`, bounded `method`, registered route template, and `status_class`. A safe Access sample is:

```text
route="GET /s/{token}"
```

Raw path values must never appear. Investigate any unexpected label before sharing or retaining data. Stop Prometheus, restrict access to its storage volume, and treat a leaked subscription token or VPN credential as a security incident using the access-delivery runbook.

## Target down

Alert: `VPNControlPlaneTargetDown`.

1. Confirm whether the target container/process is intentionally absent. Optional local node targets do not exist unless the `vpn` profile is running.
2. Inspect `up{job=~"vpn-control-plane|vpn-provisioning|vpn-nodes"}` and the target's last scrape error.
3. Check process health using its existing mTLS health identity; do not weaken `/metrics` authorization.
4. Check certificate expiry, SAN, CA bundle, and the `observability` client identity.
5. Check private management-network DNS and firewall reachability.
6. Restart only the failed owner service after preserving support-safe logs. Escalate repeated crashes to the owning service runbook.

Do not set `insecure_skip_verify`, expose `/metrics` publicly, or reuse an administrator certificate as a workaround.

## High HTTP error ratio

Alert: `VPNHTTPServerErrorRatioHigh`.

1. Identify `service`, route template, method, and status class. Do not seek a raw path.
2. Compare request rate and deployment/restart time to distinguish a low-volume artifact from a sustained failure.
3. Inspect structured logs by time window and bounded error code. Never search by subscription token or credential material.
4. Check owned dependencies and the relevant service runbook.
5. Roll back the latest release when error ratio rose immediately after deployment and rollback criteria are met.

## High HTTP latency

Alert: `VPNHTTPServerP95LatencyHigh`.

1. Confirm the service-level p95 over at least 15 minutes and compare request volume.
2. Break down by registered route pattern; do not add IDs or raw paths to the query.
3. Check DB pool saturation, Kafka/outbox backlog, external provider latency, and node management latency as applicable.
4. Capture only aggregate timings and bounded error categories.
5. Use a load test in a non-production environment before changing timeouts or capacity.

## Configuration recovery

Run `make observability-validate` before restart. Prometheus refuses invalid configuration. Grafana dashboards and data sources are provisioned from read-only repository files; UI edits are intentionally disabled.

If local data is corrupted, stop the profile and remove only the named `vpn-service_prometheus-data` or `vpn-service_grafana-data` development volume after confirming no investigation needs it. Production data deletion requires the approved retention and incident process.

## Current gaps

- No production Alertmanager receiver or escalation ownership.
- No Kafka, outbox/inbox, PostgreSQL pool, billing, subscription, provisioning, node capacity, or Xray reload metrics yet.
- No OpenTelemetry collector, Tempo, or Loki profile components yet.
- No SLO burn-rate rules yet.
- No production backup of observability state; dashboards remain reproducible from Git.
