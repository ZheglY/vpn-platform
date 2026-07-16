# Product Decision Log

Accepted on 2026-07-12 for Stage 0 and future MVP planning.

| ID | Decision | Status | Notes |
|---|---|---|---|
| PD-001 | Project is portfolio/sandbox only. No real sales yet. | Accepted | Jurisdiction and target countries are deferred until legal review. |
| PD-002 | First version uses prepaid one-time periods only. | Accepted | No saved payment method and no recurring billing. |
| PD-003 | MVP has one test plan: 30 days, no hard traffic cap, one selected region, one primary node, one failover, 24h grace. | Accepted | Price and currency come from seed configuration, not domain constants. |
| PD-004 | Purchase during active subscription extends from `current_period_end`; purchase after expiry starts from confirmed payment time. | Accepted | Must be covered by boundary-time tests in Stage 4. |
| PD-005 | v1 supports only full operator-initiated refunds. | Accepted | Partial refunds are not supported. |
| PD-006 | Confirmed full refund is tied to the specific payment-funded subscription period. | Accepted | Revoke access only if recalculation leaves no valid current/future paid entitlement. |
| PD-007 | Subscription receives primary node in selected region plus one failover node. | Accepted | User is not given all platform nodes. |
| PD-008 | Happ HWID and device limit are not used in v1. | Accepted | No unused device fields or interfaces should be introduced early. |
| PD-009 | Only aggregate traffic and health statistics are allowed. | Accepted | No DNS, domains, destination IPs, packet content, or browsing history. |
| PD-010 | Domains, DNS/TLS, and VPS providers are not selected yet. | Deferred | Must have separate hostnames for bot webhook, subscription endpoint, and admin API. |
| PD-011 | VPN VPS provider must allow VPN/proxy under its AUP. | Accepted | Production provider choice requires review. |
| PD-012 | Management plane uses private WireGuard network plus mTLS. | Accepted | Public internet exposure is forbidden. |
| PD-013 | YooKassa is sandbox-only in v1. | Accepted | Receipts, 54-FZ, VAT, and required buyer data are deferred. |
| PD-014 | Payment provider adapter should allow future receipt fields without changing subscription domain model. | Accepted | Billing owns payment provider details. |
| PD-015 | Admin v1 is protected CLI plus internal admin API with RBAC and audit. | Accepted | No web-admin in v1. |
| PD-016 | Initial SLOs are the specification SLOs. | Accepted | Must be revisited after measurement. |
| PD-017 | Capacity reserve is at least 20%; no new assignments after 80% configured node limit. | Accepted | Placement policy tests required in Stage 6. |
| PD-018 | Sandbox retention: app logs 14d, audit 365d, diagnostics 30d, aggregate traffic 30d, payment records kept until legal decision. | Accepted | Retention must be configurable and documented. |
| PD-019 | No geoblocking in sandbox. | Accepted | Production launch must define allowed jurisdictions. |
| PD-020 | Support/abuse contacts are placeholders in sandbox. | Accepted | Runbook requirements must exist before production. |
| PD-021 | Lost subscription URL is handled by token rotation. | Accepted | Old plaintext token is never shown again. Default rotation is immediate. |
| PD-022 | VPN access becomes `ready` only after primary node success; entitlement `active` is separate. | Accepted | Failover failure means `degraded`, visible in metrics/admin/alerting. |
| PD-023 | Kafka client is `franz-go`. | Accepted | Stage 1 must pin current stable version after changelog review. |
| PD-024 | Migrations use `goose` with SQL migrations and migration jobs. | Accepted | Stage 1 must define exact command and version. |
| PD-025 | API/event specs use OpenAPI 3.1, AsyncAPI, and JSON Schema. | Accepted | Contract linting is required in CI. |
| PD-026 | PostgreSQL access uses `pgx/v5` and `pgxpool`. | Accepted | No ORM. |
| PD-027 | HTTP uses standard `net/http`. | Accepted | `http.ServeMux` unless a later ADR changes it. |
| PD-028 | Logging uses `zap`. | Accepted | JSON in production, console locally. |
| PD-029 | Redis client is `go-redis/v9`. | Accepted | Redis remains ephemeral only. |
| PD-030 | Integration tests use Testcontainers-Go. | Accepted | Fake providers still allowed at ports. |
| PD-031 | Observability uses Prometheus/OpenMetrics and OpenTelemetry. | Accepted | Payloads and token paths are never traced. |
| PD-032 | Internal APIs use mTLS service identity from a platform CA. | Accepted | Private network is defense in depth, not authentication. |
| PD-033 | Subscription URL is issued only after provisioning readiness through a synchronous one-time bot call. | Accepted | Kafka never carries the URL or token. |
| PD-034 | Provisioning fetches credential material from access-service over mTLS. | Accepted | Kafka carries only credential ID, operation ID, and revision. |
| PD-035 | Revoke lifecycle has explicit request, succeeded, failed events and reconciliation. | Accepted | Access remains `revoking` until assigned nodes confirm removal. |

## Decisions Requiring Later Approval

- Real sales launch, jurisdiction, taxes, KYC, receipt data, and buyer data.
- Production domain, DNS/TLS provider, and VPS provider.
- Happ device limit or HWID support.
- Partial refunds or recurring payments.
- Production retention for financial records.
