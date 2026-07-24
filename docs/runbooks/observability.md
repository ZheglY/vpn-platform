# Observability

## Scope

This runbook covers the Stage 8 HTTP and bounded operational metrics plus the local Prometheus/Grafana profile. It does not authorize production deployment or define a production alert receiver.

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

`make stage7-smoke` also enables observability during the full local VPN lifecycle and verifies all 11 expected scrape targets. CI runs both commands on native Ubuntu: the first proves the 8-target baseline, and the second proves the 11-target VPN inventory.

For interactive local use, provide the normal local environment variables and start:

```text
docker compose --profile core --profile app --profile obs up --build
```

When `vpn` and `obs` are enabled together, select the matching inventory before startup:

```powershell
$env:PROMETHEUS_CONFIG_FILE="./deploy/observability/prometheus/prometheus-vpn.yml"
docker compose --profile core --profile app --profile vpn --profile obs up --build
```

Local endpoints:

- Prometheus: `http://127.0.0.1:9090`
- Grafana: `http://127.0.0.1:3000`
- dashboard: folder `VPN Platform`, dashboard `VPN Platform Overview`

Grafana is anonymous Viewer-only and loopback-bound in the local profile. Do not expose port 3000 beyond the development host.

The repository Prometheus image is built from the peeled official 3.13.1 release commit and integrity-checked prebuilt web UI with the approved gRPC-Go security override. The image includes upstream `LICENSE` and `NOTICE`.

Runtime containers never bind-mount host private keys. Compose stages an explicit allowlist into separate named volumes using a network-isolated root init with only the file-ownership capabilities it needs. Private material is owner-readable `0400`; certificates are `0440`. A second container checks metadata and reads all non-root credentials as UID 65532 before any dependent service starts. A failure is terminal for startup. Never bypass the verifier, make a key `0644`, stage `ca.key`, or replace per-service volumes with one shared credential volume.

Grafana is built from integrity-checked 13.1.1 source over its digest-pinned official runtime. The rebuild updates gRPC-Go, retains only the Tempo protobuf DTO dependency used by the Grafana server, and omits unused bundled Zipkin and Elasticsearch backend executables. Re-enabling either omitted data source requires a new dependency review, image scan, and dashboard smoke.

## Privacy checks

Allowed HTTP labels are `service`, bounded `method`, registered route template, and `status_class`. A safe Access sample is:

```text
route="GET /s/{token}"
```

Raw path values must never appear. Investigate any unexpected label before sharing or retaining data. Stop Prometheus, restrict access to its storage volume, and treat a leaked subscription token or VPN credential as a security incident using the access-delivery runbook.

Operational labels are limited to reviewed SQL operation/outcome, allowlisted Kafka topic/direction/outcome/stage, durable `kind/state`, domain `kind/state`, node `status/type`, and Xray reload outcome. Never add a query, table, DSN, message key/header/payload, partition/offset, user/payment/subscription/credential/event/correlation/node ID, error text, or URL to a query, dashboard, or alert.

## Target down

Alert: `VPNControlPlaneTargetDown`.

1. Confirm whether the target container/process is intentionally absent. Optional local node targets do not exist unless the `vpn` profile is running.
2. Inspect `up{job=~"vpn-control-plane|vpn-provisioning|vpn-nodes"}` and the target's last scrape error.
3. Check process health using its existing mTLS health identity; do not weaken `/metrics` authorization.
4. Check certificate expiry, SAN, CA bundle, and the `observability` client identity.
5. Check private management-network DNS and firewall reachability.
6. Restart only the failed owner service after preserving support-safe logs. Escalate repeated crashes to the owning service runbook.

Do not set `insecure_skip_verify`, expose `/metrics` publicly, or reuse an administrator certificate as a workaround.

## Target inventory mismatch

Alerts: `VPNControlPlaneTargetInventoryMismatch`, `VPNProvisioningTargetInventoryMismatch`, and `VPNNodeTargetInventoryMismatch`.

1. Confirm that `PROMETHEUS_CONFIG_FILE` matches the active Compose profiles: baseline expects 8 targets; VPN expects 11.
2. Query `count by (job) (up)` and inspect `/targets`. A missing series is an inventory failure even when no `up == 0` sample exists.
3. Check whether the service was removed, renamed, detached from the network, or omitted from Compose. Do not lower the expected count to hide an unplanned outage.
4. If the topology changed intentionally, update the static inventory, count alert, `promtool` tests, dashboard, smoke expectation, and this runbook in one reviewed change.

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

## PostgreSQL pool saturation

Alert: `VPNPostgresPoolSaturated`.

1. Confirm sustained utilization by `service`; a short spike during startup is not saturation.
2. Compare query-class latency and pool empty-wait/canceled-acquire counters. SQL text and arguments are intentionally unavailable in metrics.
3. Check the owning service health, PostgreSQL resource pressure, lock waits, and recent migrations using support-safe database tooling.
4. Prefer fixing slow transactions, leaked rows/connections, or a failed dependency before raising pool size. Validate any pool change under non-production load.
5. Escalate repeated saturation with aggregate timings and bounded operation classes only.

## Kafka and durable backlog

Alerts: `VPNKafkaConsumerLagHigh` and `VPNDurableBacklogStuck`.

1. Identify the owner, allowlisted topic or durable `kind/state`, and when delay began. The Kafka gauge is observed handled-record age, not broker offset lag; its alert requires a recent processing observation.
2. Compare consume outcomes, retry stage, DLQ count, oldest durable age, and owner snapshot health.
3. Check Kafka availability, consumer readiness, PostgreSQL transactions, and outbox leases. Do not inspect or paste message keys/payloads unless following an approved incident procedure.
4. Use the owning service replay command/runbook only for a durable dead letter. Never edit an inbox/outbox row by hand.
5. If work is legitimately quiet, do not infer broker offset health from record age; use broker administration tooling without adding partition or offset labels to application metrics.

## Metrics snapshot failure

Alert: `VPNOperationalSnapshotFailed`.

1. Check whether only message/domain state or the owner-specific snapshot failed.
2. Verify the owning database is reachable and the one-second collector query is not blocked.
3. Compare the deployed migration version with the service binary. An unexpected state intentionally fails the snapshot closed and requires code/schema review.
4. Do not broaden an allowlist merely to silence the alert. Confirm the state is legitimate and add behavior, tests, dashboard, ADR, and runbook changes together.

## Owner scheduler lag

Alerts: `VPNBillingReconciliationLagHigh` and `VPNSubscriptionSchedulerLagHigh`.

1. Confirm both due count and lag; zero due work suppresses the alert.
2. For Billing, check provider reachability, reconciliation leases, and safe provider error classes. Never expose provider IDs or payloads.
3. For Subscription, check lifecycle worker readiness, PostgreSQL time, inbox ordering, and outbox backlog.
4. Follow the Billing or Subscription owner runbook before replaying work. Do not mutate payment or entitlement state directly.

## Node capacity and health

Alerts: `VPNNodeCapacityHigh` and `VPNNodeHeartbeatStale`.

1. Confirm the `vpn` profile or approved production node inventory is active and inspect aggregate capacity by bounded node status.
2. Correlate heartbeat age, node target `up`, Xray health, allocation state, and provisioning backlog.
3. Do not add node IDs, management URLs, credential IDs, or client UUIDs to metrics. Use the authenticated support API/runbook for a specific node investigation.
4. Drain a node before maintenance. Capacity changes require a placement/load review; an offline node must not be made active solely to clear an alert.

## Xray reload failure

Alerts: `VPNXrayUnhealthy` and `VPNXrayReloadFailed`.

1. Distinguish validation failure, restored rollback, failed rollback, and current unhealthy state.
2. Preserve support-safe node-agent logs and local revision metadata. Never print the rendered Xray config, VLESS UUID, or REALITY private key.
3. A restored rollback keeps the last-known-good config but still requires investigation. A failed rollback or unhealthy Xray is critical and the node must stop receiving new allocations.
4. Use the provisioning runbook to drain/reconcile. Do not bypass Xray validation or manually inject clients.

## Configuration recovery

Run `make observability-validate` before restart. It stages and reads the Prometheus key as UID 65532, validates both the 8-target and 11-target configurations, and executes target, HTTP, PostgreSQL, durable-workflow, scheduler, and node rule tests. Prometheus refuses invalid configuration. Grafana dashboards and data sources are provisioned from read-only repository files; UI edits are intentionally disabled.

If local data is corrupted, stop the profile and remove only the named `vpn-service_prometheus-data` or `vpn-service_grafana-data` development volume after confirming no investigation needs it. Production data deletion requires the approved retention and incident process.

## Current gaps

- No production Alertmanager receiver or escalation ownership.
- No broker offset-lag exporter or Kafka broker dashboard yet; application lag is observed record age.
- No OpenTelemetry collector, Tempo, or Loki profile components yet.
- No SLO burn-rate rules yet.
- No production backup of observability state; dashboards remain reproducible from Git.
