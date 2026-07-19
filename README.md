# VPN Service Platform

Production-grade portfolio project for selling prepaid VPN subscriptions through a Telegram bot and delivering Happ-compatible subscription URLs backed by Xray-core nodes.

Current milestone: Stage 6 provisioning control plane and node-agent.

## What Exists Now

- One root Go module.
- Shared technical platform packages under `internal/platform`.
- `identity-service` with Telegram identity, consent persistence, health, version, and metrics endpoints.
- `catalog-service` with immutable versioned plans, prices, regions, and published Telegram catalog queries.
- `billing-service` with immutable order snapshots, idempotent payment creation, YooKassa sandbox verification, webhook inbox, reconciliation, and transactional Kafka outbox.
- `subscription-service` with a separate database, payment/refund inbox, immutable entitlement periods, activation/extension, grace/expiry scheduler, refund recalculation, and transactional Kafka outbox.
- `access-service` with a separate database, ordered lifecycle cursor, encrypted VLESS credentials, revision-bound revoke proof, sequenced transactional outbox, audited provisioning reads, one-time URL issuance/rotation, and a Happ-compatible no-store endpoint.
- `provisioning-service` with a separate database, cross-topic command ordering, transactional node placement, capacity reserve, durable operations/outbox, sanitized DLQ, health polling, and desired-vs-actual reconciliation.
- Two local mTLS node-agents that persist revision tombstones, validate and atomically apply pinned Xray-core configuration, manage a fixed child process, and restore last-known-good after failure.
- `telegram-bot` with Telegram webhook dedupe, consent, `/plans`, and `/buy` sandbox purchase flow.
- Local Compose stack with isolated service databases, Kafka, Redis, fake external APIs, and an optional two-node VLESS + REALITY data plane.
- Goose migration runner tool.
- OpenAPI/AsyncAPI contract linting.
- Makefile and CI verification workflow, including Go vulnerability checks, secret scan, image build, and image scan.

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
- `GET /metrics`

The telegram-bot service listens on `http://localhost:8081` by default:

- `POST /webhooks/telegram`
- `GET /livez`
- `GET /readyz`
- `GET /version`
- `GET /metrics`

Catalog, billing, subscription, and access listen on `https://localhost:8083`, `https://localhost:8084`, `https://localhost:8086`, and `https://localhost:8087`. The YooKassa webhook is public TLS ingress on billing; internal order, payment, entitlement, URL issuance, and credential-material endpoints require an allowlisted service certificate. Happ fetches `GET /s/{token}` without a client certificate.

`make compose-smoke` exercises onboarding and a complete sandbox purchase through entitlement activation and access delivery. It runs real Billing, Subscription, and Access PostgreSQL suites, injects a provisioning result, verifies one-time issue replay, Happ headers/body, token-path redaction, event publication, and mTLS authorization. It does not start Xray.

`make vpn-smoke` generates local-only keys on D, starts two node-agents with the integrity-checked Xray-core `26.3.27` security rebuild, runs Provisioning PostgreSQL tests, provisions a credential through the mTLS desired-state API, verifies VLESS + REALITY traffic with the official client image, replays the operation idempotently, revokes it, and verifies removal and last-known-good rollback.

## Repository Rules

Read `AGENTS.md`, `PLANS.md`, and `docs/CODEX_VPN_PLATFORM_SPEC.md` before architecture, payment, security, or public-contract changes.
