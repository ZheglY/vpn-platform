# Local Development Runbook

## Verify

```powershell
npm install
make verify
```

`make verify` runs:

- `gofmt` check without rewriting files
- `go mod tidy -diff`
- `go vet ./...`
- `go test ./...`
- `go test -race ./...`
- `golangci-lint`
- `govulncheck`
- Gitleaks secret scan
- `npm audit --audit-level=high`
- OpenAPI lint
- AsyncAPI parser validation
- identity-service and telegram-bot Docker builds
- Trivy HIGH/CRITICAL vulnerability scan for service images
- `docker compose config --quiet`
- `git diff --exit-code`

On Windows, `make race` uses local `gcc` when available. If no C compiler is on `PATH`, it runs the race test inside a Linux `golang:1.26.5` container.
On Windows, `make vuln` first tries the default `govulncheck` network client and falls back to a temporary local copy of the official Go vulnerability database if that client cannot download the database reliably.
Before the first baseline commit, Gitleaks scans the working tree; after `HEAD` exists, it scans Git history.
`npm audit` is retried because the npm advisory endpoint occasionally resets long-running local checks.
`make image-scan` requires Docker access and scans the already built local service images.
Tool versions are pinned in `Makefile`; override them only for explicit upgrade work.

## Compose

Create `.env` from `.env.example` and replace placeholders.

```powershell
Copy-Item .env.example .env
make compose-up
```

Local ports bind to `127.0.0.1` only. Kafka exposes `localhost:9094` for host tools and `kafka:9092` for containers on the Compose network. `make compose-up`, `make compose-config`, and `make compose-smoke` generate local development mTLS material under ignored `secrets/dev-mtls`.

Identity-service uses HTTPS with client-certificate authentication in local Compose. Telegram-bot calls it over mTLS, and bot readiness includes that check. `make compose-smoke` removes Compose volumes to verify database bootstrap and migrations from zero.

Stop:

```powershell
make compose-down
```

## Health Checks

```powershell
docker compose exec -T identity-service /identity-service healthcheck
Invoke-RestMethod http://localhost:8081/livez
Invoke-RestMethod http://localhost:8081/readyz
Invoke-RestMethod http://localhost:8081/version
```

Automated smoke:

```powershell
make compose-smoke
```

## Notes

- Local Compose credentials are development-only and must never be reused in production.
- Local development mTLS keys are generated secrets and must never be committed.
- Redis is ephemeral in this project and is not a source of truth.
- Stage 2 creates only the identity-service schema. Other service schemas remain future-stage work.
