# ADR 0028: Stage 8 bounded operational metrics

- Status: Accepted for Stage 8 implementation
- Date: 2026-07-24
- Owners: platform, billing, subscription, access, provisioning, notification, and node owners
- Security impact: High
- Contract impact: operational metrics only; no public business API or Kafka schema change
- Extends: ADR 0013 and ADR 0027

## Context

The HTTP RED baseline detects request failures but cannot distinguish PostgreSQL exhaustion, delayed Kafka processing, a stuck transactional outbox/inbox, an overdue owner scheduler, exhausted VPN-node capacity, or a failed Xray reload. These signals are operationally necessary, but their natural source data includes SQL, message keys, partitions, offsets, aggregate IDs, payment/subscription/credential IDs, node IDs, and VPN material that must not become metric labels.

Each service owns its database and domain vocabulary. A shared metrics package may provide technical collectors, but it cannot query another service database or define cross-service domain entities.

## Decision

1. PostgreSQL instrumentation is attached while constructing each `pgxpool.Pool`. `pgxpool.Stat` exports pool size and cumulative acquisition data. The pgx query tracer exports duration using only `service`, a fixed SQL operation class, and `success|error`. It never exports SQL text, arguments, statement names, table names, DSNs, or PostgreSQL error text.
2. Kafka instrumentation uses franz-go producer/fetch hooks plus explicit consumer/outbox observations. Topics are an owner-supplied finite allowlist and unknown values collapse to `other`. Direction, handler outcome, and retry stage are fixed enums. No key, payload, partition, offset, event/aggregate/correlation ID, header value, or error text is exported.
3. `vpn_platform_kafka_consumer_lag_seconds` is the capped record age observed when an allowlisted record is handled, not broker offset lag. It is capped at 30 days and paired with a last-observed timestamp so alerts ignore stale observations from quiet topics. Offset and partition cardinality are intentionally excluded.
4. Owner-local PostgreSQL collectors export explicit zero-valued series for durable backlog and domain states. Each collector has a one-second query bound, an exact allowlist of `kind/state` pairs, and a `snapshot_success` gauge. Query failure, duplicate output, negative values, or an unexpected state fails the whole snapshot closed instead of dynamically creating a label.
5. Billing exports payment states and reconciliation due count/lag. Subscription exports lifecycle states and scheduler due count/lag. Access exports credential and operation states. Provisioning exports operation/allocation/node states, operation duration, capacity/utilization, heartbeat age, and observed config revision. Notification exports pending/retry/permanent-failure and DLQ backlog. Node-agent exports active-client count, config revision, Xray health, and bounded reload outcome/duration.
6. Node identity is represented only by Prometheus scrape target metadata. Application metrics do not add `node_id`. VPN credentials, REALITY material, subscription URLs, user/payment data, destination data, and traffic metadata are absent from all series.
7. Operational alerts cover sustained pool saturation, observed Kafka delay, nonempty old durable work, failed snapshots, owner scheduler lag, node capacity/heartbeat, Xray health, and failed reloads. Every alert links to the repository runbook and has a `promtool` regression test for representative behavior.
8. The provisioned dashboard contains only aggregate bounded dimensions. Local retention, protected scraping, immutable provisioning, and loopback-only Grafana remain as defined by ADR 0027.

## Consequences

- Operators can separate transport, database, durable-workflow, owner-scheduler, and node failures without searching by user or credential identifiers.
- A new domain state, Kafka topic, backlog state, or reload outcome requires an explicit code and review change. This is deliberate cardinality and disclosure control.
- Owner snapshots add bounded read load at scrape time. Their one-second timeout and snapshot health series expose rather than hide query failures.
- Observed Kafka record age is useful for delayed processing but is not a replacement for future broker offset-lag monitoring.
- No public OpenAPI, AsyncAPI, event schema, or database ownership boundary changes.

## Rejected alternatives

- Export SQL statements, table names, Kafka keys, partitions, offsets, or aggregate IDs: rejected for disclosure and cardinality risk.
- Build one central collector with credentials to every service database: rejected because it violates database-per-service ownership.
- Discover labels from database values or topic names at runtime: rejected because unexpected data would fail open.
- Use logs as the only source for retries and backlog state: rejected because logs are unsuitable for bounded alert evaluation and can encourage identifier searches.
