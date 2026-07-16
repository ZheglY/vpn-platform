# ADR 0016: One-Time Subscription URL Issuance After Provisioning

Status: Accepted

Date: 2026-07-12

## Context

The earlier Stage 0 architecture created a plaintext subscription token before asynchronous provisioning, stored only a hash, and planned to deliver the URL after provisioning. That was impossible: after the async step, plaintext token would no longer exist, and sending the token through Kafka is forbidden.

## Decision

Do not create the subscription token before provisioning.

Flow:

1. `access-service` creates an access credential record after subscription entitlement is activated.
2. `access-service` publishes `access.provision.request.v1` with only `credential_id`, `operation_id`, and `desired_revision`.
3. `provisioning-service` applies the credential through node-agent and publishes `access.provision.succeeded.v1` or `access.provision.failed.v1`.
4. After primary node success, `access-service` marks access as `ready` or `degraded` and publishes `access.ready.v1`. This event contains no URL or token.
5. Notification/Telegram tells the user that VPN is ready and offers a "get link" action.
6. On click, `telegram-bot` calls authenticated `POST /internal/v1/subscriptions/{subscription_id}/subscription-url/issue`.
7. `access-service` generates the token, stores only its HMAC/hash lookup value, and returns the full URL once in the synchronous response.
8. `telegram-bot` immediately sends the URL to the user and must not persist or log it.

Lost URL:

- Do not show old plaintext.
- Rotate by revoking the old token and issuing a new one through the same synchronous pattern.
- Default rotation has no overlap. Any overlap window must be configured and tested explicitly.

## Consequences

- Kafka never carries subscription URL or token.
- The plaintext token exists only in memory during issuance/rotation and in the user's Telegram history after delivery.
- Stage 5 must test one-time issuance, idempotency semantics, redaction, lost-link rotation, and duplicate button presses.
