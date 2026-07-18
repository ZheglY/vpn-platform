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
