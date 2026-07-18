# Subscription Lifecycle Recovery

## Scope

This runbook covers Stage 4 entitlement processing only. An `active`
subscription is a paid entitlement and is not proof that VPN access is
provisioned. Do not create tokens, VLESS credentials, or Xray changes while
following this runbook.

## Payment Event Not Applied

1. Check `subscription-service /readyz` for PostgreSQL, Billing, and Kafka.
2. Check consumer group lag for `subscription-service-v1`, partitioned by the
   `user:{user_id}` key. Do not print event payloads.
3. Check safe counts/state in `inbox`, `subscription_periods`, and `outbox`.
   Do not export normalized payload JSON.
4. Confirm the Billing order still exists and its status is `paid` through the
   allowlisted internal API. Never read the Billing database from Subscription.
5. Restore the failed dependency and allow the consumer to retry. A transient
   failure must not be manually marked poison or skipped.

## Poison Record

`consumer_dead_letters` contains only topic, partition, offset, payload hash,
and reason code. Compare the hash with the secured source record without copying
the raw payload into tickets, logs, or chat. Correct the producer/contract before
replay. Stage 4 has no automatic dead-letter replay command; an operator must not
delete the metadata or advance offsets manually without an incident record.

## Stuck Lifecycle Transition

1. Check subscriptions whose `next_transition_at` is due.
2. A non-null `transition_lease_until` in the past is recoverable; the worker
   will claim it again with `FOR UPDATE SKIP LOCKED`.
3. Restore the service and verify active becomes grace at period end, then
   expired at grace end with one `subscription.expired.v1` outbox row.
4. Do not edit period timestamps. Paid period identity and timing are immutable.

## Outbox Not Published

Restore Kafka and allow retry. Rows in `pending` or expired `processing` lease
state are recoverable. Do not insert a replacement event: the durable dedupe key
and event ID are authoritative. Verify publication before marking an incident
resolved.

## Refund Waiting For Payment

A verified refund can arrive before its payment event and remain `pending` with
`payment_not_received`. Restore payment-event processing and allow reconciliation.
Do not manufacture a period or revoke a user manually. Revocation is emitted only
when recalculation finds no valid current or future paid period.
