# VPN Platform

Production codebase for selling prepaid VPN subscriptions through Telegram, accepting YooKassa payments, issuing Happ-compatible subscription URLs, and provisioning VLESS + REALITY access on Xray-core nodes.

Production rollout is fail-closed. The current decision is `NO-GO` until the legal, provider, license, PKI, HA/PITR, alerting, registry, capacity, and production-staging gates in [Stage 9 readiness](docs/reviews/stage9/go-no-go-checklist.md) are approved.

## Stack

- Go 1.26.5, standard `net/http`, `pgx/v5`, and `zap`
- PostgreSQL database-per-service
- Kafka with transactional outbox/inbox
- Redis for ephemeral state only
- Telegram Bot API, YooKassa, Happ, Xray-core
- Docker Compose, Prometheus, Grafana, Tempo, Loki, OpenTelemetry

## Local Start

Requirements: Docker 29+, Docker Compose, Go 1.26.5, Node.js 24, npm, and GNU Make.

```powershell
Copy-Item .env.example .env
# Replace every placeholder in .env with a unique local value.
npm ci
make compose-config
make compose-up
```

The local stack generates disposable development mTLS and Xray material. Never reuse `.env`, `secrets/dev-mtls`, or `secrets/dev-xray` outside local development.

Stop the stack:

```powershell
make compose-down
```

## Validation

Fast security and contract checks:

```powershell
make secret-scan
make filesystem-secret-scan
make contracts
make production-readiness
```

Workflow checks:

```powershell
make compose-smoke
make vpn-smoke
make observability-smoke
make stage7-smoke
```

Full release candidate verification:

```powershell
make verify
make backup-restore-drill
make node-hardening-test
make secret-rotation-drill
make resilience-drill
```

## Release

```powershell
make release-bundle
make license-publication-gate RELEASE_OUTPUT_DIR=<bundle-directory>
make production-preflight ENVIRONMENT_CONFIG=D:\secure\production.json DEPLOY_ENVIRONMENT=production SOURCE_COMMIT=<reviewed-40-character-commit>
```

Do not publish images or deploy when the license gate, preflight, or Stage 9 decision is blocked. Follow [deployment and rollback](docs/production/deployment-and-rollback.md) and [release operations](docs/runbooks/release.md).

## Operations

| Task | Command or document |
|---|---|
| Health | `GET /livez`, `GET /readyz`, `GET /version` |
| Metrics | `GET /metrics` with the `observability` mTLS identity |
| Backup/restore | `make backup-restore-drill` and [runbook](docs/runbooks/backup-restore.md) |
| Retention | [data retention runbook](docs/runbooks/data-retention.md) |
| Incident response | [incident runbook](docs/runbooks/incident-response.md) |
| Admin operations | [admin runbook](docs/runbooks/admin-operations.md) |
| VPN nodes | [node hardening runbook](docs/runbooks/vpn-node-hardening.md) |

## Documentation

- [Contribution rules](CONTRIBUTING.md)
- [Product specification](docs/VPN_PLATFORM_SPEC.md)
- [Architecture](docs/architecture.md)
- [Threat model](docs/threat-model.md)
- [API and event contracts](contracts/)
- [Production owner inputs](docs/production/OWNER_INPUTS.md)
- [Runbooks](docs/runbooks/README.md)
