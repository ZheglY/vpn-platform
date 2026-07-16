# ADR 0010: YooKassa Sandbox Payment Verification

Status: Accepted

Date: 2026-07-12

## Context

YooKassa is the first payment provider, but v1 is sandbox only. Webhooks are useful signals but must not be trusted as the sole source of truth for fulfillment. Ambiguous provider responses must not accidentally grant access or create duplicate effects.

## Decision

Use YooKassa sandbox only until legal/payment requirements are decided.

Payment creation uses a provider idempotency key. Internal order/payment creation also requires an `Idempotency-Key` tied to subject, operation, and request hash.

Webhook handling:

- limit request body;
- parse strict JSON;
- store normalized dedupe/inbox record durably;
- never log raw webhook body;
- return a fast 2xx after durable storage or accepted duplicate handling;
- an idempotent worker fetches payment/refund from YooKassa API before fulfillment;
- the worker verifies provider payment ID, status, amount, currency, shop/account, and internal metadata;
- the worker transitions order/payment/refund state and writes outbox event in one PostgreSQL transaction;
- reconciliation handles notifications that were stored but not yet verified.

Receipts, 54-FZ, VAT, and required buyer data are not implemented until a separate product/legal decision. Payment provider adapter design must allow adding receipt fields later without changing subscription domain models.

## Consequences

- Stage 3 must use fake YooKassa server and sandbox tests for duplicate/out-of-order webhooks, timeouts, 5xx, ambiguous responses, and amount mismatch.
- No access is activated on webhook payload alone.
- A provider GET may still happen inline only if a strict short timeout is configured and durable recovery through inbox/reconciliation remains in place.
- No real YooKassa credentials are committed or used in Stage 0.
