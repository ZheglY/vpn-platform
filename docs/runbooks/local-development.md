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
- `go test ./...` (`scripts/test.ps1` retries in Docker on Windows if Application Control blocks generated test binaries)
- `go test -race ./...` (`scripts/test.ps1 -Race` uses Docker when needed)
- `golangci-lint`
- `govulncheck`
- Gitleaks secret scan
- `npm audit --audit-level=high`
- OpenAPI lint
- AsyncAPI parser validation
- billing and subscription event JSON Schema example validation
- identity-service, catalog-service, billing-service, subscription-service, and telegram-bot Docker builds
- Trivy HIGH/CRITICAL vulnerability scan for service images
- `docker compose config --quiet`
- `git diff --exit-code`

On Windows, `make test` and `make race` first try the local Go toolchain. If Windows Application Control blocks generated test binaries, the script retries inside a Linux `golang:1.26.5` container. Race tests also use Docker when no local C compiler is on `PATH`.
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

Identity, catalog, billing, and subscription internal routes use HTTPS with service identity authorization in local Compose. Readiness checks include synchronous dependencies and Kafka where used. Local `telegram-api` and `yookassa-api` fakes prevent calls to real providers. `make compose-smoke` removes Compose volumes to verify all service databases and migrations from zero.

Stage 2 smoke also verifies:

- synthetic Telegram `/start` update through webhook, bot, identity-service, PostgreSQL, Redis, and fake Telegram API
- concurrent duplicate update returns retryable `503` while the first update is still processing
- completed duplicate replay returns `200` without a second Telegram message
- consent acceptance persists exactly once
- identity-service rejects a valid but unauthorized mTLS SPIFFE identity

Stage 3 smoke also verifies:

- PostgreSQL concurrency, uniqueness, trigger, transaction rollback, lease recovery, and provider-create deadline behavior
- configured plan seed and immutable order snapshot flow through `/buy`
- provider create committed before an ambiguous `500` is recovered with the same provider idempotency key
- concurrent purchase updates with different command keys reuse one open order, one payment, and one provider object
- duplicate YooKassa webhook is durably deduplicated
- terminal state is accepted only after provider GET verifies account, test mode, amount, currency, metadata, and status
- an out-of-order canceled notification cannot reverse a succeeded payment or create another event
- one `billing.payment.succeeded.v1` is visible in Kafka
- billing rejects a valid but unauthorized mTLS SPIFFE identity

Stage 4 smoke also verifies:

- Subscription PostgreSQL tests for concurrent extensions, immutable snapshots, atomic outbox, exact period/grace boundaries, lease recovery, and refund ordering
- `subscription-service` consumes the single verified payment event through its durable inbox
- the Billing order read accepts `subscription-service` and still rejects non-allowlisted identities
- one immutable paid period activates one subscription and publishes one `subscription.activated.v1`
- the entitlement endpoint accepts `telegram-bot` mTLS identity and rejects a different valid identity

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
docker compose exec -T catalog-service /catalog-service healthcheck
docker compose exec -T billing-service /billing-service healthcheck
docker compose exec -T subscription-service /subscription-service healthcheck
```

Automated smoke:

```powershell
make compose-smoke
```

## Notes

- Local Compose credentials are development-only and must never be reused in production.
- Local development mTLS keys are generated secrets and must never be committed.
- Redis is ephemeral in this project and is not a source of truth.
- Identity, catalog, billing, and subscription use distinct logical databases and credentials even though local Compose shares one PostgreSQL instance.
- `TERMS_URL` must point to the immutable terms document matching `CONSENT_VERSION`; local Compose defaults to `https://example.invalid/terms/terms-v1`.
- `YOOKASSA_SHOP_ID` and `YOOKASSA_SECRET_KEY` are local fake-provider values. Never place real credentials in `.env` or run Stage 3 against a production shop.
