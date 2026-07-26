# Definition of Done

This Definition of Done applies to every implementation task after Stage 0. Stage 0 is complete when its documentation acceptance criteria are met.

## Required

- Scope and service boundaries match the currently approved milestone.
- Behavior, business invariants, and failure modes are covered by tests appropriate to the change.
- Idempotency is designed and tested for webhooks, Telegram updates, Kafka consumers, payment operations, provisioning, and retries.
- Public HTTP contracts, internal HTTP contracts, Kafka schemas, runbooks, ADRs, and README are updated with behavior changes.
- Database changes are forward-only migrations with a rollout strategy.
- Logs, errors, metrics, traces, and audit records do not reveal secrets, bearer URLs, VPN credentials, payment credentials, full provider payloads, full Telegram payloads, destination IPs, DNS history, or packet contents.
- Unit tests and relevant integration, contract, e2e, security, and race tests pass.
- Formatter, `go vet`, linter, vulnerability scan, secret scan, migration-from-zero, OpenAPI/AsyncAPI lint, and Docker image checks pass when applicable.
- Docker images run as non-root and contain no secrets.
- Failure handling has bounded retries, backoff, cancellation, and operational visibility.
- The final diff is reviewed for cross-service coupling, dead code, accidental files, SQL transaction issues, concurrency issues, compatibility, and redaction.
- No TODO hides a security, correctness, payment, or provisioning blocker.
- Any skipped check is documented with the reason and follow-up.

## Stage 0 Specific

- No business code, migrations, Compose, Dockerfiles, or service implementations are created.
- All decisions accepted by the product owner are captured.
- All unresolved decisions affecting money, law, privacy, public API, event semantics, or VPN provisioning are explicit.
- ADRs exist for every architecture/security/public-contract decision made in Stage 0.
- Contract skeletons are present but do not imply implemented behavior.

## Stage 3 Specific

- YooKassa is sandbox-only and every parsed provider object must have `test=true` and the configured account ID.
- Money is stored as integer minor units and an uppercase ISO-style three-letter currency; no `float` crosses a contract or transaction.
- A provider idempotency key is persisted before the first create call and reused for all ambiguous retries.
- Duplicate clicks, idempotency replay with a changed request, duplicate webhook, delayed webhook, and out-of-order terminal notification are tested.
- PostgreSQL and service tests cover concurrent payment commands with different keys, one-payment-per-order constraints, state triggers, terminal/outbox rollback, worker lease recovery, and the 23-hour internal/24-hour provider-window boundaries.
- Exact same-key replay returns the stored resource before current Identity/Catalog availability checks.
- Webhook fulfillment requires provider GET verification of ID, account, amount, currency, internal metadata, status, and capture time.
- Terminal payment/order transition and versioned outbox insert are one PostgreSQL transaction.
- Full provider/webhook payloads, confirmation URLs, credentials, and payment secrets are absent from logs and durable inbox data.
- Confirmation URLs are cleared on succeeded, canceled, and failed payment transitions.
- Migration from zero, fake-provider E2E, contract examples, service images, and image scans pass before acceptance.

## Stage 4 Specific

- Subscription owns a separate database; no code or SQL reads Billing/Catalog/Identity databases directly.
- The implemented payment v1 event remains compatible and is validated against Billing's immutable order snapshot over mTLS.
- Payment inbox, immutable period, final entitlement transition, and activation/extension/direct-expiry outbox event commit atomically.
- Duplicate event/payment IDs and concurrent different payments cannot duplicate, overlap, or shorten purchased entitlement.
- Active, grace, expired, and refund transitions use authoritative PostgreSQL UTC boundaries and recoverable scheduler leases.
- Refund-before-payment, permanent refund conflicts, refund gaps, and current/future/historical full-period cases are idempotent and tested.
- Concurrent outbox workers cannot publish a later aggregate sequence before an earlier unpublished sequence.
- Kafka poison handling retains no raw payload; transient failures do not advance the committed partition offset.
- Subscription HTTP and event contracts, migration-from-zero, PostgreSQL integration suite, Compose E2E, service image, and image scan pass before acceptance.

## Stage 5 Specific

- Access owns a separate database and never reads Subscription, Provisioning, Identity, or Billing databases.
- Credential creation, operation creation, inbox completion, and secret-free provisioning outbox insertion are one PostgreSQL transaction.
- VLESS UUIDs are versioned AES-256-GCM ciphertext at rest; tokens are 256 random bits with only a separate-key HMAC-SHA-256 persisted.
- Provisioning success is current-revision checked, validates exactly one primary and at most one failover, stores endpoint snapshots atomically, and emits one `access.ready.v1`.
- Lifecycle facts carry a monotonic subscription sequence; Access persists its last applied sequence, defers gaps without committing them, and rejects stale sequence collisions.
- Database time prevents elapsed activation from provisioning and prevents a delayed provisioning success from emitting readiness; a delayed physical success atomically starts revoke.
- Revoke success is bound to desired/allocation revisions and exactly matches the unique assigned-node snapshot; zero-allocation no-op and partial-removal cases are tested.
- Access outbox rows and envelopes carry a credential-owned sequence, and no later sequence is claimable while an earlier one is unpublished.
- Issue and rotation serialize per subscription; duplicate/concurrent effects cannot create multiple active tokens, and completed idempotency replay never reconstructs a plaintext URL.
- Global idempotency-key reuse across subscriptions resolves deterministically under concurrency without a unique-violation 500.
- Malformed, unknown, expired, and revoked tokens have the same no-store 404 fingerprint; logs contain only the route template.
- Happ headers and VLESS + REALITY URI encoding have golden and security tests against the current official documentation.
- Provisioning material is mTLS allowlisted, every successful plaintext read is durably audited without secret material, and Kafka contains no VLESS UUID, token, or URL.
- The Redis IP/token decision is one atomic script; a blocked dimension cannot consume the other dimension's budget.
- Migration-from-zero, Access PostgreSQL integration, Compose delivery smoke, contracts, service image, and image scan pass before acceptance.
- No Stage 6 node registry, placement, node-agent, Xray mutation, or live connection is present or claimed.

## Stage 6 Specific

- Provisioning owns a separate database and obtains credential material and immutable placement only through allowlisted mTLS APIs.
- Provision and revoke commands share a credential sequence cursor; duplicate, collision, stale, cross-topic gap, poison, retry, and replay behavior is tested.
- Placement atomically chooses one primary and one distinct failover in the paid region and rejects unhealthy nodes or allocation beyond the 80% threshold under concurrency.
- Provisioning result events are transactional, schema-versioned, secret-free, and share one positive credential-owned sequence across all four outcome topics. Access persists a common cursor, defers gaps, and rejects collisions.
- Provision success separates the exact complete assigned-node proof from usable Happ endpoints. Degraded-to-revoke removes the failed failover assignment as well as the usable primary.
- Node-agent authenticates provisioning-service, while provisioning pins the exact registered node SPIFFE identity. Health identities cannot mutate desired state.
- Desired operations are idempotent by operation ID and request hash; stale revisions conflict and absent tombstones prevent delayed restoration.
- Xray-core source is pinned by version, commit, and archive SHA-256, then rebuilt with pinned fixed security dependencies. Candidate validation, atomic install, process restart, last-known-good rollback, secret-file permissions, image scanning, and the real protocol path have tests.
- Higher-revision recovery atomically rebinds and fences the allocation generation, preserves or re-reserves capacity exactly once, and rejects stale worker writes during revoke/reactivation races.
- Reconciliation uses durable due times and lease claims, compares control-plane desired state to node actual state, records repaired allocation/capacity state, refuses newer revisions, and cannot starve rows beyond one batch.
- DLQ notices retain no raw record; replay is topic-allowlisted and verifies the stored payload hash before republish.
- Request cancellation during Xray reload cannot leave the process stopped; candidate startup or last-known-good restoration completes under an independent bounded context.
- Local `vpn` Compose proves the complete Access command, Kafka, Provisioning, both node-agents, real Xray, outcome, Happ profile, VLESS + REALITY traffic, and terminal revoke path. Direct node mutation alone is insufficient.
- Migration from zero, PostgreSQL concurrency, contracts, race tests, normal Compose smoke, VPN smoke, service images, and image scans pass before acceptance.

## Stage 7 Specific

- Notification and Admin each own a separate database, migration path, runtime credential, and Docker image. Admin has no credentials for another service database.
- Notification uses a payload-free durable inbox, producer/aggregate cursor, unique business key, PostgreSQL job lease, bounded retry, database time, and sanitized DLQ metadata.
- Exact source replay is a no-op; changed event/sequence reuse is a durable conflict; gaps remain uncommitted; several workers cannot claim one job concurrently; expired leases recover.
- Telegram target and consent are resolved just in time. Notification storage, logs, metrics, traces, audit, Kafka, and DLQ contain no chat ID, rendered text, Bot token, subscription URL, VLESS UUID, ciphertext, or private key.
- telegram-bot remains the token owner and exposes one allowlisted typed mTLS send endpoint with a stable delivery ID. `429`, network/5xx, blocked/missing target, unauthorized token, invalid request, overflow, and ambiguous timeout semantics are tested and documented.
- Notification jobs store source and delivery stream sequences. Claims are FIFO per stream, terminal subscription/refund facts suppress older retry work, permanently failed predecessors do not block forever, and a stale predecessor cannot be manually resurrected after a successor.
- Extension/grace/terminal and Access-related jobs query current Subscription state; readiness jobs also query Access state. Delivery is suppressed after incompatible owner state even while downstream services are still converging. Initial activation and physical revoke facts follow the accepted business-notification suppression policy.
- Admin derives actor only from one verified admin SPIFFE URI, maps enabled local principals to built-in default-deny roles, and checks an explicit permission on every endpoint. No superadmin/wildcard exists.
- Admin CLI verifies the server certificate, uses bounded timeout/request ID, exposes typed commands only, requires reason and idempotency key for mutations, rejects unsafe output, and exits nonzero on failure.
- Admin mutations are limited to notification retry, subscription revoke, and higher-revision Access recovery. Owning services execute them idempotently; no financial mutation, arbitrary SQL/Kafka/shell, URL rotation, role grant, or direct node/Xray change exists.
- Admin owner attempts use durable claim leases. Timeout/reset/`5xx`/invalid successful responses become recoverable `outcome_unknown`; only definitive rejection becomes `failed`, and replay reuses action/correlation/owner idempotency IDs.
- Accepted, attempted, retrying, unknown, and completion audit rows are append-only, use PostgreSQL time, retain safe identity/permission/request snapshots, and cannot be updated/deleted/truncated by the runtime role.
- OpenAPI, AsyncAPI, JSON Schemas/examples, ADRs, threat model, runbooks, risk register, README, Compose, Makefile, and CI match behavior.
- Migration from zero, unit/PostgreSQL/race/security/contract checks, all images and scans, normal Compose smoke, VPN smoke, and Stage 7 E2E passed before Stage 7 acceptance. Stage 8 began only after that explicit acceptance.

## Stage 8 Specific

- Stage 8 was accepted by the product owner on 2026-07-26 after all ten slices, acceptance-review remediation, and required verification completed; no real VPS, production credential, alert receiver, registry publication, deployment, or user traffic was touched.
- Metrics, traces, logs, dashboards, alerts, and diagnostics comply with ADR 0013. Labels are bounded and contain no raw paths, user/payment/subscription/credential/event/request/correlation IDs, Telegram/provider payload data, VPN material, or browsing metadata.
- Every observability ingress is authenticated and private for its environment. Local loopback-only anonymous Grafana configuration is never reused as a production authentication policy.
- Runtime private keys are staged into least-privilege named volumes with owner-only modes and are read under each actual container UID on native Linux before service startup; no runtime service depends on host bind-mount ownership translation.
- Expected scrape inventory is explicit for every active profile, and tests cover both an existing failed target and a target whose `up` series is absent.
- Dashboard and alert configuration is reproducible from Git, validates before startup, links alerts to runbooks, and has owner, severity, retention, and rollback behavior.
- PostgreSQL, Kafka, durable backlog, owner-domain, scheduler, node-capacity, and Xray metrics use finite reviewed labels, expose snapshot failure, and have unit, `promtool`, and 8/11-target smoke coverage.
- HTTP and Kafka propagate only W3C trace context. Spans exclude raw URLs, query strings, hosts, payloads, message metadata, error text, network peers, baggage, and business identifiers.
- OTLP logs are selected by exact static message and field allowlists before export; Collector transforms repeat the privacy boundary. Tempo/Loki are private, source-integrity-pinned security rebuilds, license-complete, zero-unsuppressed-HIGH/CRITICAL scanned, and queryable only through loopback Grafana in local Compose.
- Tempo's compatible Prometheus LTS dependency uses the redacting `prometheus/common/config.Secret` type for Azure AD remote-write credentials; the build asserts this because Tempo exposes its effective configuration. Its only OpenVEX statement is bound to that exact patched LTS PURL and remains visible in scanner output. Dependency, import, route, compatibility-patch, or VEX changes require renewed security review.
- Local retention is enforced as 30 days for traces and 14 days for logs. The smoke suite proves a known trace and correlated log arrive and a synthetic query-string secret does not.
- SLI recording rules define eligible and bad events for control APIs, the subscription endpoint, and durable payment fulfillment. Successful payments form a crash-safe denominator; pending rows older than 60 seconds remain bad. Fast/slow burn and both p95 alerts have `promtool` tests; Alertmanager validates with inert credential-free receivers and makes no production delivery claim.
- Retention commands are owner-local, PostgreSQL-clocked, dry-run-first, bounded, legal-hold aware, preserve partial reports on failure, and are tested against PostgreSQL. Payment and correctness records, fresh rows, and unresolved dead letters are protected.
- Every owner database has an age-encrypted streaming backup and exact-empty-target restore drill. Source inspection shares the exported pg_dump snapshot; ciphertext integrity, exact public-table counts, relation owners, RPO/RTO, non-empty rejection, and forced-failure cleanup are executable evidence.
- The Debian 12 Ansible baseline enforces WireGuard-only management, a role-owned nftables default-deny table that preserves unrelated host tables, separate non-root node-agent/Xray systemd identities, restricted credentials/sudo, idempotence, real service startup, and last-known-good rollback in disposable Linux tests.
- mTLS, provider-client, Access encryption/HMAC, and REALITY rotations have bounded overlap/retirement/rollback procedures and secret-free executable drills. No real credential is used.
- Bounded HTTPS-only load and failure injection cover exact submission limits, subscription latency/error budgets, Kafka replay without duplicate entitlement, PostgreSQL liveness/readiness recovery, real failover VPN traffic after primary loss, Xray rollback, and Provisioning reconciliation. Every drill uses a unique Compose namespace and proves ordinary local volumes are preserved.
- The complete custom-image release inventory builds from a clean commit, emits SPDX 2.3 SBOMs and retained Trivy reports, passes digest-pinned HIGH/CRITICAL scans, binds every SPDX container subject to its immutable image ID, verifies the exact checksummed artifact set, and has a manual least-privilege keyless provenance workflow. Registry publication and deployment remain Stage 9 gates.
- Stage 8 slices 1-10 and their acceptance-review remediation are accepted. Stage 9 is separately approved as a production-readiness review only and does not authorize launch.
- `make verify`, relevant Compose/VPN/observability smoke suites, security scans, configuration validation, and a final privacy/concurrency/operations review pass before Stage 8 is presented for acceptance.

## Stage 9 Specific

- Stage 9 is a review/evidence milestone and does not authorize production,
  provider credentials, customer data, public DNS, registry publication, VPS
  enrollment, image deployment, or real traffic.
- The system inventory covers every service, database, HTTP/Kafka contract,
  ingress, mTLS/management/observability interface, network, volume, release
  image, workflow, and open production dependency.
- Architecture/code and security/privacy reviews contain evidence, severity,
  impact, owner, due date, status, and regression/gate references. No P0/P1 may
  remain open. Unaccepted P2 production findings remain blocking.
- The 19-image license inventory and machine-readable policy fail closed.
  Unknown/unapproved licenses, incomplete notices/source offers, AGPL/MPL/Redis
  counsel review, and the repository distribution license block publication.
- Legal/payment/privacy/provider decisions are never guessed. Every row has an
  allowed status, owner, dated evidence, next review, and launch-blocking flag.
- Production topology options and trade-offs are documented without selecting
  a provider or adding Kubernetes. Missing DNS/edge, HA/PITR, Kafka ACL, secret
  manager, PKI, backup, alert, registry, capacity, VPS, on-call, and configuration
  ownership decisions remain NO-GO.
- All 12 game-day scenarios record preconditions, fault, blast radius, abort,
  metrics/alerts, business behavior, recovery, rollback, actual result, budget,
  evidence, and residual risk. Unavailable real HA, PKI revocation, alert, or
  rollback evidence is not simulated as success.
- Rollback uses accepted immutable digests, N/N-1 compatibility,
  expand/migrate/contract, forward-only migrations, owner reconciliation,
  last-known-good, safe trust/key overlap, and isolated restore. No automatic
  database downgrade or mutable-tag rebuild is allowed.
- The machine-readable go/no-go gate cannot report GO while any hard gate is
  open. `make production-readiness` validates review consistency but is not a
  deployment authorization.
- Required local verification, smoke, restore, node, rotation, resilience, and
  release-bundle commands are commit-bound in the evidence index with aggregate
  secret-free results. Heavy generated artifacts remain ignored/checksummed.
