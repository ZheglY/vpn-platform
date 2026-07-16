# ADR 0013: Observability, Retention, and Privacy

Status: Accepted

Date: 2026-07-12

## Context

The platform needs observability and capacity planning without collecting browsing history or leaking secrets. Sandbox retention can be defined now, but production retention depends on legal decisions.

## Decision

Use Prometheus/OpenMetrics for metrics and OpenTelemetry for tracing.

Allowed aggregate statistics:

- bytes transferred by credential/node;
- node state and load;
- active credential count;
- temporary technical metrics.

Forbidden data:

- DNS requests;
- visited domains;
- destination IP addresses assigned by user traffic;
- packet contents;
- browsing history;
- full Telegram updates;
- raw YooKassa webhook bodies;
- subscription token/path;
- VLESS UUID;
- REALITY private keys.

Sandbox retention:

- application logs: 14 days;
- security/administrative audit: 365 days;
- diagnostic data: 30 days;
- aggregate traffic statistics: 30 days;
- payment records: no automatic deletion until legal requirements are determined.

Retention must be configurable and documented.

## Consequences

- Stage 8 must implement retention jobs and validate them.
- Metrics labels must avoid user IDs, payment IDs, token values, URL paths, and high-cardinality secrets.
- Privacy export/delete workflow must respect mandatory financial retention.
