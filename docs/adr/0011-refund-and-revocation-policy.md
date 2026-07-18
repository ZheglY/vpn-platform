# ADR 0011: Refund and Revocation Policy

Status: Accepted

Date: 2026-07-12

## Context

Refund behavior changes user entitlement and VPN credential state. Partial refunds and automatic refund semantics can become legally and operationally complex.

## Decision

In v1, support only full refunds initiated by an operator.

Partial refunds are not supported.

Each successful payment maps to exactly one immutable `subscription_period` through `source_payment_id` and `source_order_id`.

After a confirmed full refund:

- the matching `subscription_period` is marked refunded/revoked;
- subscription entitlement is recalculated from remaining non-refunded periods;
- if no valid current or future entitlement remains, subscription transitions to `revoked` and emits `subscription.revoked.v1` with reason `refund`;
- if refund removes the current entitlement but a future period remains, subscription transitions to `pending` and emits `subscription.revoked.v1` with reason `refund_gap`; access stays revoked until the scheduler emits a new activation at the future period boundary;
- refunding a historical or future period does not revoke access while another period remains currently valid;
- notification is emitted;
- audit record includes operator, reason, payment/refund IDs, and correlation ID.

Refund confirmation comes from billing provider verification and durable billing state, not from an unverified request.

## Consequences

- Stage 3 implements full refund records and provider verification only if refund scope is approved for that stage.
- Stage 4/5/6 must test revocation propagation and duplicate refund events.
- Stage 4 must test multiple paid periods and refund of a period that is current, future, and historical.
- Refund policy must be revisited before real sales.
