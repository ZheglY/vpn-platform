# VPN Service Platform

Production-grade portfolio project for selling prepaid VPN subscriptions through a Telegram bot and delivering Happ-compatible subscription URLs backed by Xray-core nodes.

Current milestone: Stage 2 identity and Telegram onboarding.

## What Exists Now

- One root Go module.
- Shared technical platform packages under `internal/platform`.
- `identity-service` with Telegram identity, consent persistence, health, version, and metrics endpoints.
- `telegram-bot` with Telegram webhook secret validation, update dedupe, Redis FSM, `/start`, consent prompt, and health/version/metrics endpoints.
- Local Compose skeleton for PostgreSQL, Kafka in KRaft mode, Redis, identity-service, telegram-bot, and a fake Telegram Bot API.
- Goose migration runner tool.
- OpenAPI/AsyncAPI contract linting.
- Makefile and CI verification workflow, including Go vulnerability checks, secret scan, image build, and image scan.

No payment logic, tariff catalog, VPN provisioning, access credential delivery, or subscription lifecycle exists yet.

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

The identity service listens on `https://localhost:8080` in local Compose and requires generated development mTLS certificates. Use `make compose-smoke` or the container healthcheck for routine checks:

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

`make compose-smoke` sends synthetic Telegram updates through the full local Stage 2 path and verifies identity persistence, consent persistence, duplicate handling, Redis behavior, fake Telegram side effects, and mTLS authorization.

## Repository Rules

Read `AGENTS.md`, `PLANS.md`, and `docs/CODEX_VPN_PLATFORM_SPEC.md` before architecture, payment, security, or public-contract changes.
