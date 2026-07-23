# Telegram Notifications and Recovery

This runbook covers Stage 7 notification-service and its typed telegram-bot delivery boundary. It does not authorize reading raw Kafka values, Telegram chat IDs, rendered text, Bot API payloads, or bearer/VPN credentials.

## Delivery Semantics

- Kafka processing and notification jobs are durable at-least-once.
- A source inbox and business key prevent another business job for exact replay or an equivalent fact.
- A PostgreSQL lease prevents concurrent workers from claiming one job. An expired lease is recoverable.
- telegram-bot has an ephemeral Redis guard for the stable delivery ID.
- Telegram may accept `sendMessage` while the HTTP result is lost. A later retry can duplicate the visible message; neither service claims exactly-once delivery.
- Attempts are bounded. Retry delay uses capped backoff, except a bounded Telegram `Retry-After` takes precedence.

## Safe Signals

Inspect aggregate counts by job `status`, `notification_type`, bounded `terminal_reason_code`, attempt bucket, and oldest `next_attempt_at`. Inspect consumer lag, cursor sequence, source topic/partition/offset, payload SHA-256, and service readiness. Never select `variables`, reconstruct message text, print an Identity response, inspect Redis delivery values, or attach Kafka records to a ticket.

Allowed incident evidence is limited to notification ID, source event ID, business fact category, timestamps, attempt count, status, bounded reason code, correlation ID, Kafka coordinates, and digest. Do not put Telegram user/chat IDs, subscription URLs, VLESS UUIDs, raw owner responses, or rendered content in logs or tickets.

## Telegram Outage or Retry Backlog

1. Confirm notification-service, telegram-bot, Redis, Identity, and the fake/real Telegram endpoint readiness without printing configuration or tokens.
2. Compare pending/retry counts and oldest due time. A growing due backlog with `telegram_temporary` indicates an outbound problem; `identity_unavailable` or `access_state_unavailable` identifies an owner dependency.
3. Restore the dependency and let due jobs resume. Do not bulk-update `next_attempt_at`, attempts, leases, or statuses.
4. If several replicas are running, confirm leases advance and one notification ID is not simultaneously processing. Stop deployment changes if lease ownership is inconsistent.
5. Escalate before the bounded attempt ceiling is reached. A permanently failed job can only be retried through the typed admin action after the cause is fixed.

## Telegram 429

1. Confirm the bounded reason is `telegram_rate_limited` and inspect the due-time distribution, not the Telegram response body.
2. Notification honors `Retry-After` up to the configured cap. Do not bypass it or add parallel senders.
3. Reduce avoidable notification bursts at the producer/policy level. Do not merge unrelated jobs or discard durable work.
4. Escalate sustained rate limiting with counts and time windows only.

## Bot Blocked, Chat Missing, or Unauthorized Token

- `telegram_bot_blocked` and `telegram_invalid_target` are permanent for that job. Do not repeatedly retry until Identity state or the user relationship changes.
- `telegram_unauthorized` may indicate revoked Bot API credentials. Stop delivery, rotate through the approved secret process, and verify telegram-bot readiness. Never print or test the token in a shell history.
- An operator retry requires the `notification.retry` permission, a human reason, and a new idempotency key. Exact replay of that action is safe; changed input must conflict.

## Poison Event and Notification DLQ

Notification DLQ stores only topic, partition, offset, SHA-256, validated event type when available, and a bounded reason. It deliberately stores no raw payload.

1. Use the admin read endpoint to identify the coordinate and reason without retrieving the source value.
2. Fix the producer or contract first. An invalid envelope, forged partition key, changed event identity, or invalid data must not be replayed unchanged.
3. Stage 7 does not expose a generic admin replay or arbitrary Kafka publisher. Keep the record in Kafka retention and preserve the safe coordinate while preparing a reviewed recovery.
4. For a corrected producer fact, publish a new valid versioned event through the owning service/outbox. Do not edit Notification inbox/cursors or mark a DLQ row replayed by SQL.
5. A future automated coordinate replay must verify the retained record hash in-process, be topic allowlisted and typed, and receive a separate ADR/security approval.

## Duplicate Delivery Investigation

1. Compare the source event ID, business key, notification ID, attempts, and telegram-bot delivery guard outcome. Do not compare text or chat ID.
2. Two jobs for one business key indicate a database/contract defect and require immediate escalation.
3. One job with multiple attempts can be the documented ambiguous timeout: Telegram accepted the send, but the response was lost before durable completion.
4. Preserve timing and bounded outcomes. Do not promise exactly-once delivery or erase job history.

## Invalid Template

`invalid_template` is permanent. Verify the compiled `(notification_type, template_version)` renderer, escaping test, 4096-rune limit, and forbidden-content tests. Deploy a corrected code/template version and use a reasoned typed retry only after the renderer is valid. Templates are code-owned; there is no runtime user template execution.

## Notification Lag or Sequence Gap

1. Check lag by topic/partition and the producer cursor for `(producer, aggregate_type, aggregate_id)`.
2. A later sequence remains deferred and uncommitted until the missing predecessor arrives. Do not send it, DLQ it as poison, or advance the cursor manually.
3. Restore the producer outbox/Kafka path for the lower sequence. A reused sequence with changed content is a durable conflict and requires engineering review.
4. Access-related messages are checked against current Subscription entitlement immediately before send. Ready/degraded messages also check current Access state. A terminal Subscription suppresses with `stale_subscription_state`; an incompatible credential state suppresses with `stale_access_state`.

## Local Verification

```powershell
make compose-smoke
make stage7-smoke
```

The Stage 7 smoke uses only generated local certificates, fake Telegram behavior, and local Xray. It proves `429`, permanent error, duplicate-event, restart, admin retry, and post-revoke stale suppression paths.
