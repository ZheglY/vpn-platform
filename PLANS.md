# Project Plans

## Current Approval

Approved milestone: Stage 8 - Observability, hardening, and deployment.

Stage 7 was explicitly accepted by the product owner on 2026-07-23 with the instruction to begin Stage 8. Stage 9 is not approved. Stage 8 may add observability, operational hardening, reproducible deployment assets, drills, and release controls. It must not connect production VPS instances, deploy production VPN infrastructure, configure real alert receivers, or use production credentials without a separate explicit approval.

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

Status: accepted on 2026-07-19 after review remediation and required verification on `codex/stage5-access-happ`.

Acceptance criteria:

- `access-service` owns a separate PostgreSQL database and consumes subscription lifecycle and provisioning outcome events through a durable, payload-free inbox.
- Activation or extension creates at most one current encrypted VLESS credential and atomically emits a secret-free `access.provision.request.v1` through a transactional outbox.
- Subscription lifecycle envelopes carry a producer-owned sequence; Access atomically persists its last applied sequence, defers gaps without acknowledging them, rejects stale collisions, and checks provisioning eligibility against PostgreSQL time.
- Provisioning success is the only transition to `active` or `degraded`; it stores a validated public endpoint snapshot and emits one secret-free `access.ready.v1` event.
- A provisioning success delayed beyond entitlement expiry atomically starts revoke and emits no readiness. Revoke succeeds only with exact unique node-set proof bound to desired and allocation revisions, including a valid zero-allocation no-op.
- VLESS UUIDs use versioned AES-256-GCM encryption at rest. Subscription tokens use 256 bits from `crypto/rand`; only an HMAC-SHA-256 lookup value is persisted.
- URL issuance and rotation are serialized per subscription. Plaintext URLs exist only in the successful response; replay of a completed idempotency key returns a safe conflict because the plaintext cannot be reconstructed.
- Global URL idempotency keys and per-subscription issuance are both serialized, and the public Redis IP/token decision is atomic across both counters.
- Unknown, expired, revoked, and malformed public tokens receive the same `404 text/plain` response with `Cache-Control: no-store`; request logs record only `GET /s/{token}`.
- Happ responses include compatible profile and expiry headers and deterministic VLESS + REALITY share URIs backed by golden tests.
- Terminal entitlement events invalidate tokens synchronously and emit a revoke command; actual node allocation, Xray mutation, reconciliation, and real-Xray e2e remain Stage 6.
- OpenAPI, AsyncAPI/JSON Schema, ADRs, threat model, runbook, Compose, migrations, contract tests, `make verify`, and `make compose-smoke` match implemented behavior.
- Credential-owned outbox sequence barriers, security audit for every successful plaintext provisioning-material read, malformed `/s` path equivalence, and expired-status behavior have PostgreSQL/Redis/Kafka regression coverage.

Risks and dependencies:

- Stage 5 cannot prove a live VPN connection without Stage 6. Tests inject a contract-valid provisioning result and never report access ready before that result.
- The Stage 4 lifecycle event does not contain the selected region. Stage 6 must version the placement command or obtain the immutable region through an approved service API before node allocation; Stage 5 does not guess a region.
- Loss of an issued plaintext URL before Telegram delivery requires explicit rotation. Storing a replayable encrypted URL is rejected because it increases credential exposure.

Non-goals:

- No node registry, capacity selection, desired-state reconciliation, node-agent, Xray configuration changes, or production VPS access.
- No Happ Provider ID, HWID/device enforcement, traffic accounting, or advanced app-management flags.
- No Telegram delivery changes; telegram-bot integration with the synchronous issue API is a later approved change.

Verification completed for the Stage 5 remediation on 2026-07-19:

- `make verify`
- `make compose-smoke`
- final review of lifecycle ordering, PostgreSQL transaction boundaries, delayed provisioning, revoke proof, outbox claims, global idempotency, Redis atomicity, redaction, and public response compatibility

### Stage 6 - Provisioning control plane and node-agent

Depends on Stage 5 and threat review. Adds node registry, allocation, mTLS protocol, idempotent desired revision, Xray validation, atomic reload, rollback, and local real-Xray e2e tests.

Status: accepted by the product owner on 2026-07-19 after acceptance-review remediation and complete verification on `codex/stage6-provisioning-node-agent`.

Implementation plan:

1. Record the Stage 6 threat review, provisioning/node ownership boundaries, desired-state protocol, Xray process-management decision, and official Xray version pin in ADRs and operational documentation.
2. Add an allowlisted mTLS Subscription placement endpoint backed by immutable paid-period snapshots. Extend the audited Access provisioning-material response with `subscription_id` so provisioning can obtain placement without cross-service SQL or a region-bearing Kafka command.
3. Add `provisioning-service` with its own PostgreSQL database, migrations, node registry, health snapshots, allocation and operation state machines, ordered command cursor, durable inbox/outbox, bounded retries, sanitized DLQ notices, and an explicit replay command.
4. Apply the accepted one-primary/one-failover policy with distinct healthy nodes in the selected region and a hard 20% capacity reserve. Primary success is required; exhausted failover attempts produce a visible degraded result.
5. Add an mTLS-only node-agent desired-state API. Persist a local operation journal and last-known-good desired state, reject operation collisions and stale revisions, validate candidate Xray JSON, atomically replace config, reload through a fixed process manager, and rollback on validation or reload failure.
6. Pin official stable Xray-core `26.3.27` source by commit and archive SHA-256, then rebuild it on pinned Go with explicit fixed security dependencies. Generate local REALITY and mTLS material outside Git, seed two local nodes, and isolate node-agent management traffic from the application backend network.
7. Reconcile pending operations, allocation state, node heartbeat/capacity, and agent actual revisions. Revoke succeeds only after exact removal from all allocation nodes at the allocation revision expected by Access.
8. Add placement/capacity/concurrency, duplicate/gap/stale Kafka command, retry/DLQ/replay, mTLS identity, node-agent idempotency, invalid-config rollback, reload rollback, reconciliation, and local VLESS + REALITY data-plane tests.
9. Update OpenAPI, AsyncAPI/JSON Schema, Compose, runbooks, threat model, risk register, Definition of Done, README, and CI verification together with behavior.
10. Remediate the acceptance review with a full assignment proof distinct from usable endpoints, one cross-topic provisioning-outcome sequence, higher-revision allocation generations, durable reconciliation claims, cancellation-safe Xray consistency, and a control-plane VPN E2E.

Acceptance criteria:

- `provisioning-service` owns a separate database and never reads Access, Subscription, or node state through another service's database.
- Provision and revoke command sequences are consumed in credential order across their two Kafka topics; gaps remain unacknowledged, duplicates are no-ops, and stale collisions are durable conflicts.
- Placement uses immutable Subscription data obtained over an allowlisted mTLS API, assigns distinct primary and failover nodes in the selected region, and cannot exceed 80% of configured node capacity under concurrent allocation.
- Kafka and logs contain no VLESS UUID, subscription token, REALITY private key, node-agent request body, or raw poison payload. Every Access material read remains durably audited.
- Node-agent authorizes only the provisioning SPIFFE identity, binds the server certificate to the registered node identity, and applies `(operation_id, credential_id, desired_revision)` idempotently.
- An invalid candidate never replaces last-known-good. A failed Xray reload restores the previous config and process before returning a bounded redacted error.
- Provisioning reports `active` only after primary and failover success, `degraded` only after primary success and exhausted failover work, and terminal failure when primary cannot be applied within the bounded retry policy.
- Revoke results contain the exact unique assigned-node set and allocation revision. Partial physical removal never becomes success.
- Provisioning outcomes share a producer-owned credential sequence across all four result topics. Access durably defers gaps, treats exact replay as a no-op, and rejects sequence collisions.
- Higher desired revisions atomically rebind allocations and fence stale workers. Reactivation after revoke or terminal provision failure preserves capacity exactly once and can converge without changing credential identity.
- Reconciliation claims due allocations with durable leases and bounded batches so multiple replicas do not duplicate live work and rows beyond the first batch cannot starve.
- Once Xray mutation starts, request cancellation cannot return until the candidate or last-known-good process is healthy within the configured consistency bound.
- Sanitized DLQ notices, an operator replay command, reconciliation, node heartbeat loss, low capacity, primary failure, and degraded failover have tests and runbook procedures.
- The `vpn` Compose profile proves real Xray config validation, live VLESS + REALITY traffic through a provisioned credential, idempotent replay, expiry/revoke removal, and continued last-known-good service after a rejected candidate.
- `make verify`, migration-from-zero, race tests, contract tests, image scans, normal Compose smoke, and VPN Compose smoke pass before Stage 6 is presented for acceptance.

Risks and dependencies:

- The official stable Xray release can lag newer prereleases. The Stage 6 source pin follows the release marked stable by XTLS, while the node artifact is rebuilt with patched Go dependencies to satisfy the vulnerability gate; both upstream changes and the security override must be reviewed before production rollout.
- REALITY private keys and VLESS client UUIDs necessarily exist on a VPN node. Local files are generated under ignored secret directories with restrictive permissions; production disk, user, and secret hardening remain Stage 8 work.
- The current MVP has one catalog region. A future purchase that changes region during an existing entitlement needs an explicit product migration policy before multiple production regions are offered.
- Local Compose may co-locate node-agent process supervision and Xray for deterministic reload testing. Production deployment must preserve separate non-root identities through the Stage 8 systemd/Ansible design.

Non-goals:

- No production VPS onboarding, WireGuard deployment, public node-agent listener, production certificates, or real user traffic.
- No notification delivery, admin mutation API/CLI, RBAC workflow, or operational UI; those remain Stage 7.
- No per-destination, DNS, packet, or browsing telemetry and no Happ HWID/device accounting.

Verification completed for the Stage 6 acceptance-review remediation on 2026-07-19:

- `make verify` on the clean Stage 6 remediation commit, including format/tidy/vet, unit, race, lint, official-database govulncheck fallback, secret scan, npm audit, contract checks, all service image builds, HIGH/CRITICAL image scans, and Compose config validation
- Access and Provisioning PostgreSQL regression suites for degraded assignment proof, terminal-failure recovery, revoke/reactivation fencing, cross-topic outcome order, exact replay/collision, capacity accounting, and reconciliation beyond one batch
- `make compose-smoke` for the complete Telegram, YooKassa sandbox, entitlement, Access, Happ, Kafka, PostgreSQL, Redis, idempotency, redaction, and mTLS flow
- `make vpn-smoke` for the complete Access command, Kafka, Provisioning, audited material/placement reads, two mTLS node-agents, real Xray validation, Happ delivery, VLESS + REALITY traffic, refund-driven revoke, and post-revoke traffic rejection
- cancellation-during-reload Xray tests plus final review of service ownership, transaction boundaries, generation fencing, cross-topic ordering, capacity, reconciliation leases, SPIFFE binding, rollback, secret persistence, cleanup, logging, contracts, and documentation

### Stage 7 - Notifications and admin operations

Depends on lifecycle events. Adds durable Telegram notifications, admin CLI/internal API, RBAC, and audit.

Status: accepted by the product owner on 2026-07-23 after acceptance-review remediation on `codex/stage7-notifications-admin`.

Implementation plan:

1. Record notification policy, cross-topic ordering, Telegram delivery semantics, administrator identity, RBAC, action ownership, and append-only audit in ADRs.
2. Add versioned Subscription grace and Access user-facing outcome facts without credentials or bearer material.
3. Add notification-service with its own PostgreSQL database, migrations, normalized inbox, aggregate cursors, business dedupe, typed templates, durable leases/retries, sanitized DLQ, and support-safe status/retry API.
4. Keep the Telegram token in telegram-bot and add one allowlisted typed mTLS delivery endpoint with a stable delivery ID and ephemeral Redis collision guard.
5. Resolve active Telegram target and current terms consent just in time through an allowlisted Identity API; do not persist or log chat IDs or rendered message text.
6. Add admin-service with its own PostgreSQL database, validated principal bootstrap, default-deny RBAC, idempotent action requests, fixed owner clients, and append-only audit protected from the runtime DB role.
7. Add Go admin CLI using mTLS, bounded timeouts, request IDs, mandatory idempotency keys/reasons for mutations, support-safe human/JSON output, and nonzero error exits.
8. Expose only typed notification retry, subscription revoke, and higher-revision Access recovery mutations. Keep all secrets and unapproved financial/node/role operations inaccessible.
9. Update OpenAPI, AsyncAPI/JSON Schema/examples, Compose, Dockerfiles, CI, threat model, runbooks, README, and Definition of Done together with implementation.
10. Prove duplicate/gap/collision handling, Telegram retry/permanent outcomes, multiple workers, RBAC, owner idempotency, audit immutability, mTLS denial, secret absence, restart recovery, and the complete Stage 7 Compose flow without regressing Stage 6 VPN smoke.
11. Remediate commit `8d651fc` acceptance findings with per-stream notification delivery barriers/current-state suppression and recoverable fenced admin owner outcomes. Required regressions are extension `429` followed by revoke and owner commit followed by a lost response plus same-key replay.

Acceptance criteria were the Stage 7 Definition of Done and the product-owner request attached on 2026-07-19.

Verification completed on 2026-07-23:

- Go formatting, tidy, vet, unit/PostgreSQL tests, race tests, and golangci-lint pass; the full test and lint suites were also executed in the pinned Go 1.26.5 Linux image because Windows Application Control can block generated test binaries.
- OpenAPI, AsyncAPI, and all 19 event schema examples pass contract validation; PowerShell and Bash Stage 7 scripts pass parser checks.
- `npm audit`, `govulncheck`, and Gitleaks pass. Fresh advisories were remediated with `golang.org/x/text v0.39.0`, `fast-uri v3.1.4`, and an ADR-recorded Xray security rebuild using gRPC-Go `v1.82.1`.
- All 11 service/CLI images build, and Trivy reports zero HIGH/CRITICAL findings across all 24 OS and Go-binary targets.
- Compose configuration and the complete `make stage7-smoke` flow pass after final security review, including notification retry/permanent outcomes, RBAC denial, action replay/collision, audit, restart recovery, real VLESS + REALITY traffic, revoke, and post-revoke stale-delivery suppression.
- Final review found and fixed bounded-body enforcement for the admin facade and all Stage 7 owner delivery/mutation endpoints. No cross-service internal imports or SQL access, sensitive log/audit/DLQ fields, floating-point money, or unapproved admin operations remain in the reviewed diff.
- Acceptance remediation adds an atomic FIFO delivery barrier with terminal preemption and exact Subscription state validation. PostgreSQL regression coverage proves `extension -> Telegram 429 -> revoked -> extension retry` leaves the extension suppressed, while the refund barrier suppresses a delayed payment confirmation.
- Administrator attempts now use a claim lease and distinguish definitive owner rejection from recoverable `outcome_unknown`. The required dropped-response integration test proves same-action replay succeeds with the original action/correlation/owner key and exactly one owner mutation.
- `make compose-smoke`, `make vpn-smoke`, and the complete `make stage7-smoke` pass on the remediation diff. The full `make verify` substantive suite passes; its clean-tree `diff-check` is rerun on the remediation commit before push.

### Stage 8 - Observability, hardening, and deployment

Depends on working services. Adds dashboards, alerts, runbooks, backup/restore drill, node hardening, secret rotation, privacy retention jobs, SBOM, and signing.

Status: in progress on `codex/stage8-observability-hardening`. Slices 1-10 are implemented for review. The slice 4-6 acceptance findings are remediated with a durable successful-payment denominator, PostgreSQL-clocked partial retention reports, exported-snapshot backup inspection, exact empty-target restore preflight, and forced-failure secret cleanup. Slices 7-10 add disposable Debian node hardening, bounded secret rotation, load/failure recovery, and release SBOM/keyless provenance controls. Stage 8 is not accepted and Stage 9 remains blocked pending the complete verification and product-owner review.

Implementation plan:

1. Establish the privacy-safe telemetry contract, protected scrape identity, HTTP RED metrics, pinned Prometheus/Grafana profile, initial dashboard/alerts, configuration validation, and Compose smoke.
2. Add bounded Kafka producer/consumer, lag/retry/DLQ, outbox/inbox, PostgreSQL pool/query-class, billing, subscription, access, provisioning, node-capacity, and Xray reload metrics with owner-specific tests.
3. Add W3C trace-context propagation for HTTP and Kafka plus OpenTelemetry Collector and Tempo. Add structured log collection through the collector/Loki without payloads, bearer paths, VPN material, or user-traffic metadata.
4. Define SLI recording rules and multi-window burn-rate alerts for control API, subscription endpoint, and payment-to-provisioning objectives. Add Alertmanager routing templates without real receiver credentials.
5. Implement configurable privacy retention jobs per owning service, legal holds for financial records, dry-run/reporting, deletion bounds, and PostgreSQL tests.
6. Add database-per-service backup artifacts and perform a clean restore drill with integrity, ownership, and recovery-time evidence.
7. Add Ansible roles for supported VPN-node OS hardening, firewall, WireGuard management plane, separate non-root node-agent/Xray systemd units, certificate deployment/rotation, and rollback. Test only against local disposable hosts until production approval.
8. Exercise rotation for mTLS, Telegram/YooKassa credentials, Access encryption/HMAC keys, and REALITY keys with overlap/rollback rules and no secret output.
9. Add load, soak, failure-injection, Kafka/DB outage, node-loss, reload-failure, and recovery scenarios with explicit pass/fail budgets.
10. Produce release images and SBOMs, scan them, sign immutable artifacts through keyless CI where approved, verify provenance, and document rollback. Production deployment remains a separate approval gate.

First-slice acceptance:

- Every HTTP process exports request count, duration histogram, and in-flight gauge with bounded labels only.
- A regression test proves a raw `/s/{token}` value cannot enter metric labels.
- `/metrics` accepts only the environment-bound `observability` mTLS identity; Telegram metrics are absent from the public listener.
- Integrity-pinned Prometheus and Grafana security rebuilds run with reduced container privileges, loopback-only host ports, 15-day local metric retention, immutable provisioning, no HIGH/CRITICAL scan finding, and no committed administrator password.
- Prometheus 8/11-target configurations and absence/down rules, Grafana dashboard JSON, Linux credential staging/readability, Compose configuration, unit/race/lint checks, `make verify`, `make observability-smoke`, and the 11-target Stage 7 smoke pass.
- ADR 0027, architecture, threat model, runbook, external sources, README, and Definition of Done agree with behavior and list the remaining Stage 8 work.

Second-slice acceptance:

- All database-backed service runtimes expose pgx pool and bounded query-class metrics without SQL, arguments, identifiers, DSNs, or error text.
- Kafka publishers, consumers, retries, DLQs, and outbox workers expose only explicitly allowlisted topics and bounded outcomes/stages; record age is capped and no partition, offset, key, payload, or header enters a label.
- Owner-local collectors expose explicit durable backlog and domain-state series with one-second query bounds, exact label allowlists, zero values, and fail-closed snapshot health.
- Billing reconciliation, Subscription lifecycle scheduling, Access state, Provisioning operation/capacity/heartbeat, Notification delivery, and node-agent Xray metrics are present and privacy-safe.
- Operational dashboard panels, alerts, representative `promtool` tests, 8/11-target smoke assertions, ADR 0028, architecture, threat model, runbook, risk register, external sources, README, and Definition of Done match the implementation.

Third-slice acceptance:

- All service HTTP servers and reviewed HTTP clients propagate W3C `traceparent`/`tracestate` without baggage and record only method, registered route, and status.
- Every franz-go client injects W3C context; each owner consumer extracts it. Kafka span data excludes topic, key, payload, partition, offset, envelope IDs, and headers.
- OTLP logs pass an exact message/field allowlist before export. Collector transforms repeat the privacy allowlist and remove unreviewed resource, span, scope, and log attributes.
- OTLP ingress requires TLS 1.3 mTLS with per-service staged client credentials and a separate Collector server identity. Tempo and Loki remain private to Compose; only loopback Grafana can query them.
- Local trace retention is 30 days and log retention is 14 days. No production storage, tenancy, authentication, backup, or retention policy is implied.
- Tempo and Loki are rebuilt from integrity-pinned release sources with reviewed security dependency updates, verified licenses/modules, and zero unsuppressed HIGH/CRITICAL findings. Tempo's exact Prometheus LTS PURL has one visible OpenVEX correction backed by a build assertion of the patched redacting Azure AD client-secret type because Tempo exposes effective configuration.
- `make observability-validate` and `make observability-smoke` prove native configuration, real-UID key readability, a known W3C trace, a trace-correlated log, and absence of a synthetic query-string secret in Tempo/Loki.
- ADR 0029, architecture, threat model, runbook, risk register, external sources, README, and Definition of Done match the implementation.

Fourth-slice acceptance:

- Control API and public subscription availability have explicit eligible/bad-event recording rules for 99.9% and 99.95% monthly objectives.
- Every successful payment enters an owner-local durable denominator keyed for idempotency; only aggregate identifier-free measurements are exported. Committed initial provisioning or extension fulfillment is recorded transactionally, while replay and rollback do not double-count.
- Fast 1h/5m and slow 6h/30m budget-burn alerts cover both availability objectives and the 99% under-60-second asynchronous objective. p95 warnings cover 300 ms control API and 200 ms subscription rendering targets.
- `promtool` tests cover healthy and budget-burning states. Alertmanager configuration and templates pass `amtool` with inert receivers and no credentials or production delivery claim.
- ADR 0030 and the observability runbook define SLI semantics, operator response, limitations, and the production escalation blocker.

Fifth-slice acceptance:

- Billing, Access, Provisioning, Notification, and Admin each own a maintenance binary that connects only to the owner database. The shared runner contains technical bounds only.
- Every command defaults to dry-run, limits batches to 1,000 and per-dataset deletion to 100,000, and emits aggregate identifier-free reports.
- Billing legal holds protect eligible processed webhook rows; payment/order/refund/idempotency/outbox data is never auto-deleted.
- Fresh rows, unresolved dead letters, owner correctness barriers, and Admin runtime audit immutability have PostgreSQL regression coverage. Admin audit deletion requires the migrator identity.
- ADR 0031, privacy policy, threat model, risk register, and retention runbook agree on eligible datasets and production legal/custody blockers.

Sixth-slice acceptance:

- PostgreSQL custom-format output streams directly into authenticated age X25519 encryption without a plaintext dump artifact or password-bearing process argument.
- Restore verifies metadata and ciphertext SHA-256, decrypts directly into `pg_restore --single-transaction`, and assigns the exact service owner in an isolated empty database.
- The disposable drill backs up and restores all eight owner databases, compares exact public-table row counts and relation owners, enforces local RPO <= 24 hours and RTO <= 30 minutes, and removes temporary keys, artifacts, and volumes.
- Unit tests cover key mode, connection-secret handling, comparison failure, and safe identifiers. The backup image is license-complete and included in build and HIGH/CRITICAL scan gates.
- ADR 0032 and the backup runbook explicitly leave production scheduling, off-host immutable storage, key custody, HA/PITR, and approved recovery targets unresolved.

Acceptance-review remediation for slices 4-6:

- Every successful Billing payment enters an Access-owned durable denominator. Committed initial provisioning or extension fulfills the row; unfinished rows become bad after 60 seconds by PostgreSQL time. Restart, replay, rollback, and lifecycle-before-payment delivery have regression coverage.
- Prometheus error ratios normalize an absent `5xx` series to zero. Fast/slow burn and both p95 warning paths have `promtool` tests.
- Retention uses the owning PostgreSQL clock and preserves completed aggregate progress plus a bounded dataset/stage when a later dataset fails.
- Backup source inspection and `pg_dump` use the exact same exported repeatable-read snapshot. Restore verifies the URL and actual database, rejects every non-empty target before `pg_restore`, and never uses `--clean`.
- Forced failure after key generation must remove the age identity and drill directory. Cleanup errors fail, and an ignored-filesystem scan detects private-key artifacts.

Seventh-slice acceptance:

- A Debian 12 Ansible role configures nftables default deny, WireGuard-only node management, SSH/sysctl hardening, separate non-root node-agent and Xray identities, restricted credential modes, and hardened systemd units.
- node-agent production mode uses a fixed reload/status helper, atomic current/last-known-good state, and health-confirmed rollback without arbitrary shell input or unrestricted sudo.
- A digest-pinned disposable target converges twice with zero second-pass changes and asserts identities, modes, firewall, WireGuard, sudoers, and unit restrictions. Real VPS enrollment remains blocked.

Eighth-slice acceptance:

- mTLS rotation proves old/new trust overlap, leaf cutover, old retirement, and rollback under TLS 1.3.
- Access encryption and HMAC use independent versioned keyrings with at most four read generations and one explicit active writer. Token lookup survives overlap/rollback without plaintext storage or unbounded work.
- Telegram/YooKassa client safety, Xray last-known-good recovery, and distinct REALITY generations have secret-free tests. Real provider/VPS rotation remains an approved operator procedure.

Ninth-slice acceptance:

- The bounded load probe cannot emit its target URL, exceeds neither duration/rate/sample limits, and produces aggregate p50/p95/p99/error evidence with explicit budgets.
- Kafka outage/replay republishes durable work without a second subscription period; PostgreSQL outage preserves liveness, fails readiness, and recovers.
- Active primary-node loss is proven by real VLESS + REALITY traffic through the separately provisioned failover. Reload failure and Provisioning reconciliation retain last-known-good and generation fencing.

Tenth-slice acceptance:

- One fixed release inventory classifies every custom production runtime/tool Dockerfile and builds each included image from a clean full commit with commit-addressed tags and OCI source/revision/version/date labels.
- Digest-pinned Syft creates nonempty SPDX 2.3 SBOMs. Digest-pinned Trivy applies the zero HIGH/CRITICAL gate, with only the existing exact Tempo VEX exception, and its JSON result is retained.
- `releasectl` proves the custom Dockerfile inventory is complete, validates the SPDX container subject and Trivy artifact against each immutable image ID, binds report digests, manifest, exact artifact set, and SHA-256 checksums, and rejects tampering.
- A manual `release-signing` workflow uses commit-pinned Actions and least-privilege GitHub OIDC attestation, verifies provenance before upload, and has no registry or deployment permission. Production publication/deployment remains Stage 9.

### Stage 9 - Production readiness review

Depends on previous milestones. Performs architecture, security, legal/payment, license, incident, backup, and rollback reviews before any real launch.
