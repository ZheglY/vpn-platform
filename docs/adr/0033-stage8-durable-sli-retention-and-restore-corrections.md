# ADR 0033: Stage 8 durable SLI, retention, and restore corrections

- Status: Accepted for Stage 8 implementation
- Date: 2026-07-26
- Owners: platform, access, database owners, and operations
- Security impact: High
- Contract impact: operational metrics and maintenance tooling only
- Supersedes in part: ADR 0030 decisions 3-6, ADR 0031 reporting clock behavior, and ADR 0032 decisions 5-7

## Context

The first implementation of slices 4-6 had three correctness gaps. The payment objective used process memory and had no durable denominator for successful payments that never reached provisioning. Retention cutoffs used host time and could lose a partial report after a later dataset failed. Restore invoked `pg_restore --clean` and source inspection did not share the exact PostgreSQL snapshot used by `pg_dump`.

## Decision

1. Access consumes `billing.payment.succeeded.v1` into an owner-local durable SLI projection keyed by the payment identifier for idempotency and event correlation. The payment event creates the denominator. A committed initial provisioning command or committed extension records fulfillment in the same owner transaction as the corresponding Access mutation. Only aggregate, identifier-free measurements leave the Access database through metrics and drill reports.
2. Exact event replay cannot increment the projection twice. Out-of-order lifecycle-before-payment delivery is reconciled by payment ID. An unfinished successful payment becomes bad after 60 seconds according to PostgreSQL `clock_timestamp()`. Process restart, scrape gaps, and Kafka replay cannot erase or duplicate the observation.
3. Prometheus exports cumulative started and bad values from this projection. The bad value includes terminally late fulfillment and still-pending rows older than 60 seconds. SLI rules normalize an absent `5xx` series to zero, and rule tests cover fast burn, slow burn, both p95 warnings, target absence, and target failure.
4. Every retention owner obtains its invocation clock from PostgreSQL. A failure report preserves aggregate progress from completed datasets and includes only a bounded dataset and stage code for the failed operation. It never emits SQL, row identifiers, payloads, or database errors.
5. `backupctl backup` opens a read-only repeatable-read transaction, exports its MVCC snapshot, inspects table counts and ownership inside that transaction, and passes the same snapshot to `pg_dump --snapshot`. The transaction remains open until the dump finishes.
6. Restore requires the URL database name and `current_database()` to match metadata. It counts non-system relations, routines, and user-defined types before starting `pg_restore`; any object rejects the target. Restore never uses `--clean` or `--if-exists`.
7. A forced failure immediately after key generation must leave no private key or drill directory. Cleanup errors fail the drill. A repository filesystem scan covers ignored temporary paths and rejects age identities, private PEM blocks, and known VPN secret file patterns.

## Consequences

- The asynchronous objective has a durable denominator and includes never-fulfilled payments instead of sampling only successes.
- Retention evidence remains useful after partial failure and uses the database as the authority for age.
- Backup inspection and dump describe one exact source snapshot, while restore cannot overwrite an accidentally populated target.
- The SLI projection is operational evidence, not a cross-service business model. Access still learns payment facts only through Kafka and never reads Billing storage.

## Rejected alternatives

- Keep an in-memory histogram and infer missing payments from Kafka lag: rejected because restart and independent failure modes bias the objective.
- Compare inspections immediately before and after `pg_dump`: rejected because stable row counts do not prove the dump used either inspection snapshot.
- Permit `pg_restore --clean` after a warning: rejected because a wrong target can be irreversibly modified before an operator notices.
