# Project Plans

## Current Approval

Approved milestone: Stage 2 - Identity and Telegram onboarding.

Not approved yet: Stage 3 and later implementation milestones. Do not create catalog, billing, YooKassa, subscription lifecycle, access credential delivery, provisioning, notification, or VPN business logic until the relevant milestone is explicitly approved.

## Stage 0 Plan

### Goals

- Initialize a local Git repository.
- Preserve the source specification in `docs/CODEX_VPN_PLATFORM_SPEC.md`.
- Record product decisions, unresolved questions, risks, and assumptions.
- Establish architecture, threat model, contract versioning, ADRs, and Definition of Done.
- Fix the Go repository layout conflict from the original specification.
- Create OpenAPI/AsyncAPI skeletons without implementing endpoints or services.

### Non-Goals

- No business code.
- No database migrations.
- No Docker Compose or Dockerfiles.
- No CI implementation.
- No calls to real YooKassa, Telegram, Happ, or Xray systems.
- No production VPS, domains, certificates, or secrets.

### Deliverables

- `AGENTS.md`
- `PLANS.md`
- `docs/architecture.md`
- `docs/threat-model.md`
- `docs/privacy.md`
- `docs/product-decisions.md`
- `docs/open-questions.md`
- `docs/risk-register.md`
- `docs/definition-of-done.md`
- `docs/external-sources.md`
- `docs/contracts/versioning.md`
- ADRs under `docs/adr/`
- `contracts/http/openapi.yaml`
- `contracts/events/asyncapi.yaml`
- `contracts/events/schemas/envelope.schema.json`

### Acceptance Criteria

- All unknowns affecting money, law, privacy, public API, event semantics, and VPN provisioning are either decided or listed as open questions.
- Documentation consistently uses the corrected `services/<service>/cmd/<binary>` layout.
- ADRs cover monorepo layout, sync/async boundaries, database isolation, Kafka client/naming, migrations, money, token storage, Xray management, mTLS, payment verification, refund/revocation, admin surface, retention, and provisioning readiness.
- Contract skeletons contain versioning rules and safe redaction requirements.
- Repository contains no Go business code, migrations, Docker Compose, or service implementation.

### Verification

- Inspect repository tree.
- Inspect `git status --short`.
- Search for forbidden Stage 1 artifacts: `*.go`, migration SQL under service directories, `compose.yml`, service Dockerfiles.
- Search documentation for the obsolete root `cmd/` service layout.
- Review diff as documentation reviewer for consistency, security, and unresolved blockers.

### Rollback

Stage 0 changes are documentation-only. If a decision is wrong, supersede the ADR with a new ADR instead of silently editing history after implementation starts.

## Later Milestones

### Stage 1 - Repository/platform foundation

Status: accepted baseline; pushed to GitHub `main`.

Depends on Stage 0 approval. Introduces Go module, platform primitives, local infra Compose, checks, linting, and one template service. Must not add domain entities to shared packages.

Deliverables:

- Root `go.mod` using Go 1.26.5.
- Technical platform packages under `internal/platform` only.
- Minimal `identity-service` template with health/version/metrics only.
- Local Compose for PostgreSQL, Kafka, Redis, and identity-service.
- Goose migration runner without business migrations.
- Makefile, lint config, contract tooling, CI workflow, README/runbook updates.
- Strict mTLS identity extraction and TLS server config primitive.

Verification completed:

- `make verify`
- `make compose-smoke`
- Compose smoke waits for Postgres, Redis, Kafka, and identity-service health checks and verifies identity-service HTTP endpoints.

Non-goals:

- No Identity business endpoints or durable domain schema.
- No Telegram integration.
- No payment, subscription, access, provisioning, notification, or Xray business behavior.
- No production secrets, production VPS, or real provider calls.

### Stage 2 - Identity and Telegram onboarding

Status: review fixes implemented and verified locally; pending user acceptance.

Depends on Stage 1. Adds identity service, Telegram webhook adapter, update dedupe, Redis FSM, consent versioning, local mTLS for bot-to-identity calls, and fake Telegram tests.

Deliverables:

- Identity-service PostgreSQL migration for users, Telegram identities, and consent versions.
- Identity-service internal endpoints for Telegram identity upsert, user lookup, consent acceptance, and consent status.
- Telegram-bot service with webhook secret validation, body limits, Redis update dedupe using `processing`/`completed` states, Redis FSM, webhook rate limiting, `/start`, and consent acceptance.
- Local Compose migration job, identity-service, telegram-bot, fake Telegram API, and generated dev-mTLS certificates.
- Contract updates for implemented Stage 2 HTTP endpoints.
- Tests for identity handlers, strict JSON decoding, Telegram webhook idempotency/retry behavior, blocked-user fail-closed behavior, Redis rate limiting, safe Telegram API errors, dev-mTLS permissions, and fake Telegram API client calls.

Non-goals:

- No tariff catalog, orders, YooKassa, payment webhooks, subscription lifecycle, access URLs, Xray provisioning, or notifications.

Verification completed:

- `go test ./...`
- `go test -race ./...`
- `make compose-smoke`
- `make fmt-check tidy-check vet test race lint vuln secret-scan npm-audit contracts docker-build image-scan compose-config`
- `git diff --check`

Additional Stage 2 smoke acceptance:

- synthetic update flows through telegram-bot, mTLS identity-service, PostgreSQL, Redis, and fake Telegram API
- concurrent duplicate update has a single side effect and returns retryable status for the in-flight duplicate
- completed duplicate replay is safely acknowledged without a second side effect
- wrong SPIFFE identity is rejected by identity-service

Notes:

- `govulncheck` direct network fetch timed out locally and the scripted fallback used a temporary copy of the official Go vulnerability database; result: no vulnerabilities found.
- `telegram-bot /readyz` checks Redis and an mTLS `identity-service /livez` request.

### Stage 3 - Catalog, Billing, and YooKassa sandbox

Depends on Stage 2 and payment/legal ADR confirmation. Adds immutable plan/order snapshots, YooKassa sandbox adapter, payment idempotency, webhook inbox, verification, reconciliation, and fake provider tests.

### Stage 4 - Subscription lifecycle

Depends on Stage 3 payment events. Adds entitlement state machine, extension/expiry/revocation logic, scheduler/reconciler, inbox, and time-boundary tests.

### Stage 5 - Access and Happ subscription delivery

Depends on Stage 4. Adds token generation/hash/rotation, subscription endpoint, VLESS + REALITY URI rendering, Happ compatibility tests, no-store responses, and redaction tests.

### Stage 6 - Provisioning control plane and node-agent

Depends on Stage 5 and threat review. Adds node registry, allocation, mTLS protocol, idempotent desired revision, Xray validation, atomic reload, rollback, and local real-Xray e2e tests.

### Stage 7 - Notifications and admin operations

Depends on lifecycle events. Adds durable Telegram notifications, admin CLI/internal API, RBAC, and audit.

### Stage 8 - Observability, hardening, and deployment

Depends on working services. Adds dashboards, alerts, runbooks, backup/restore drill, node hardening, secret rotation, privacy retention jobs, SBOM, and signing.

### Stage 9 - Production readiness review

Depends on previous milestones. Performs architecture, security, legal/payment, license, incident, backup, and rollback reviews before any real launch.
