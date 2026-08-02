# ADR 0015: Internal Authentication and Service Identity

Status: Accepted

Date: 2026-07-12

## Context

The Stage 0 OpenAPI skeleton originally did not define authentication. That made internal operations look callable without identity and left IDOR-sensitive operations such as order creation, token rotation, credential material retrieval, and admin actions ambiguous.

## Decision

Use mTLS as the concrete authentication mechanism for all internal service-to-service APIs in v1.

Rules:

- Internal HTTP APIs require client certificates issued by a platform CA.
- Service authorization is based on strict SPIFFE URI identities from verified certificate chains, as clarified in ADR 0018, mapped to a service identity such as `telegram-bot`, `billing-service`, `access-service`, or `provisioning-service`.
- A private network is defense in depth, not authentication.
- Spoofable headers such as `X-Internal` are never sufficient.
- Endpoint allowlists define which service identities may call each operation.
- Admin CLI/internal admin API also uses mTLS, with admin certificate identity mapped to RBAC roles and audited actions.
- Local development uses generated development CA/certificates in the default Compose path, so the same identity checks remain active.

Public ingress exceptions:

- Telegram webhook uses the Bot API secret header `X-Telegram-Bot-Api-Secret-Token`.
- YooKassa webhook is public ingress. It is authenticated by durable storage plus idempotent provider API verification before fulfillment, not by trusting the inbound payload.
- `GET /s/{token}` is a public bearer endpoint. The bearer token is carried in the path for Happ compatibility and must be redacted everywhere.

## Consequences

- Stage 1 platform HTTP middleware must extract, validate, and authorize mTLS identities.
- OpenAPI must define security for every operation.
- Order/payment APIs are internal bot-facing APIs and must not accept arbitrary unauthenticated `user_id` in a public body.
- Provisioning credential material endpoint is restricted to `provisioning-service`.
