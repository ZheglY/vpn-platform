# ADR 0036: Stage 8 resilience budgets and failure drills

- Status: Accepted for Stage 8 implementation
- Date: 2026-07-26
- Owners: platform and all service owners
- Security impact: Medium
- Contract impact: test tooling only
- Extends: ADR 0008, ADR 0009, ADR 0024, ADR 0030, and ADR 0033

## Context

Health checks and isolated unit tests do not prove behavior during Kafka loss, database loss, node loss, or a rejected Xray reload. Failure exercises also need bounded load so a local or shared runner cannot accidentally become a traffic generator.

## Decision

1. `loadprobe` accepts its URL only through environment configuration, enforces TLS 1.3, discards bounded bodies, and emits only aggregate request/error/p50/p95/p99 data. It is capped at 5,000 requests per second, 256 workers, 30 minutes, and 100,000 samples.
2. The default CI load is 20 requests per second for 30 seconds. The public subscription negative path must keep unexpected-status/transport error ratio at or below 0.1% and p95 below 200 ms. The drill raises local source/token limits above this bounded workload and accepts only uniform not-found (`404`), preventing cheap rate-limit responses from masking lookup latency; all other statuses fail the probe. Longer soak duration or higher rate is opt-in and remains within hard bounds.
3. Kafka failure stops the broker after a payment event has been published, requeues the same durable outbox event, restores Kafka, and requires publication recovery with no additional subscription period.
4. PostgreSQL failure must preserve Access liveness, fail readiness, and recover readiness after the database restarts.
5. During an active credential, the drill stops primary node-agent/Xray, rewrites the local client to the independently provisioned failover endpoint, and requires real VLESS + REALITY traffic to the camouflage service before primary is restarted.
6. Linux systemd-manager tests prove a rejected candidate or reload failure restores last-known-good. Provisioning tests prove reconciliation and failover outcomes remain generation-fenced.
7. `make resilience-drill` owns its disposable Compose stack and removes containers and volumes on every exit. It never targets a URL outside explicit local configuration and never prints a subscription URL or credential.

## Consequences

- Resilience evidence observes business idempotency and actual VPN traffic, not only process health.
- Default drills are suitable for CI and local development but are not production capacity, multi-hour soak, regional disaster, or recovery-time evidence.
- Production failure injection requires an approved environment, blast-radius owner, abort thresholds, and incident coverage.

## Rejected alternatives

- Declare node failover successful from a healthy secondary healthcheck: rejected because no user traffic may be routable.
- Run an unbounded generic benchmark: rejected because target mistakes and runaway concurrency create operational risk.
- Delete state between Kafka retries: rejected because it would avoid testing the durable replay guarantees the platform depends on.
