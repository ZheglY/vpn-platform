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
