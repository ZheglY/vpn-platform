# Stage 9 System Inventory

- Review date: 2026-08-02
- Owner: Architecture
- Accepted baseline: `76f5624e664c556c3ec598055524319eab0af1f3`
- Stage 9 reviewed candidate: `dfee5bc44cadc8138e6ebe115cb7bf1ddab61b2a`
- Environment represented: local portfolio/sandbox only
- Production claim: none

## Bounded Contexts and Durable Stores

| Component | Owner responsibility | Durable store |
|---|---|---|
| `identity-service` | Telegram identity, user status, consent | `identity_service` PostgreSQL |
| `catalog-service` | Immutable plans, prices, regions | `catalog_service` PostgreSQL |
| `billing-service` | Orders, YooKassa sandbox payments, refunds, webhook verification/reconciliation | `billing_service` PostgreSQL |
| `subscription-service` | Paid periods and entitlement lifecycle | `subscription_service` PostgreSQL |
| `access-service` | Encrypted VLESS credentials, one-time subscription URL issuance, Happ document | `access_service` PostgreSQL |
| `provisioning-service` | Nodes, allocations, desired operations, reconciliation | `provisioning_service` PostgreSQL |
| `notification-service` | Ordered durable Telegram notification jobs | `notification_service` PostgreSQL |
| `admin-service` | Admin principals, RBAC, fenced owner actions, append-only audit | `admin_service` PostgreSQL |
| `telegram-bot` | Telegram webhook UX, typed delivery boundary | Redis ephemeral dedupe/FSM only |
| `node-agent` | Node desired/actual convergence and Xray last-known-good | Node-local journal and configuration |

The eight PostgreSQL databases use distinct local credentials. No service is permitted to connect to another owner's database. The provider-neutral contract now names separate runtime, migrator, backup, and restore roles for every database; actual production grants and denial evidence remain a Stage 9 infrastructure blocker.

## HTTP Surfaces

The OpenAPI contract defines 42 operations.

| Surface | Representative routes | Authentication and exposure |
|---|---|---|
| Telegram ingress | `POST /webhooks/telegram` | Public edge; Telegram webhook secret, TLS, body/rate limits, dedupe |
| YooKassa ingress | `POST /webhooks/yookassa` | Public edge; normalized inbox plus authenticated provider GET before fulfillment |
| Catalog | `GET /v1/plans` | Public read |
| Happ subscription | `GET /s/{token}` | Public bearer path on a dedicated hostname; uniform no-store response and path redaction |
| Internal service APIs | `/internal/v1/...` | TLS 1.3 mTLS with exact environment SPIFFE allowlists |
| Admin API | `/admin/v1/...` | Admin TLS 1.3 mTLS, local RBAC, typed routes, append-only audit |
| Node management | `/internal/v1/status`, `/internal/v1/credentials/{id}` | Provisioning/health mTLS over the management network; mutation restricted to provisioning identity |
| Health/version/metrics | `/livez`, `/readyz`, `/version`, `/metrics` | Health semantics per service; metrics require observability mTLS |

All Go HTTP servers use `net/http`, bounded body middleware, read/header/write/idle limits, request IDs, panic recovery, privacy-safe route logging, signal-aware shutdown, and bounded shutdown deadlines. Production reverse proxy behavior is not implemented by local Compose and remains a separate gate.

## Kafka Inventory

The AsyncAPI contract defines 19 versioned channels:

- Billing facts: `billing.payment.succeeded.v1`, `billing.payment.canceled.v1`, `billing.refund.succeeded.v1`.
- Subscription facts: `subscription.activated.v1`, `subscription.extended.v1`, `subscription.grace.started.v1`, `subscription.expired.v1`, `subscription.revoked.v1`.
- Access commands: `access.provision.request.v1`, `access.revoke.request.v1`.
- Sanitized command DLQs: `access.provision.request.v1.dlq`, `access.revoke.request.v1.dlq`.
- Provisioning outcomes: `access.provision.succeeded.v1`, `access.provision.failed.v1`, `access.revoke.succeeded.v1`, `access.revoke.failed.v1`.
- User-facing Access facts: `access.ready.v1`, `access.provisioning.failed.v1`, `access.revoked.v1`.

Business writes use transactional outbox/inbox and at-least-once delivery. Subscription lifecycle, Access commands/outcomes, and Notification delivery have explicit owner sequences and gap/collision handling. Staging/production clients now require TLS 1.3 mTLS and the machine contract enumerates per-service topics/groups; production broker topology, applied ACL denial, quotas, and retention are not selected or tested.

## Network and Trust Boundaries

| Compose network | Purpose | Production interpretation |
|---|---|---|
| `backend` | Services, PostgreSQL, Redis, Kafka, local provider fakes | Local convenience only; production segmentation and egress policy unresolved |
| `node-management` | Provisioning, node-agent, Collector | Must become private WireGuard plus firewall and mTLS |
| `vpn-data` | Xray client traffic and camouflage endpoint | Local VLESS + REALITY proof only |
| `observability` | Prometheus/Grafana/Collector/Tempo/Loki | Private local telemetry plane; production auth/tenancy/storage unresolved |

Local host ports bind to loopback. Production requires separate Telegram, subscription, and admin hostnames. No domain, DNS, edge, DDoS provider, VPS provider, cloud firewall, or production CA has been selected.

## mTLS and Management Identities

- Service identities use `spiffe://vpn-service/ns/<environment>/sa/<service>`.
- Admin identities use `spiffe://vpn-service/ns/<environment>/admin/<principal>`.
- Node-agent server identities and separate health clients are exact allowlist entries.
- Observability scrape and OTLP identities are separate from business identities.
- Runtime keys are staged into owner-specific volumes and verified under the real non-root UID.
- Production issuance, hardware-backed custody, expiry monitoring, revocation, compromise response, and break-glass authority remain unresolved.

## Observability Interfaces

- Prometheus uses explicit 8-target and 11-target TLS 1.3 mTLS inventories.
- Grafana is loopback-only anonymous Viewer in local Compose.
- Collector, Tempo, and Loki have no host ports.
- Traces use W3C TraceContext only; logs and attributes pass exact privacy allowlists.
- Alertmanager receivers are inert and contain no production contacts or credentials.
- Production authentication, tenancy, object storage, retention approval, receiver delivery, on-call, and escalation are hard gates.

## Release and Automation Inventory

The release inventory contains 19 custom images: 10 service runtimes, `admin-cli`, `credentialstage`, `migrate`, `backupctl`, and five observability images. The two fake provider images are explicitly excluded from production inventory. Release tooling creates commit-addressed image IDs, SPDX 2.3 SBOMs, Trivy reports, exact checksums, and a repository-bound manual attestation bundle. It does not publish images.

GitHub workflows:

- `verify.yml`: source, contract, image, scan, Linux credential, and smoke validation.
- `release-attest.yml`: manually approved keyless metadata attestation without registry or deployment permission.

Production registry, immutable OCI digest publication, per-image signatures/attestations, approval identity, canary deployment, and digest rollback are unresolved.

Provider-neutral deployment artifacts now include separate strict staging and
production schemas/templates, complete configuration classification, exact
service/database/Kafka/SPIFFE bindings, and an offline preflight that requires
the full reviewed commit and all 19 image digests. These artifacts contain no
provider choice or valid credential value and do not close external gates.

## Admission Result

Stage 8 was explicitly accepted and recorded before this branch. The ten required remediation controls were inspected; keyring/loadprobe focused tests and the disposable node-hardening test passed. Local HEAD and upstream matched, the worktree was clean, and no Stage 9 files existed before this inventory.
