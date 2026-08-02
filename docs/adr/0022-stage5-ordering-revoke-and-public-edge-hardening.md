# ADR 0022: Stage 5 ordering, revoke proof, and public edge hardening

- Status: Accepted
- Date: 2026-07-19
- Owners: access-service, subscription-service
- Security impact: High
- Contract impact: lifecycle envelopes, Access command/readiness envelopes, revoke result, Access status
- Amends: ADR 0020 and ADR 0021

## Context

Subscription lifecycle facts use four Kafka topics. Kafka preserves order only inside one topic partition, so an expiry or revoke fact can reach Access before an earlier activation from another topic. Stage 4 already assigns a subscription-owned outbox sequence, but the sequence was not present in the Kafka envelope. Access therefore could not reject stale facts or wait for a missing predecessor.

A delayed provisioning success could also arrive after entitlement expiry and incorrectly mark access ready. Revoke success named nodes but did not prove that the list was complete, did not support a zero-allocation no-op, and did not bind the proof to the allocation revision. Access outbox claims were ordered by transaction timestamps rather than an aggregate-owned sequence. The public Redis limiter mutated IP and token counters in separate scripts, and a global URL idempotency key was not locked across subscriptions.

These are pre-production contract corrections required before Stage 5 acceptance. No released external consumer depends on the earlier Stage 5 draft contracts.

## Decision

1. Subscription lifecycle v1 envelopes carry a required positive `aggregate_sequence`. Subscription-service copies the sequence already stored in its outbox into the envelope in the same transaction. Pending rows are backfilled by migration.
2. Access stores a subscription lifecycle cursor with `last_applied_sequence`. Sequence `N` applies only after `N-1`; duplicates are no-ops, stale sequence collisions are durable conflicts, and gaps are retryable. A consumer with a gap pauses only that source topic partition, continues consuming other lifecycle topics, and retries the deferred record after progress. It never commits or dead-letters a gap merely because the predecessor is delayed.
3. Before any credential provisioning is initiated, Access reads `clock_timestamp()` inside the transaction and requires `grace_ends_at > database time`. Elapsed activation/extension facts advance the lifecycle cursor but do not create or provision a credential.
4. Provisioning success rechecks entitlement against PostgreSQL time while holding the credential and operation rows. If entitlement has elapsed, Access stores the allocation snapshot, completes the provision operation, atomically moves to `revoking`, and emits a new revoke command. If a terminal lifecycle event already created the revoke operation, a later physical success may only advance its allocation revision and snapshot while remaining `revoking`. Older allocation results cannot overwrite a newer snapshot. Neither path emits `access.ready.v1`.
5. Revoke operations capture `allocation_revision`. `access.revoke.succeeded.v1` carries `desired_revision`, `allocation_revision`, `all_assigned_nodes_removed: true`, and a unique node list. Access marks a credential revoked only when this set exactly equals its snapshot for that allocation revision. An empty set is valid only for a zero-allocation operation; partial and duplicate proofs are rejected.
6. Every Access outbox insert increments a credential-owned `aggregate_sequence`, stores it in the row and envelope, and uses a lower-sequence publication barrier. Transaction timestamps are not an ordering authority.
7. The public rate limiter evaluates and conditionally increments both HMAC-derived counters in one Redis Lua script. A rejected IP does not consume a token budget, and a rejected token does not consume an IP budget.
8. URL issuance first takes an advisory lock derived from the global idempotency key and then the subscription lock. Concurrent reuse across subscriptions deterministically resolves to one success and one `409 idempotency_key_conflict`.
9. `/s`, `/s/`, nested malformed `/s/` paths, malformed tokens, unknown tokens, and unavailable credentials share the same generic no-store 404 response. Access status reports `expired`, never `ready`, after the database entitlement boundary.
10. A successful provisioning-material read must append a security audit event with action, outcome, actor service, credential ID, and database timestamp before plaintext material is returned. The audit contains no VLESS UUID or response payload.

## Consequences

- A persistent lifecycle gap pauses one source partition and requires investigation of the missing producer sequence; operators must not advance the cursor or Kafka offset manually.
- The v1 lifecycle schema gains a required field before production release. Contract examples and tests are updated together.
- Stage 6 must produce revoke results with exact allocation proof and must reject stale Access command sequences/revisions.
- PostgreSQL integration tests cover out-of-order lifecycle delivery, delayed provisioning success, partial and zero-allocation revoke, reversed outbox timestamps, and concurrent global idempotency reuse.
- Redis integration tests cover cross-dimension budget isolation and concurrent limits.

## Rejected alternatives

- Trusting `occurred_at` or comparing timestamps: rejected because clocks and cross-topic delivery do not establish aggregate order.
- Marking a gap processed and relying on a later periodic repair: rejected because it creates an observable stale-access window and loses the Kafka retry signal.
- Accepting a boolean revoke success without an exact node set and revisions: rejected because partial removal could become terminal success.
- Persisting provisioning material in an audit payload: rejected because audit must prove access without duplicating the secret.
