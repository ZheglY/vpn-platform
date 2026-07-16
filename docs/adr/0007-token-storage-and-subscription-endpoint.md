# ADR 0007: Subscription Token Storage and Endpoint Behavior

Status: Accepted

Date: 2026-07-12

## Context

The Happ subscription URL is a bearer secret. Anyone with the URL can fetch the subscription document. The URL may leak through chat history, browser history, reverse-proxy logs, traces, metrics, screenshots, or support messages.

Happ documentation describes subscription metadata through headers/body and fallback behavior when a subscription URL returns an error status or times out. Compatibility must be tested during Stage 5.

## Decision

Generate subscription tokens with at least 256 bits of entropy from a CSPRNG only when the user explicitly requests the link after access is ready.

Store only a lookup hash/HMAC of the token. Do not persist plaintext tokens.

Use a path segment URL:

```text
https://<subscription-host>/s/{token}
```

Do not put bearer tokens in query strings.

Initial invalid-token behavior:

- unknown, expired, and revoked tokens return the same external response;
- `404 Not Found`;
- `Content-Type: text/plain; charset=utf-8`;
- `Cache-Control: no-store`;
- generic body with no token, user ID, subscription ID, credential ID, or reason.

Subscription hostname access logs must not write request path. Application logs, traces, metrics, and error reporting must use route templates such as `/s/{token}` and never record the raw token.

If a user loses the subscription URL, do not reveal old plaintext. Rotate the token, revoke the old token, and issue a new URL. Rotation is immediate by default. A short overlap window is allowed only when explicitly configured and tested.

Token issuance flow is defined in ADR 0016. In short: create credential first, provision it, mark access ready, then generate and return the URL exactly once through an authenticated synchronous call from telegram-bot.

## Consequences

- Stage 5 must run Happ compatibility tests and may supersede this ADR if `404` causes unacceptable UX.
- Token rotation and redaction tests are required.
- Leaked-token runbook must exist before production.
- Kafka must never carry subscription URL or token.
