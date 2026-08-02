# ADR 0025: Stage 7 notification ordering and Telegram delivery

- Status: Accepted for Stage 7 implementation
- Date: 2026-07-19
- Owners: notification-service, telegram-bot, identity-service
- Security impact: High
- Contract impact: notification consumers, Access user-facing facts, Subscription grace fact, Telegram internal delivery API

## Context

Stage 7 must turn domain facts into useful Telegram notifications without making telegram-bot a durable workflow owner or storing the Bot API token outside that service. Kafka and Telegram are both at-least-once systems. Telegram cannot prove whether `sendMessage` succeeded when the HTTP result is lost, so an exactly-once claim would be false.

Lifecycle facts also span several Kafka topics. Subscription already owns one `aggregate_sequence` across its lifecycle topics and Access owns one outbox sequence across credential facts. Billing success remains an immutable financial fact and does not establish the current entitlement or access state. Kafka receive time and event timestamps are not ordering authorities.

## Decision

1. `notification-service` owns its PostgreSQL database, normalized inbox records, producer cursors, business deduplication, typed template versions, notification jobs, retry leases, delivery history, and sanitized dead-letter metadata. It never stores a raw Kafka payload, rendered message, Telegram chat ID, Bot API request/response, bearer URL, VLESS UUID, or credential ciphertext.
2. `telegram-bot` remains the only owner of the Telegram Bot API token. Notification calls one allowlisted mTLS endpoint with a stable `delivery_id`, a transient Telegram chat ID, a fixed notification type/template version, and bounded rendered text. The endpoint cannot select a Telegram method, API base URL, token, parse mode, or arbitrary request fields.
3. Templates are compiled Go renderers selected by `(notification_type, template_version)`. Rendering is deterministic. Dynamic values are HTML escaped, output is bounded by Telegram's message limit, and access-ready text tells the user to invoke the authenticated one-time `/link` flow. It never embeds a subscription URL.
4. Delivery is durable at-least-once with business-level deduplication. A job lease prevents concurrent sends, and telegram-bot keeps an ephemeral Redis request-hash guard for a stable delivery ID. An exact completed replay is suppressed while the guard exists; a changed request conflicts. A timeout after Telegram accepted the message can still produce a duplicate after retry, and this limitation is exposed in the runbook.
5. Telegram `429` honors a bounded `Retry-After`; network failures, connection resets, `5xx`, and unknown responses retry with capped exponential backoff. Invalid requests, blocked bots, missing chats, unauthorized/revoked token responses, message overflow, and idempotency collisions become bounded permanent reason codes. No provider description or response body is persisted or logged.
6. Sequenced Subscription and Access facts use a durable per-aggregate cursor across their respective topics. Exact event ID plus payload replay is a no-op even when Kafka assigns a new offset; the first source coordinates remain the provenance record. Reuse of an event ID or aggregate sequence with another payload is a durable conflict, and a gap remains unacknowledged. Unsequenced immutable Billing facts use event identity and business keys.
7. Every job also stores the source producer/aggregate/sequence and an explicit `(delivery_stream_key, delivery_sequence)`. Subscription lifecycle jobs use the subscription sequence, Access jobs use the credential outcome sequence, and payment/refund jobs use one payment stream with `payment_confirmed=1` and full `refund_confirmed=2`. Event insertion takes a transaction-scoped advisory lock for that delivery stream so concurrent topics cannot bypass the barrier.
8. Claims are FIFO within one delivery stream: a later job is not claimable while an earlier job is pending, retrying, or processing. Delivered, suppressed, and permanently failed predecessors are terminal and do not block forever. A terminal/superseding fact atomically suppresses older pending/retry jobs; if the predecessor is already processing, the terminal job waits, and a failed/retry completion is converted to `suppressed/superseded_by_terminal_state`. An administrator cannot resurrect a failed predecessor after any successor exists.
9. A full refund is the terminal authority for its payment notification stream. Once its fact is recorded, an earlier payment-confirmed retry is suppressed, and a delayed payment fact is inserted as suppressed even if it arrives after the refund. This rule does not claim knowledge of a refund before Notification receives the owner-produced refund fact.
10. Before a send, Notification resolves the active Telegram target and current terms consent through an allowlisted Identity mTLS endpoint. The chat ID exists only in memory and the outbound mTLS request. Extension jobs require the current active period end to equal the event period end; grace jobs require the current grace deadline; expired/revoked jobs require the matching terminal owner state. Access-ready, degraded, and provisioning-failed jobs require current entitlement, and ready/degraded jobs additionally query Access for the current credential state. A job is suppressed when an owner has advanced to an incompatible state.
11. Notification policy is:

| Source fact | User result |
|---|---|
| `billing.payment.succeeded.v1` | payment confirmed and provisioning started |
| `subscription.activated.v1` | suppressed as the same initial purchase chain already covered by payment and readiness |
| `subscription.extended.v1` | subscription extended |
| `subscription.grace.started.v1` | grace period started |
| `subscription.expired.v1` | subscription expired |
| `subscription.revoked.v1` | entitlement/access revoked with a safe reason category |
| `billing.refund.succeeded.v1` | refund confirmed; it does not claim physical revoke |
| `access.ready.v1` | access ready; degraded status mentions reduced redundancy and requires explicit `/link` |
| `access.provisioning.failed.v1` | terminal setup failure with no node or credential details |
| `access.revoked.v1` | suppressed as a physical confirmation of the already-notified terminal entitlement fact |

12. `subscription.grace.started.v1`, `access.provisioning.failed.v1`, and `access.revoked.v1` are versioned owner-produced facts. They carry only safe identifiers, positive aggregate sequence, bounded state/reason, and timestamps. Access produces its two facts in the same transaction that applies the corresponding Provisioning outcome.
13. Notification DLQ rows contain source topic, partition, offset, SHA-256, event type when validated, and a bounded reason code only. Stage 7 exposes read-only sanitized metadata, not generic replay. Recovery requires correcting the producer/contract first and following the bounded partition procedure in the notification runbook; unchanged poison records must not be replayed.

## Consequences

- Restarting Notification or running several replicas does not lose jobs and does not create duplicate business jobs.
- Exactly-once user-visible Telegram delivery is not promised. The residual ambiguous-timeout duplicate is measurable and diagnosable without exposing message text or chat ID.
- Notification availability depends on Identity at send time and on owner state for terminal-sensitive messages. Those failures are recoverable jobs, not dropped records.
- A terminal message can pass a permanently failed predecessor, but it cannot overtake a still-running predecessor. This preserves user-visible order without allowing one permanently failed Telegram call to block revocation forever.
- A future non-Telegram channel can consume the same normalized policy without gaining access to Telegram credentials.

## Rejected alternatives

- Put Telegram token in notification-service: rejected because it breaks credential ownership and broadens compromise impact.
- Send raw Telegram methods or payloads over the internal API: rejected because it creates a privileged proxy.
- Order by Kafka receive time or `occurred_at`: rejected because neither proves causality across topics.
- Persist rendered text or chat ID for retry: rejected because deterministic rendering and just-in-time identity resolution avoid unnecessary personal data.
- Claim exactly-once after a successful local DB commit: rejected because Telegram may have accepted a request whose response was lost.
