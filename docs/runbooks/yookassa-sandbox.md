# YooKassa Sandbox and Billing Recovery

## Scope

This runbook covers Stage 3 sandbox payments only. Do not point the service at a production shop. Receipts, VAT, buyer data, refunds, recurring payments, and saved payment methods are not implemented.

## Invariants

- Never retry create with a newly generated provider idempotency key.
- Never mark a payment successful from a webhook body or operator assumption.
- Never paste credentials, confirmation URLs, or provider payloads into logs, tickets, or chat.
- A terminal payment/order transition and its outbox event must be atomic and unique.
- Never issue or retry provider create at or after `provider_create_deadline`; the service must fail the payment and cancel its open order without a provider POST.

## Local Exercise

```powershell
Copy-Item .env.example .env
make compose-smoke
```

The smoke starts empty databases, runs PostgreSQL concurrency/deadline/lease/atomicity tests, sends concurrent purchase commands, forces an ambiguous create after the fake provider commits, waits for same-key reconciliation, sends duplicate and out-of-order webhooks, and verifies one succeeded event in Kafka.

## Triage

1. Check `billing-service /readyz` and connectivity to PostgreSQL, identity, catalog, Kafka, and the provider.
2. Inspect counts and safe state fields in `payments`, `webhook_inbox`, and `outbox`. Do not select `confirmation_url` or credentials.
3. For `verification_pending`, confirm the create deadline has not elapsed. Let the reconciler repeat create with the persisted key; do not issue a manual create.
4. For a pending inbox row, verify the provider GET is reachable. Unknown provider IDs and provider mismatches become `dead` with a redacted reason code and require investigation, not fulfillment.
5. For an unpublished outbox row, restore Kafka and allow retry. Do not insert a replacement event because the original event ID and aggregate uniqueness are authoritative.

## Recovery Rules

- Duplicate webhook: acknowledge and retain the existing normalized inbox row.
- Delayed or contradictory webhook: retrieve current provider state; terminal local state remains monotonic.
- Provider timeout or `5xx` during create: keep `verification_pending` and retry with the same key inside the create window.
- Provider create deadline reached: expect payment `failed`, its still-open order `canceled`, no new provider POST, and no retained confirmation URL.
- Permanent provider rejection: mark payment failed and cancel only its still-open order.
- Provider data mismatch: fail closed, record a redacted code, and escalate. Never reveal the compared payload values.

Production alert routing, real credentials, retention, legal reconciliation, and public webhook edge controls are Stage 8/9 blockers. The production edge must use the current YooKassa source-IP list from `https://yookassa.ru/developers/using-api/webhooks` plus a bounded rate limit. Continue authenticated GET verification even for allowlisted sources.
