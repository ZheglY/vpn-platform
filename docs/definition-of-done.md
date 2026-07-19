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
- Provisioning result events are transactional, schema-versioned, secret-free, and match Access's revision and exact revoke-proof contracts.
- Node-agent authenticates provisioning-service, while provisioning pins the exact registered node SPIFFE identity. Health identities cannot mutate desired state.
- Desired operations are idempotent by operation ID and request hash; stale revisions conflict and absent tombstones prevent delayed restoration.
- Xray-core source is pinned by version, commit, and archive SHA-256, then rebuilt with pinned fixed security dependencies. Candidate validation, atomic install, process restart, last-known-good rollback, secret-file permissions, image scanning, and the real protocol path have tests.
- Reconciliation compares control-plane desired state to node actual state, records repaired allocation/capacity state, refuses newer revisions, and never rewrites a published terminal result.
- DLQ notices retain no raw record; replay is topic-allowlisted and verifies the stored payload hash before republish.
- Local `vpn` Compose proves two node-agents, real Xray validation, VLESS + REALITY traffic, idempotent replay, revoke removal, and rollback behavior.
- Migration from zero, PostgreSQL concurrency, contracts, race tests, normal Compose smoke, VPN smoke, service images, and image scans pass before acceptance.
