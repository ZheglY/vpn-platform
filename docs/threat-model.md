# Initial Threat Model

Method: STRIDE-inspired model updated for Stage 3. It must be reviewed again before connecting production VPS or real payment credentials.

## Scope

In scope:

- Telegram bot webhook ingestion.
- YooKassa sandbox payment flow and webhook verification.
- Subscription URL and Happ document delivery.
- Access credential lifecycle.
- Kafka outbox/inbox processing.
- Provisioning control plane and node-agent.
- Admin CLI/internal API.
- Observability, logs, traces, metrics, and audit.

Out of scope for v1:

- Custom VPN client.
- Cryptocurrency payments.
- Production legal launch.
- Automatic VPS purchasing.
- Web admin UI.
- Traffic content inspection.

## Assets

| Asset | Why it matters |
|---|---|
| Telegram bot token and webhook secret | Allows bot impersonation or forged updates |
| YooKassa credentials and webhook trust chain | Allows payment fraud or false fulfillment |
| Subscription bearer token | Grants access to Happ subscription document |
| VLESS client UUIDs and REALITY private keys | Grants or compromises VPN access |
| Node-agent mTLS certificates | Allows unauthorized provisioning |
| Admin credentials and roles | Allows user blocking, refunds, grants, and revocation |
| Payment/order records | Financial correctness and audit |
| Consent records | Legal/privacy evidence |
| Kafka messages and outbox/inbox state | Eventual consistency and lifecycle correctness |
| Xray last-known-good config | Data-plane continuity |

## Trust Boundaries

| Boundary | Threat focus |
|---|---|
| Internet to edge | spoofing, DDoS, token leakage, path logging |
| Edge to services | header spoofing, missing or weak service identity |
| Services to PostgreSQL | overprivileged credentials, cross-service access |
| Services to Kafka | unauthorized publish/consume, replay, poison messages |
| Billing to YooKassa | ambiguous provider response, credential leakage |
| Bot to Telegram API | token leakage, retry duplication |
| Access endpoint to Happ | bearer URL leakage, cache leaks, enumeration |
| Provisioning to access-service | unauthorized credential material retrieval |
| Provisioning to node-agent | unauthorized credential apply/revoke |
| Node-agent to Xray/OS | invalid config, privilege escalation |
| Admin CLI/internal API | RBAC bypass, un-audited sensitive actions |

## STRIDE Summary

| Category | Key threats | Required mitigation |
|---|---|---|
| Spoofing | Forged Telegram/YooKassa webhooks, fake node-agent, fake internal service | Secret token checks, durable provider verification, mTLS, service identity allowlists, firewall default deny |
| Tampering | Payment amount/status changes, Kafka payload changes, Xray config corruption | Provider verification, JSON Schema, outbox/inbox, allowed state machines, Xray config test and atomic swap |
| Repudiation | Operator denies refund/revoke/grant, webhook processing unclear | Append-only audit, operation IDs, correlation IDs, immutable ledgers |
| Information disclosure | Subscription token in logs, VLESS UUID in traces, raw webhook/Telegram payloads, browsing data | Redaction, no raw payload logs, no destination/DNS history, no path logging on subscription hostname |
| Denial of service | Oversized requests, repeated payment clicks, webhook floods, Kafka lag, node overload | Body limits, rate limits, idempotency, bounded retries, DLQ, capacity reserve |
| Elevation of privilege | Admin RBAC bypass, node-agent shell execution, cross-service DB access | Deny-by-default RBAC, no shell command execution, least-privilege DB users, architecture checks |

## High-Priority Threat Scenarios

### T1 - Fake YooKassa webhook activates access

Risk: an attacker sends a forged `payment.succeeded` webhook.

Mitigation:

- Store normalized webhook dedupe/inbox record and return fast 2xx for accepted or duplicate notifications.
- Keep sandbox ingress bound to loopback. Before production, enforce an edge rate limit and the current official YooKassa IPv4/IPv6 allowlist so arbitrary unique provider IDs cannot cause unbounded inbox growth.
- Idempotent worker fetches payment/refund from YooKassa API before fulfillment.
- Verify provider payment ID, status, amount, currency, shop/account, and metadata.
- Transition payment/order and write outbox in one PostgreSQL transaction.
- Never activate access from webhook payload alone.
- Accept only sandbox objects for the configured account and exact internal order/payment metadata.
- Keep local terminal state monotonic when delayed notifications disagree with current provider state.

### T2 - Subscription bearer token leaks through logs

Risk: path `/s/{token}` is written to edge logs, traces, metrics, panic reports, or support screenshots.

Mitigation:

- Dedicated subscription hostname.
- Edge configured not to log request path for that hostname.
- Application logs use route template `/s/{token}` only.
- Metrics labels use status class and route template only.
- Token stored only as lookup hash/HMAC.
- Rotation revokes leaked token and issues a new URL.

### T3 - Kafka duplicate or out-of-order delivery creates double entitlement

Risk: duplicate `billing.payment.succeeded.v1` creates two subscription periods.

Mitigation:

- Inbox unique `event_id`.
- Business unique key for source payment/order period.
- Subscription state machine with optimistic locking.
- Contract and concurrency tests.

### T4 - Invalid Xray config removes working access

Risk: provisioning writes broken config and reloads Xray.

Mitigation:

- Node-agent builds candidate config.
- Run Xray config validation before swap.
- Atomic replace only after validation.
- Reload with bounded timeout.
- Keep last-known-good and rollback.
- Report redacted diagnostic.

### T5 - Operator performs sensitive action without audit

Risk: manual refund/revoke/grant cannot be attributed.

Mitigation:

- CLI/internal admin API uses authenticated identity.
- RBAC deny-by-default.
- Every sensitive action requires reason.
- Append-only audit record.
- Audit retention is 365 days in sandbox.

### T6 - Traffic privacy violation through diagnostics

Risk: platform stores DNS requests, visited domains, destination IPs, or packet contents.

Mitigation:

- Explicitly forbid these fields in domain models, logs, traces, metrics, and node health snapshots.
- Allow only aggregate bytes by credential/node, node load, active credential counts, and temporary technical metrics.
- Add Stage 8 retention jobs and secret/privacy scans.

### T7 - Plaintext subscription URL is lost between async stages

Risk: access-service creates a plaintext URL before provisioning, stores only a hash, and cannot deliver the URL after provisioning finishes without leaking it through Kafka or storage.

Mitigation:

- Do not generate a token before provisioning.
- Publish `access.ready.v1` without token.
- Generate and return URL exactly once through authenticated synchronous `subscription-url/issue`.
- Bot sends URL immediately and never stores/logs it.

### T8 - Provisioning credential material leaks through Kafka or broad internal API

Risk: provisioning needs VLESS UUID, but Kafka and broad internal APIs could leak credential material.

Mitigation:

- Kafka provision/revoke commands contain only operation ID, credential ID, and revision.
- Provisioning-service fetches material from access-service over mTLS.
- Endpoint is restricted to provisioning-service identity and audited.
- Access-service stores VLESS UUID encrypted at rest.
- REALITY private key stays only on VPN node.

### T9 - Revoke lifecycle is incomplete

Risk: access-service requests revoke but never learns whether all nodes removed the credential.

Mitigation:

- Access enters `revoking`.
- Provisioning publishes `access.revoke.succeeded.v1` or `access.revoke.failed.v1`.
- Access marks `revoked` only after all assigned nodes confirm removal.
- Reconciliation checks access state against provisioning allocations and node actual state.
- Terminal failure alerts require operator escalation.

### T10 - Telegram update is acknowledged before durable processing

Risk: webhook dedupe marks an update as processed before the side effect finishes; a concurrent duplicate receives `200`, Telegram stops retrying, and the user-visible effect can be lost if the first attempt fails.

Mitigation:

- Redis dedupe has separate `processing` and `completed` keys.
- A concurrent duplicate for an in-flight update receives retryable `503`.
- Only a completed update replay receives `200`.
- Failed processing releases the short processing lease with a bounded context that ignores request cancellation.
- Unit and smoke tests cover concurrent duplicate, failed processing cleanup, completed replay, and canceled request context cleanup.

### T11 - Telegram bot token leaks through outbound errors

Risk: HTTP client errors can include a URL containing `/bot{token}/...`, and wrapping the original `url.Error` would expose the token through logs or traces.

Mitigation:

- Telegram client converts transport, decode, HTTP status, API, and rate-limit failures into classified safe errors.
- Error strings never include request URL, token, or provider response description.
- Bot API `ok=false` and `parameters.retry_after` are parsed without logging raw response bodies.
- Redaction tests cover a synthetic `url.Error` containing the bot token.

### T12 - Ambiguous payment create produces multiple provider objects

Risk: YooKassa commits a payment but the response times out or returns `5xx`; a retry with a new key creates another payable object.

Mitigation:

- Generate and persist a UUID v4 provider idempotency key before the first network call.
- Move ambiguous results to durable `verification_pending`, never to succeeded or failed by assumption.
- Reconciliation repeats create with the same key inside a 23-hour window and validates the returned object.
- The common HTTP/worker create path checks the durable deadline before every provider POST; at the boundary it atomically fails the payment and cancels the still-open order.
- One payment per order, order-row serialization, and provider-key uniqueness prevent parallel local creation under different command keys.
- Provider-result recovery writes use a separate bounded context after client cancellation and never discard persistence errors.
- PostgreSQL concurrency/boundary tests plus fake-provider E2E force competing requests and an ambiguous response after commit while asserting one provider object.

### T13 - Provider payload or confirmation URL leaks from billing

Risk: credentials, provider response bodies, webhook buyer fields, or confirmation URLs enter logs, traces, metrics, Kafka, or long-lived diagnostics.

Mitigation:

- YooKassa adapter returns classified errors without wrapping credential-bearing URLs or raw bodies.
- Webhook ingress persists only event type, provider object ID, and observed status.
- Confirmation URL is excluded from events and forbidden from structured logs, metrics, and traces.
- Confirmation URL is cleared in the same transaction that makes a payment succeeded, canceled, or failed.
- Provider responses are body-limited; error and redaction tests include synthetic secrets.

## Initial Security Requirements

- TLS everywhere.
- mTLS for internal service-to-service APIs.
- mTLS between provisioning-service and node-agent.
- Private WireGuard management network.
- Separate DB credentials per service.
- Kafka ACLs per service in production.
- Secrets via secret mounts or secret manager, never Git or image.
- Request body limits per public endpoint.
- Telegram webhook ingress rate limit before business processing.
- YooKassa webhook edge rate limit and current official source-IP allowlist before public production exposure; authenticated provider GET remains mandatory.
- `Idempotency-Key` required for payment/order/token issue/rotation commands.
- Manual commit after successful Kafka processing and inbox persistence.
- DLQ is actionable with alert, reason, replay tooling, and runbook.
- No production private keys in CI logs or artifacts.

## Open Threat Model Items

- Exact production jurisdiction and legal obligations.
- Real YooKassa receipt/tax flow.
- Admin certificate issuance, rotation, and revocation process.
- Concrete DNS/TLS provider and edge log redaction configuration.
- Xray-core pinned version and config validation command.
- VPS provider AUP and lawful request process.
- Backup/restore key management.
