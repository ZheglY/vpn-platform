# ADR 0031: Stage 8 owner retention and legal holds

- Status: Accepted for Stage 8 implementation
- Date: 2026-07-25
- Owners: platform, billing, access, provisioning, notification, and admin
- Security impact: High
- Contract impact: maintenance commands and owner-local schema only
- Extends: ADR 0003, ADR 0013, and ADR 0026
- Superseded in part by: ADR 0033

> Historical note: ADR 0033 adds PostgreSQL-authoritative invocation time and partial aggregate reports on later dataset failure.

## Context

The sandbox privacy policy requires bounded retention for audit, diagnostics, node health, and sanitized dead-letter data. Blind table cleanup could remove financial evidence, idempotency barriers, unresolved work, or data under a legal hold. A central cleanup service would also need credentials for every database and violate database-per-service ownership.

## Decision

1. Retention is implemented as one maintenance binary per owning service. It connects only to that service database and reuses a shared technical runner containing bounds, dry-run handling, and identifier-free JSON reporting. The shared package contains no domain entities or SQL.
2. Every command defaults to dry-run. Batch size is limited to 1-1000 rows and total deletion to at most 100,000 rows per dataset per invocation. The local defaults are 500 and 5,000.
3. Billing may delete only processed normalized webhook inbox rows older than the configured cutoff. Payment, refund, order, reconciliation, idempotency, and outbox records are never auto-deleted. An active owner-local legal hold protects the matching webhook scope.
4. Access may delete security audit events only after 365 days. Credential, token, operation, lifecycle cursor, inbox, outbox, and idempotency state are retained.
5. Provisioning may delete node health snapshots after 30 days and sanitized consumer dead-letter metadata after 30 days only when the dead letter is already marked replayed. Notification applies the same replay-first rule to its notification dead letters.
6. Admin audit may be deleted after 365 days only by the separate `admin_migrator` maintenance identity with a transaction-local retention guard. Runtime `admin_app` remains unable to update, delete, or truncate audit history.
7. Identity, Catalog, and Subscription have no automatically eligible dataset in this slice. Consent, immutable catalog versions, entitlement ledgers, source-payment links, scheduler barriers, inbox, and outbox state remain durable.
8. Fresh rows, unresolved dead letters, outbox/inbox correctness barriers, payment records, and held rows are protected in PostgreSQL integration tests. Dry-run and deletion bounds are unit tested.
9. A production scheduler, production data-retention periods, legal authority, hold-management identity, and approval workflow are not inherited from local Compose. Local service database roles are a development topology; production role grants must separate runtime, migration, retention, and legal-hold custody.

## Consequences

- Cleanup cannot become a cross-service privileged data plane.
- Operators get a safe preview and bounded report without row IDs, payloads, reasons, or secrets.
- Replay barriers and unresolved operational evidence survive retention.
- Payment records remain indefinitely until jurisdiction and accounting requirements are approved.
- Retention reduces selected sandbox data but does not implement user export, erasure, or anonymization workflows.

## Rejected alternatives

- One platform cleanup service with credentials to all databases: rejected because it violates ownership and increases blast radius.
- Delete all inbox/outbox rows after a time interval: rejected because they are durable idempotency and delivery evidence.
- Delete unresolved dead letters on age alone: rejected because age does not prove the business event was recovered.
- Give the admin runtime permission to purge its own audit: rejected because compromised runtime credentials could erase evidence.
- Automatically delete financial records after a guessed period: rejected until legal requirements are known.
