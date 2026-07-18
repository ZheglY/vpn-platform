# Project Plans

## Current Approval

Approved milestone: Stage 5 - Access and Happ subscription delivery.

Stage 4 was accepted by the user's instruction to begin the next milestone. Stage 6 and later implementation milestones are not approved. Do not create node placement, Xray mutation, notification delivery, or production VPN infrastructure until the relevant milestone is explicitly approved.

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

Status: accepted baseline; pushed to GitHub branch `codex/stage2-identity-telegram`.

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

Status: accepted after acceptance-review remediation on `codex/stage3-catalog-billing`.

Depends on Stage 2 and sandbox payment decisions. Adds immutable plan/order snapshots, YooKassa sandbox adapter, payment idempotency, webhook inbox, verification, reconciliation, and fake provider tests.

Acceptance criteria:

- Catalog plan versions and prices are immutable; an order stores the complete selected plan, price, region, and accepted terms snapshot.
- Internal order/payment retries use durable request-scoped idempotency and cannot create a second compatible open order, payment, or provider object.
- Ambiguous YooKassa create responses are reconciled with the persisted provider idempotency key within the provider's idempotency window.
- Webhook payloads create only normalized inbox records; terminal effects require authenticated provider GET verification.
- Payment and order transition plus one versioned Kafka outbox event commit atomically and remain monotonic under duplicate or out-of-order delivery.
- Compose starts from empty volumes and the Stage 3 smoke verifies ambiguous create, duplicate click, duplicate webhook, out-of-order webhook, mTLS authorization, and one Kafka event.
- `make verify`, `make compose-smoke`, and final diff review pass before Stage 3 is declared complete.

Verification completed:

- PostgreSQL integration suite for concurrent different-key payment creation, constraints/triggers, atomic terminal outbox behavior, lease recovery, and provider-create deadline boundaries
- `make verify`
- `make compose-smoke`
- Final review of transaction boundaries, idempotency, state monotonicity, mTLS allowlists, contract compatibility, redaction, and service ownership

Notes:

- Direct `govulncheck` access to the Go vulnerability service timed out; the repository's fallback used a temporary local copy of the official Go vulnerability database and reported no vulnerabilities.

### Stage 4 - Subscription lifecycle

Depends on Stage 3 payment events. Adds entitlement state machine, extension/expiry/revocation logic, scheduler/reconciler, inbox, and time-boundary tests.

Status: accepted. Hardening implementation and required verification completed on `codex/stage4-subscription-lifecycle` after review of commit `3ab6bdc`; the user explicitly approved beginning the next milestone on 2026-07-18.

Acceptance criteria:

- `subscription-service` owns a separate PostgreSQL database and never reads another service database.
- Existing `billing.payment.succeeded.v1` remains compatible; immutable entitlement terms are fetched through Billing's allowlisted mTLS order read API and validated against the event.
- Payment consumption atomically commits inbox, immutable period, final subscription state, and exactly one activation, extension, or direct-expiry outbox event.
- Duplicate event IDs, duplicate payment IDs, concurrent different payments, delayed delivery, and consumer restarts do not duplicate or shorten entitlement.
- The scheduler uses authoritative PostgreSQL time, transitions active to grace and grace to expired at exact UTC boundaries, recovers expired leases, and emits one terminal event.
- Full refund events are idempotent, can wait for an out-of-order payment, permanently dead-letter incompatible durable state, and revoke current access for terminal or future-period-gap outcomes.
- Outbox workers preserve monotonic lifecycle delivery per subscription aggregate under concurrent claims.
- Public HTTP and Kafka contracts, ADRs, threat model, runbooks, Compose, migrations, and contract tests match implemented behavior.
- PostgreSQL integration tests, `make verify`, `make compose-smoke`, and final security/concurrency/transaction review pass before Stage 4 is declared complete.

Non-goals:

- No subscription URL or token generation.
- No Happ document rendering, VLESS credentials, Xray provisioning, or node operations.
- No Telegram notification delivery or production refund initiation.

### Stage 5 - Access and Happ subscription delivery

Depends on Stage 4. Adds token generation/hash/rotation, subscription endpoint, VLESS + REALITY URI rendering, Happ compatibility tests, no-store responses, and redaction tests.

Status: implementation and required verification completed on `codex/stage5-access-happ`; pending user acceptance. Stage 6 remains blocked until explicit approval.

Acceptance criteria:

- `access-service` owns a separate PostgreSQL database and consumes subscription lifecycle and provisioning outcome events through a durable, payload-free inbox.
- Activation or extension creates at most one current encrypted VLESS credential and atomically emits a secret-free `access.provision.request.v1` through a transactional outbox.
- Provisioning success is the only transition to `active` or `degraded`; it stores a validated public endpoint snapshot and emits one secret-free `access.ready.v1` event.
- VLESS UUIDs use versioned AES-256-GCM encryption at rest. Subscription tokens use 256 bits from `crypto/rand`; only an HMAC-SHA-256 lookup value is persisted.
- URL issuance and rotation are serialized per subscription. Plaintext URLs exist only in the successful response; replay of a completed idempotency key returns a safe conflict because the plaintext cannot be reconstructed.
- Unknown, expired, revoked, and malformed public tokens receive the same `404 text/plain` response with `Cache-Control: no-store`; request logs record only `GET /s/{token}`.
- Happ responses include compatible profile and expiry headers and deterministic VLESS + REALITY share URIs backed by golden tests.
- Terminal entitlement events invalidate tokens synchronously and emit a revoke command; actual node allocation, Xray mutation, reconciliation, and real-Xray e2e remain Stage 6.
- OpenAPI, AsyncAPI/JSON Schema, ADRs, threat model, runbook, Compose, migrations, contract tests, `make verify`, and `make compose-smoke` match implemented behavior.

Risks and dependencies:

- Stage 5 cannot prove a live VPN connection without Stage 6. Tests inject a contract-valid provisioning result and never report access ready before that result.
- The Stage 4 lifecycle event does not contain the selected region. Stage 6 must version the placement command or obtain the immutable region through an approved service API before node allocation; Stage 5 does not guess a region.
- Loss of an issued plaintext URL before Telegram delivery requires explicit rotation. Storing a replayable encrypted URL is rejected because it increases credential exposure.

Non-goals:

- No node registry, capacity selection, desired-state reconciliation, node-agent, Xray configuration changes, or production VPS access.
- No Happ Provider ID, HWID/device enforcement, traffic accounting, or advanced app-management flags.
- No Telegram delivery changes; telegram-bot integration with the synchronous issue API is a later approved change.

### Stage 6 - Provisioning control plane and node-agent

Depends on Stage 5 and threat review. Adds node registry, allocation, mTLS protocol, idempotent desired revision, Xray validation, atomic reload, rollback, and local real-Xray e2e tests.

### Stage 7 - Notifications and admin operations

Depends on lifecycle events. Adds durable Telegram notifications, admin CLI/internal API, RBAC, and audit.

### Stage 8 - Observability, hardening, and deployment

Depends on working services. Adds dashboards, alerts, runbooks, backup/restore drill, node hardening, secret rotation, privacy retention jobs, SBOM, and signing.

### Stage 9 - Production readiness review

Depends on previous milestones. Performs architecture, security, legal/payment, license, incident, backup, and rollback reviews before any real launch.
