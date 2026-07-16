# VPN Service Platform

Production-grade portfolio project for selling prepaid VPN subscriptions through a Telegram bot and delivering Happ-compatible subscription URLs backed by Xray-core nodes.

Current milestone: Stage 1 repository/platform foundation.

## What Exists Now

- One root Go module.
- Shared technical platform packages under `internal/platform`.
- Minimal `identity-service` template with `/livez`, `/readyz`, `/version`, and `/metrics`.
- Local Compose skeleton for PostgreSQL, Kafka in KRaft mode, Redis, and the identity service.
- Goose migration runner tool.
- OpenAPI/AsyncAPI contract linting.
- Makefile and CI verification workflow, including Go vulnerability checks, secret scan, image build, and image scan.

No business endpoints, payment logic, Telegram logic, VPN provisioning, or service database schema exist yet.

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
docker compose --profile core --profile app up --build
```

The identity service listens on `http://localhost:8080` by default:

- `GET /livez`
- `GET /readyz`
- `GET /version`
- `GET /metrics`

## Repository Rules

Read `AGENTS.md`, `PLANS.md`, and `docs/CODEX_VPN_PLATFORM_SPEC.md` before architecture, payment, security, or public-contract changes.
