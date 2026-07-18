# ADR 0020: Stage 4 Subscription Lifecycle

Status: Accepted

Date: 2026-07-18

## Context

The Stage 3 `billing.payment.succeeded.v1` contract is already implemented and
does not carry the immutable plan duration, grace period, or selected region.
Subscription processing must not read Billing or Catalog tables, and a paid
entitlement is not evidence that VPN credentials are provisioned.

Kafka delivery, worker restarts, concurrent payments, delayed events, and
refunds can all repeat or arrive out of order. Time based transitions must also
behave deterministically at exact period and grace boundaries.

## Decision

- `subscription-service` owns a separate PostgreSQL database containing one
  subscription per user, immutable paid periods, consumer inbox records,
  consumer dead-letter metadata, and an outbox.
- The existing `billing.payment.succeeded.v1` event remains unchanged. Before a
  new payment is applied, `subscription-service` fetches the immutable order
  snapshot from Billing over the existing internal mTLS order endpoint. It
  validates user, order, plan, amount, and currency against the event and stores
  the required duration, grace period, and region in its own period record.
- Billing permits only `telegram-bot`, `subscription-service`, and `admin-cli`
  identities to read an order. This is synchronous enrichment needed before the
  Kafka record can be acknowledged; it is not cross-service database access.
- Each payment maps to exactly one immutable period. A unique
  `source_payment_id` protects against duplicate events with different event
  IDs. Reuse of an event ID with changed identity or payload hash is a durable
  conflict. An inbox event, state transition, period, and resulting outbox event
  are committed atomically.
- User-scoped transaction advisory locking serializes concurrent payments and
  refunds. For an active or grace subscription a new period starts at the
  current entitlement end. Otherwise it starts at the provider-confirmed
  `paid_at`. Each extension adds the complete purchased duration.
- A first or restarted entitlement publishes `subscription.activated.v1`; an
  appended entitlement publishes `subscription.extended.v1`. Neither event
  means VPN access is ready. A payment delivered after its immutable grace
  boundary commits directly as `expired` and emits only
  `subscription.expired.v1`; no observable active state or activation event is
  created.
- The scheduler leases due subscriptions with `FOR UPDATE SKIP LOCKED`. At
  `now >= current_period_end`, active becomes grace. At
  `now >= grace_ends_at`, active or grace becomes expired and atomically emits
  `subscription.expired.v1`. A late worker may move directly from active to
  expired. PostgreSQL `clock_timestamp()` read inside the transaction is the
  authoritative production clock. Leases expire and can be recovered after a
  crash.
- Confirmed full refunds are consumed from `billing.refund.succeeded.v1`. A
  refund that arrives before its payment is stored as pending inbox work and is
  reconciled after the period appears. The matching period is marked refunded
  once, and entitlement is recalculated from non-refunded periods.
- A refund emits terminal `subscription.revoked.v1` with reason `refund` when no
  valid current or future entitlement remains. If refund removes current access
  while a future period remains, the subscription becomes `pending` and emits
  the same event with reason `refund_gap`; the scheduler emits a fresh activation
  when that future period begins. Refunding a historical or future period while
  another period is currently valid does not revoke access. Refund provider
  verification and operator audit remain Billing responsibilities.
- Structurally invalid, unsupported, or contract-inconsistent Kafka records are
  recorded as durable dead-letter metadata containing topic coordinates,
  payload hash, and a bounded reason code. Raw payloads and entitlement details
  are not written to logs or dead-letter metadata. A permanent reconciliation
  conflict atomically clears the normalized inbox payload, marks the inbox row
  `dead`, and copies only source coordinates, payload hash, and reason to
  `consumer_dead_letters`. Transient failures are not acknowledged and are
  retried with bounded backoff.
- Every outbox insert increments a subscription-owned `aggregate_sequence`.
  Claim SQL will not lease sequence N+1 until every lower sequence for that
  subscription is published, preventing multiple service instances from
  reversing lifecycle events before Kafka send.

## Consequences

- Subscription availability temporarily depends on the Billing order read API
  for the first delivery of a payment. Duplicate events for an already stored
  payment do not require another Billing call.
- Billing order snapshots must remain immutable and available for at least the
  payment/refund retention period.
- Stage 4 exposes only entitlement status. Token generation, Happ delivery,
  credential provisioning, and access revocation commands remain Stage 5 and
  Stage 6 work.
- PostgreSQL integration tests must cover duplicate IDs, concurrent payments,
  exact time boundaries, lease recovery, concurrent ordered outbox delivery,
  atomic outbox behavior, refund before payment including permanent conflicts,
  and refunds of current, future, and historical periods.
