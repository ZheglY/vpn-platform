# ADR 0019: Stage 3 Billing State and Idempotency

Status: Accepted

Date: 2026-07-17

## Context

An internal retry, Telegram duplicate, ambiguous provider response, duplicate webhook, or delayed notification must not create a second charge or terminal effect. A YooKassa `500` after create is ambiguous because the provider may already have committed the payment. Webhook notifications are public signals and do not establish payment truth by themselves.

## Decision

- Catalog owns immutable plan versions, prices, regions, and publication. Billing copies the selected values and accepted terms version into an immutable order snapshot.
- Billing owns separate `orders`, `payments`, `idempotency_keys`, `webhook_inbox`, and `outbox` tables. No service reads the catalog or identity database directly.
- Order and payment HTTP commands require an internal `Idempotency-Key`. Billing binds subject, operation, key, and canonical request hash under a PostgreSQL advisory transaction lock. The same key and request replays the resource; a different request returns conflict.
- A user has at most one compatible open order. An order has at most one payment. New command keys may point to the same compatible resources without creating provider work.
- Billing generates and persists a UUID v4 provider idempotency key before network I/O. Ambiguous create responses move the payment to `verification_pending`; reconciliation repeats create with the same key until a 23-hour deadline, leaving one hour inside YooKassa's documented 24-hour result window.
- Provider responses are accepted only for sandbox objects with the configured account, exact amount/currency, and exact order/payment metadata. A terminal create response is verified by subsequent GET before it can create a terminal effect.
- Webhook ingress stores only provider object ID, event type, and observed status under a unique constraint. It never stores the raw payload. A worker uses authenticated provider GET and treats the current provider object as truth.
- Terminal transitions are monotonic. Payment transition, matching order transition, and one schema-versioned outbox event commit in one transaction. Replayed current terminal state is a no-op; a contradictory verified terminal state is rejected.
- The current YooKassa canceled payment representation does not provide a cancellation timestamp required by the platform event. Billing uses the local verified observation time and does not claim it is provider event time.
- Stage 3 outbox records retry with capped exponential backoff and are never automatically discarded. A Kafka DLQ is deferred until a consumer exists; stuck records remain operator-visible and reconcilable.

## Consequences

- Confirmation URLs exist transiently in billing storage and the immediate bot response, but are forbidden from logs, metrics, traces, Kafka, and fake-provider diagnostics used outside local development.
- Full refunds, receipts, recurring payments, saved payment methods, and real-shop operation remain outside Stage 3.
- Stage 4 must consume payment events through its own inbox and cannot infer entitlement from billing tables.
- Compose acceptance must demonstrate ambiguous create recovery, a second purchase click, duplicate and out-of-order webhooks, a single terminal event, and rejected non-allowlisted mTLS identity.
