# VPN Service Platform

Production-grade portfolio project for selling prepaid VPN subscriptions through a Telegram bot and delivering Happ-compatible subscription URLs backed by Xray-core nodes.

Current milestone: Stage 4 subscription lifecycle.

## What Exists Now

- One root Go module.
- Shared technical platform packages under `internal/platform`.
- `identity-service` with Telegram identity, consent persistence, health, version, and metrics endpoints.
- `catalog-service` with immutable versioned plans, prices, regions, and published Telegram catalog queries.
- `billing-service` with immutable order snapshots, idempotent payment creation, YooKassa sandbox verification, webhook inbox, reconciliation, and transactional Kafka outbox.
- `subscription-service` with a separate database, payment/refund inbox, immutable entitlement periods, activation/extension, grace/expiry scheduler, refund recalculation, and transactional Kafka outbox.
- `telegram-bot` with Telegram webhook dedupe, consent, `/plans`, and `/buy` sandbox purchase flow.
- Local Compose stack with isolated service databases, Kafka, Redis, fake Telegram API, and fake YooKassa API.
- Goose migration runner tool.
- OpenAPI/AsyncAPI contract linting.
- Makefile and CI verification workflow, including Go vulnerability checks, secret scan, image build, and image scan.

No real payments, refund initiation, VPN provisioning, Happ subscription URL, or access credential delivery exist yet. Subscription entitlement is implemented but does not mean VPN access is ready.

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

Catalog, billing, and subscription listen on `https://localhost:8083`, `https://localhost:8084`, and `https://localhost:8086`. The YooKassa webhook is public TLS ingress on billing; internal order, payment, and entitlement endpoints require an allowlisted service certificate.

`make compose-smoke` exercises onboarding and a complete sandbox purchase through entitlement activation. It runs real Billing and Subscription PostgreSQL suites, then verifies concurrent purchase clicks, ambiguous provider recovery, duplicate/out-of-order webhooks, Billing event publication, Subscription inbox processing, one immutable period, one activation event, and mTLS authorization.

## Repository Rules

Read `AGENTS.md`, `PLANS.md`, and `docs/CODEX_VPN_PLATFORM_SPEC.md` before architecture, payment, security, or public-contract changes.
