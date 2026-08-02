# Data Retention

This runbook covers the Stage 8 owner-local sandbox retention commands. It is not a legal retention schedule and does not authorize production deletion.

## Safety Rules

- Run preview first. `RETENTION_DRY_RUN` defaults to `true`.
- Never delete payment/order/refund records, unresolved dead letters, inbox/outbox barriers, idempotency records, entitlement ledgers, credentials, or lifecycle cursors through ad hoc SQL.
- Reports contain aggregate counts only. Do not add row IDs, payloads, hold reasons, user data, or secrets.
- Keep batch size at or below 1,000 and the invocation bound at or below 100,000. Use smaller values during incident investigation.
- Stop if the preview differs materially from the approved retention matrix or if a legal hold is expected but not counted as protected.

## Eligible Datasets

| Owner command | Dataset | Default minimum age | Additional gate |
|---|---|---:|---|
| `billing-retention` | Processed normalized webhook inbox | 30 days | No active legal hold for the scope |
| `access-retention` | Security audit events | 365 days | None |
| `provisioning-retention` | Node health snapshots | 30 days | None |
| `provisioning-retention` | Sanitized consumer dead letters | 30 days | Must already be replayed |
| `notification-retention` | Sanitized notification dead letters | 30 days | Must already be replayed |
| `admin-retention` | Administrator audit | 365 days | Separate migrator identity and retention guard |

Identity, Catalog, and Subscription have no automatic deletion command in this slice.

## Preview

Start PostgreSQL and complete migrations, then run one owner command:

```powershell
$env:RETENTION_DRY_RUN = "true"
$env:RETENTION_BATCH_SIZE = "500"
$env:RETENTION_MAX_DELETE = "5000"
docker compose --profile maintenance run --rm billing-retention
docker compose --profile maintenance run --rm access-retention
docker compose --profile maintenance run --rm provisioning-retention
docker compose --profile maintenance run --rm notification-retention
docker compose --profile maintenance run --rm admin-retention
```

Review `eligible`, `protected`, `deleted`, and `remaining_estimate`. A preview must report `deleted: 0`.

The cutoff clock comes from the owning PostgreSQL database. A later dataset failure returns a `failed` report while retaining completed dataset totals and a bounded `failure.dataset`/`failure.stage`; SQL, row identifiers, and database error text remain excluded.

## Execute

After owner approval, run only the reviewed owner:

```powershell
$env:RETENTION_DRY_RUN = "false"
docker compose --profile maintenance run --rm provisioning-retention
```

Run a new dry-run immediately afterward. Repeated invocations are allowed when backlog exceeds the bound, but each invocation needs its own report and review.

## Billing Legal Holds

The `retention_legal_holds` table is owner-local and intentionally has no public API. A production hold must be created and released by an approved legal/operations principal, not by `billing-service`. The hold scope must be minimal, its reason must not contain customer secrets, and release needs a second-person review. Local Compose can exercise the schema in PostgreSQL tests but does not model production custody.

An active hold increases `protected` and excludes the row from deletion. Never remove a hold merely to make a retention run complete.

## Failure Recovery

1. Keep dry-run enabled while investigating invalid configuration or an unexpected count.
2. A failed batch transaction can be retried; each query selects only currently eligible rows and remains bounded.
3. If deletion occurred outside the approved matrix, stop all retention jobs, preserve the aggregate report, and open an incident. Do not attempt to reconstruct records from application logs.
4. Use the encrypted backup/restore runbook for recovery evidence. Restore into an isolated database before considering owner-approved data repair.

## Verification

`make verify` covers runner bounds and static checks. `make compose-smoke` supplies owner database DSNs and runs the PostgreSQL regression suites; `make compose-config` validates the maintenance profile. Retention commands are never part of normal service startup and have no production scheduler in this stage.
