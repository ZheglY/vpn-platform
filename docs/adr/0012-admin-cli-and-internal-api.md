# ADR 0012: Admin CLI and Internal API

Status: Accepted

Date: 2026-07-12

## Context

Operators need safe administrative actions, but a web admin UI adds authentication, session, CSRF, UI, and attack-surface complexity. The first version needs auditable operational control, not a dashboard.

## Decision

Use a protected admin CLI plus internal admin API for v1.

No web-admin in v1.

Admin operations require:

- authenticated admin identity;
- RBAC deny-by-default;
- reason for sensitive actions;
- append-only audit record;
- no credential leakage in output;
- no subscription token plaintext retrieval after initial issuance;
- idempotency for retried operations where applicable.

## Consequences

- Stage 7 decides concrete authentication mechanism before implementation.
- CLI output must be support-safe.
- Admin API uses separate hostname/surface and must not be publicly exposed.
