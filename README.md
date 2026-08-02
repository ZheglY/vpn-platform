# VPN Service Platform

Production-grade portfolio project for selling prepaid VPN subscriptions through a Telegram bot and delivering Happ-compatible subscription URLs backed by Xray-core nodes.

Current milestone: Stage 9 production-readiness review. Stage 8 was accepted by
the product owner on 2026-07-26. Stage 9 reviews architecture, security/privacy,
dependencies/licenses, legal/payment/provider requirements, production topology,
game-day recovery, and rollback without authorizing a production deployment.
The current production decision is `NO-GO` while external and staging hard gates
remain open.

## What Exists Now

- One root Go module.
- Shared technical platform packages under `internal/platform`.
- `identity-service` with Telegram identity, consent persistence, health, version, and metrics endpoints.
- `catalog-service` with immutable versioned plans, prices, regions, and published Telegram catalog queries.
- `billing-service` with immutable order snapshots, idempotent payment creation, YooKassa sandbox verification, webhook inbox, reconciliation, and transactional Kafka outbox.
- `subscription-service` with a separate database, payment/refund inbox, immutable entitlement periods, activation/extension, grace/expiry scheduler, refund recalculation, and transactional Kafka outbox.
- `access-service` with a separate database, ordered lifecycle and provisioning-outcome cursors, encrypted VLESS credentials, full assignment-bound revoke proof, sequenced transactional outbox, audited provisioning reads, one-time URL issuance/rotation, and a Happ-compatible no-store endpoint.
- `provisioning-service` with a separate database, cross-topic command and outcome ordering, transactional generation-fenced node placement, exact capacity reserve, durable operations/outbox, sanitized DLQ, health polling, and leased desired-vs-actual reconciliation.
- Two local mTLS node-agents that persist revision tombstones, validate and atomically apply pinned Xray-core configuration, manage a fixed child process, and complete candidate or last-known-good recovery despite request cancellation.
- `telegram-bot` with Telegram webhook dedupe, consent, `/plans`, and `/buy` sandbox purchase flow.
- `notification-service` with a separate database, ordered Kafka inbox, causal delivery-stream barriers, business deduplication, typed escaped templates, durable leases/retries, current-state suppression, and typed mTLS delivery through telegram-bot.
- `admin-service` and Go `admin-cli` with administrator mTLS identities, default-deny RBAC, fenced recoverable owner attempts, idempotent typed mutations, safe reads, and append-only audit.
- Local Compose stack with isolated service databases, Kafka, Redis, fake external APIs, and an optional two-node VLESS + REALITY data plane.
- Optional `obs` Compose profile with integrity-pinned security rebuilds of Prometheus 3.13.1, Grafana 13.1.1, Tempo 2.10.5, and Loki 3.7.2; a minimal OpenTelemetry Collector 0.157.0; provisioned read-only data sources/dashboard; explicit 8/11-target inventories; operational alerts; TLS 1.3 mTLS scraping; and TLS 1.3 mTLS OTLP ingress.
- SLI recording rules and multi-window burn alerts for control API availability, the public Happ subscription endpoint, and durable successful-payment fulfillment, plus an inert credential-free Alertmanager validation baseline.
- Owner-scoped, dry-run-first retention commands with bounded deletion, replay protection, Billing legal holds, and separate Admin audit deletion authority.
- Streaming age-encrypted PostgreSQL backups whose source inspection shares an exported MVCC snapshot, plus an eight-database exact-empty-target restore drill with integrity, ownership, recovery, and cleanup evidence.
- A Debian 12 Ansible node baseline with WireGuard-only management, nftables default deny, separate non-root node-agent/Xray systemd units, restricted credentials, and last-known-good rollback.
- Secret-free mTLS, provider-client, Access encryption/HMAC, and REALITY overlap/rollback drills.
- Bounded load and Kafka/PostgreSQL/node/Xray failure drills, including real VPN traffic through failover after primary loss.
- A complete classified custom-image inventory with SPDX SBOMs, retained digest-pinned vulnerability reports, exact tamper-evident checksums, and a manual least-privilege GitHub OIDC provenance workflow.
- Linux-safe credential init/verifier containers that stage allowlisted keys into per-owner named volumes with `0400`/`0440` modes before non-root services start.
- Goose migration runner tool.
- OpenAPI/AsyncAPI contract linting.
- Makefile and CI verification workflow, including Go vulnerability checks, secret scan, image build, and image scan.
- Stage 9 review artifacts with a machine-readable fail-closed go/no-go decision,
  exact 19-image license policy, incident/game-day/rollback runbooks, and
  evidence freshness rules.
- Provider-neutral staging/production schemas, intentionally incomplete
  secret-free templates, an exact database/Kafka/SPIFFE binding contract, and
  an offline digest/commit/configuration preflight.
- Uniform staging/production startup rejection of local/fake/default values,
  PostgreSQL without `verify-full`, Kafka without per-service mTLS, and Redis
  without TLS 1.3 server verification.

No real payments, refund initiation, production VPS enrollment, WireGuard deployment, or real user traffic exist yet. The `vpn` profile is local-only and uses generated development keys.

## Requirements

- Go 1.26.5.
- Node.js 24 and npm.
- Docker 29+ with Docker Compose.
- GNU Make.

## Local Checks

```powershell
npm install
make verify
make production-readiness
```

## Local Compose

Create a local `.env` from `.env.example` and replace placeholder values before starting Compose.

```powershell
Copy-Item .env.example .env
make compose-up
```

Internal service endpoints use generated development mTLS certificates. Use `make compose-smoke` or container healthchecks for routine checks:

- `GET /livez`
- `GET /readyz`
- `GET /version`
- `GET /metrics` with the `observability` mTLS identity

The telegram-bot service listens on `http://localhost:8081` by default:

- `POST /webhooks/telegram`
- `GET /livez`
- `GET /readyz`
- `GET /version`

Telegram metrics are served only by its internal mTLS listener on port 8090 and require the `observability` identity.

Catalog, billing, subscription, and access listen on `https://localhost:8083`, `https://localhost:8084`, `https://localhost:8086`, and `https://localhost:8087`. The YooKassa webhook is public TLS ingress on billing; internal order, payment, entitlement, URL issuance, and credential-material endpoints require an allowlisted service certificate. Happ fetches `GET /s/{token}` without a client certificate.

`make compose-smoke` exercises onboarding and a complete sandbox purchase through entitlement activation and access delivery. It runs real Billing, Subscription, and Access PostgreSQL suites, injects a provisioning result, verifies one-time issue replay, Happ headers/body, token-path redaction, event publication, and mTLS authorization. It does not start Xray.

`make observability-validate` checks both Prometheus inventories, SLO and operational rules, Alertmanager, Collector/Tempo/Loki configuration, the exact Tempo LTS OpenVEX correction, image licenses, staged-key readability under real container UIDs, and Grafana provisioning. `make observability-smoke` verifies all eight always-on targets, bounded owner snapshots, a known W3C trace in Tempo, a trace-correlated log in Loki, and telemetry redaction. `make stage7-smoke` enables `vpn` plus `obs` and verifies the complete 11-target inventory, Provisioning capacity snapshots, and both Xray health series on the full VPN path. Local Prometheus and Grafana listen on `127.0.0.1:9090` and `127.0.0.1:3000`; Collector, Tempo, and Loki have no host ports.

Maintenance commands are isolated behind the `maintenance` profile. Retention defaults to dry-run and must follow [the retention runbook](docs/runbooks/data-retention.md). `make backup-restore-drill` creates disposable local encryption material, restores all eight owner databases into an empty PostgreSQL instance, validates integrity and ownership, then removes the artifacts and volumes; see [the backup runbook](docs/runbooks/backup-restore.md).

`make node-hardening-test`, `make secret-rotation-drill`, and `make resilience-drill` exercise the remaining local Stage 8 operational controls. `make docker-build`, `make image-scan`, and `make release-bundle` share the same complete 19-image inventory. The release bundle requires a clean commit, binds every SPDX/Trivy report to an immutable image ID, and verifies the exact checksummed artifact set; the manual `release-attest` workflow adds repository-bound keyless provenance without publishing or deploying images.

`make production-readiness` validates that every required Stage 9 review,
runbook, hard gate, owner, evidence reference, and license decision agrees. A
green review validation can still report `NO-GO`; it means the blockers are
represented honestly, not that production is authorized. The stricter
`make license-publication-gate RELEASE_OUTPUT_DIR=<bundle>` remains blocked
until counsel/product decisions and final SBOM obligations are approved.
Readiness evidence is either immutable or has an explicit UTC expiry; stale or
future-dated evidence and Markdown/JSON drift fail the check.

An environment owner can validate a candidate without contacting any provider:

```powershell
make production-preflight ENVIRONMENT_CONFIG=D:\secure\production.json DEPLOY_ENVIRONMENT=production SOURCE_COMMIT=<reviewed-full-commit>
```

The committed templates intentionally fail this command. A pass does not
authorize deployment and does not change the current `NO-GO`; see
`docs/production/OWNER_INPUTS.md` and ADR 0042.

`make vpn-smoke` generates local-only keys on D and runs the full Stage 6 acceptance path: Access command outbox, Kafka, Provisioning, authenticated material and placement reads, two node-agents with the integrity-checked Xray-core `26.3.27` security rebuild, sequenced outcome consumption, one-time Happ profile issuance, real VLESS + REALITY traffic, refund-driven revoke, and proof that traffic no longer passes afterward.

`make stage7-smoke` runs that full VPN path and additionally proves one notification job per business fact, Telegram `429`/permanent error handling, causal suppression from extension retry to revoke, admin CLI mTLS and RBAC, unknown owner-outcome replay, safe audit, service restart recovery, and suppression of stale access-ready delivery after revoke.

The local admin API is bound to `127.0.0.1:8092` and still requires a generated administrator certificate. The repository seed contains development identities only. See `docs/runbooks/admin-operations.md`; do not use these certificates or seed files outside local Compose.

## Repository Rules

Read `AGENTS.md`, `PLANS.md`, and `docs/CODEX_VPN_PLATFORM_SPEC.md` before architecture, payment, security, or public-contract changes.
