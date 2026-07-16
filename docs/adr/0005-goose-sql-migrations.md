# ADR 0005: Goose SQL Migrations

Status: Accepted

Date: 2026-07-12

## Context

The project requires explicit SQL with `pgx/v5` and no ORM. Database changes must be reproducible, reviewable, and compatible with service ownership.

## Decision

Use `goose` with SQL migrations and a separate migration job per service.

Migrations are forward-only for production changes. Destructive changes must follow expand, migrate, contract.

## Consequences

- Stage 1 must pin the `goose` version and define migration command conventions.
- Each service owns `services/<service>/migrations`.
- Migration-from-zero is a required quality gate.
- Down migrations may exist for local convenience, but production rollback uses forward fixes unless a later ADR says otherwise.
