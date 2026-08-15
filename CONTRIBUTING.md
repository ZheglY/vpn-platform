# Contributing

- Read `docs/VPN_PLATFORM_SPEC.md`, `PLANS.md`, and relevant ADRs before architectural, payment, security, privacy, or public-contract work.
- Work on one approved milestone at a time. Starting another milestone requires maintainer approval.
- Preserve service ownership: no cross-service database access, no shared domain models, and no imports of another service's `internal` implementation.
- Service binaries live under `services/<service>/cmd/<binary>/`; service code lives under `services/<service>/internal/`.
- Shared root `internal/platform` may contain only technical primitives. It must not contain business rules or domain entities.
- HTTP uses standard `net/http`; SQL uses `pgx/v5`; logs use `zap`; async integration uses Kafka with outbox/inbox.
- Kafka events and async commands must have schema versions, owners, partition keys, retry policy, DLQ policy, and contract tests.
- Redis is only for ephemeral state, cache, rate limiting, and short locks. It is never the source of truth.
- Never log or commit secrets, subscription URLs/tokens, VPN credentials, YooKassa credentials, Telegram payloads, webhook bodies, VLESS UUIDs, REALITY private keys, destination IPs, DNS history, or packet contents.
- Every externally retried operation must be idempotent and tested for duplicate, replayed, delayed, and out-of-order delivery.
- Update OpenAPI/AsyncAPI, JSON Schema, ADRs, runbooks, threat model, and README together with behavior changes.
- Run `make verify` before submitting implementation changes. Documentation-only changes may use focused consistency and security checks; record every skipped gate.
- Do not change public contracts, event semantics, payment rules, security boundaries, or production infrastructure without maintainer approval.
- Prefer small reviewable diffs. Review final changes for security, concurrency, SQL transaction boundaries, idempotency, redaction, and compatibility.
