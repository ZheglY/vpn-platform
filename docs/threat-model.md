# Initial Threat Model

Method: STRIDE-inspired model updated for Stage 7. It must be reviewed again before production certificate issuance, connecting production VPS, or using real payment credentials.

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
| Admin private keys, principals, roles, and audit | Allows bounded support reads, notification retry, subscription revoke, and provisioning recovery |
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
| Notification to telegram-bot | chat ID leakage, arbitrary Telegram proxying, duplicate/forged delivery |

## STRIDE Summary

| Category | Key threats | Required mitigation |
|---|---|---|
| Spoofing | Forged Telegram/YooKassa webhooks, fake node-agent, fake internal service | Secret token checks, durable provider verification, mTLS, service identity allowlists, firewall default deny |
| Tampering | Payment amount/status changes, Kafka payload changes, Xray config corruption | Provider verification, JSON Schema, outbox/inbox, allowed state machines, Xray config test and atomic swap |
| Repudiation | Operator denies refund/revoke/grant, webhook processing unclear | Append-only audit, operation IDs, correlation IDs, immutable ledgers |
| Information disclosure | Subscription token in logs, VLESS UUID in traces, raw webhook/Telegram payloads, browsing data | Redaction, no raw payload logs, no destination/DNS history, no path logging on subscription hostname |
| Denial of service | Oversized requests, repeated payment clicks, webhook floods, Kafka lag, node overload | Body limits, rate limits, idempotency, bounded retries, DLQ, capacity reserve |
| Elevation of privilege | Admin RBAC bypass, node-agent shell execution, cross-service DB access | Deny-by-default RBAC, no shell command execution, least-privilege DB users, architecture checks |

## Stage 7 Threat Scenarios

| Threat | Mitigation and residual risk |
|---|---|
| Forged admin certificate or service certificate used as admin | TLS 1.3 chain verification plus exactly one environment-bound admin SPIFFE URI; valid non-admin identities receive `403`; enabled principal lookup and endpoint permission are still required. Production issuance/revocation remains Stage 8. |
| Stolen CLI private key | Key is external to binary/repository; least-privilege role and exact principal audit constrain impact. Disable principal and revoke certificate. Hardware-backed custody is not implemented locally. |
| Privilege escalation, role confusion, or wildcard operation | Default-deny built-in role matrix, no superadmin/wildcard, explicit permission per route, runtime cannot grant roles, no actor/role headers. |
| Arbitrary target ID or admin mutation replay | UUID/type validation, fixed owner route, target in canonical request hash, reason and idempotency key required, accepted audit before execution, exact owner action ID on recovery, changed replay conflicts. |
| Admin-service SSRF or arbitrary internal URL | All upstream base URLs are startup-validated fixed HTTPS configuration; requests contain only typed path identifiers; no URL field exists in admin requests. |
| Owning-service bypass | Admin has no foreign DB credentials and cannot emit arbitrary Kafka, SQL, shell, payment, node, or Xray commands. Every mutation runs in Notification, Subscription, or Access. |
| Audit tampering | Append-only table triggers and least-privilege runtime grants reject update/delete/truncate; accepted and completion records use PostgreSQL time and immutable snapshots. Database owner compromise remains a platform incident. |
| Secret-bearing owner response or reason | Server and CLI recursively reject forbidden JSON keys/credential URLs; owner DTOs omit bearer/VPN/payment provider data; audit stores only bounded fields and hashes. |
| Duplicated notification | Durable source inbox, source coordinates/hash, business unique key, cursor, job lease, and ephemeral Telegram delivery guard. A Telegram-accepted request with lost response can still visibly duplicate and is documented. |
| Forged notification event | Exact topic/event/producer/aggregate rules, schema version, UUIDs, user partition key, source hash, JSON Schema examples, and durable conflict/DLQ behavior. Kafka ACL hardening remains Stage 8. |
| Cross-topic reordering or readiness after revoke/expiry | Producer-owned cursors reject gaps/collisions without commit; just-in-time Subscription entitlement and Access credential checks suppress stale readiness even while Access is converging. Receive time is never an ordering authority. |
| Telegram HTML injection or oversized output | Compiled typed templates, HTML escaping of every dynamic value, fixed parse mode, 4096-rune bound, no runtime templates or arbitrary Telegram methods. |
| Telegram chat ID or message leakage | Target is resolved just in time and held in memory only; jobs omit chat ID/rendered text; logs, metrics, traces, audit, and DLQ use safe IDs/categories only. telegram-bot Redis guard is ephemeral. |
| DLQ replay abuse | Notification admin API is read-only for DLQ metadata; no generic replay/publisher exists. Unchanged poison is not replayed; a corrected fact must come from its owning producer. |
| High-cardinality metrics | Metrics are limited to service, operation/type, status class, and bounded reason; no user, target, event, chat, correlation, or URL labels. |
| Compromised notification-service | It can request typed Telegram sends and read consent targets but cannot obtain Bot API token, subscription URL, VPN credential, or owner DB access. Fixed mTLS allowlists constrain calls. |
| Compromised telegram-bot | It owns the Bot API token and receives transient typed messages/chat IDs, but cannot mutate Notification jobs or use arbitrary internal owner APIs. Token compromise still requires rotation and incident response. |

## Stage 8 Observability Threat Scenarios

| Threat | Mitigation and residual risk |
|---|---|
| Subscription bearer or identifier enters metrics | HTTP labels use only reviewed `net/http` route patterns, bounded methods, service, and status class. Tests inject a synthetic secret path and assert it is absent. Future domain metrics still require the ADR 0013 allowlist. |
| Metrics endpoint exposes topology or operational state | Every service requires the dedicated environment-bound `observability` SPIFFE identity. Telegram metrics moved off public HTTP. Local Prometheus/Grafana ports bind only to loopback. Production network and Grafana authentication remain pending. |
| Monitoring certificate becomes a general service credential | The generated certificate is client-only and application allowlists grant it only `/metrics`; it is not accepted by business endpoints. Production issuance, expiry alerting, and revocation remain pending. |
| Host key ownership makes non-root Linux containers fail or encourages world-readable permissions | Runtime services never bind-mount host keys. A network-isolated allowlist init writes per-owner named volumes with `0400`/`0440`; capability-free UID 65532 verification gates startup on native Linux. The nginx camouflage key remains isolated and root-owned for its root master. Production secret injection and rotation remain separate Stage 8 work. |
| Metric cardinality exhaustion | Methods and status classes are bounded, unmatched paths collapse to one value, and route patterns are source-defined. User, payment, subscription, credential, event, request, correlation, and raw URL values are forbidden labels. |
| A disappeared discovery target produces no `up == 0` series | Profile-specific static inventories and count/absence alerts encode 8 baseline or 11 VPN targets. Rule tests cover both scrape failure and full series absence. Intentional topology changes require a reviewed inventory update. |
| Dashboard or alert leaks sensitive data | Provisioned queries use only aggregate series and bounded labels. The runbook forbids raw paths/payloads and production remote write is absent. A future writable production Grafana still requires RBAC and query review. |
| Observability image ships vulnerable unused code | Prometheus and Grafana builds pin source/runtime integrity, verify modules, update the reviewed vulnerable gRPC dependency, and require zero HIGH/CRITICAL image scans. Grafana omits unused bundled Zipkin/Elasticsearch executables and includes only the Tempo protobuf DTO tree needed by its server graph. |

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
- Use PostgreSQL transaction time for entitlement decisions so host clock skew cannot create temporary access after expiry.
- Serialize outbox claims by subscription aggregate sequence so concurrent publishers cannot reverse activation and revocation.

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
- User-scoped PostgreSQL advisory transaction lock and immutable period constraints.
- Event ID inbox dedupe plus unique source payment/order keys.
- Billing order snapshot validation over an allowlisted mTLS endpoint before offset commit.
- Poison records persist only topic coordinates, payload SHA-256, and bounded reason code; raw payload is not retained or logged.
- Contract, concurrency, delayed delivery, refund-ordering, permanent-conflict DLQ, outbox sequence, and exact time-boundary tests.

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

### T14 - Subscription token leaks through URL paths or idempotency replay

Risk: Happ requires a bearer token in the URL path. Access logs, proxies, traces, referrers, or replayable idempotency responses could retain it.

Mitigation:

- Access-service stores only HMAC-SHA-256 under a dedicated external key; the plaintext token is never persisted.
- Request middleware records the route template `GET /s/{token}`, not the raw path; a security test fails if the token appears.
- Success and generic 404 responses use `Cache-Control: no-store`, `Pragma: no-cache`, `Referrer-Policy: no-referrer`, and `X-Content-Type-Options: nosniff`.
- Completed issue/rotate idempotency keys return a safe 409 instead of replaying a retained secret response.
- Unknown, malformed, expired, and revoked tokens are externally indistinguishable.
- Production edge configuration must disable or redact URI access logs before exposing the subscription hostname.
- An application-level Redis limiter stores only HMAC-derived ephemeral keys with TTL and fails closed; production edge rate limiting remains an additional control.

### T15 - Database theft reveals VLESS access or enables offline token recovery

Risk: a database snapshot contains reusable VPN credentials or allows token lookup without the application secret.

Mitigation:

- VLESS UUIDs use versioned AES-256-GCM envelope encryption under a key supplied outside PostgreSQL.
- Token lookup uses a separate 32-byte HMAC key; encryption and HMAC keys must differ.
- Tokens contain 256 random bits, preventing practical guessing even if lookup HMACs leak.
- Provisioning commands and readiness events contain no UUID, token, URL, or REALITY private key.
- Provisioning material is returned only to the verified `provisioning-service` SPIFFE identity over mTLS.
- Public REALITY endpoint parameters are stored as a client snapshot; the REALITY private key remains node-local.

### T16 - Cross-topic lifecycle reordering restores stale access

Risk: activation, extension, expiry, and revoke use different Kafka topics. A terminal event can arrive before an earlier activation, be applied as a no-op, and then allow the stale activation to provision expired access. A delayed refund-gap revoke can similarly override a newer period.

Mitigation:

- Subscription-service places its monotonic subscription-owned `aggregate_sequence` in every lifecycle envelope and outbox row.
- Access persists `last_applied_sequence` with the inbox and state effect in one transaction.
- A gap is never acknowledged or classified as poison. The consumer pauses that source partition, continues other lifecycle topics, and retries after the predecessor applies.
- Duplicate events are no-ops; a different event attempting to reuse an applied sequence is a durable conflict.
- Before provisioning, Access requires `grace_ends_at > clock_timestamp()` in PostgreSQL.
- Access outbox uses a separate credential-owned sequence and lower-sequence claim barrier, not transaction timestamps.

### T17 - Delayed provisioning or partial revoke creates false terminal state

Risk: a provisioning success delayed beyond entitlement expiry can produce a usable profile, or a revoke success can name only some nodes and still make Access report `revoked`.

Mitigation:

- Provisioning success locks current credential/operation state and compares entitlement with PostgreSQL time in the same transaction.
- An elapsed success stores the actual allocation only to atomically start a higher-revision revoke; if revoke already started, a late success can only advance the pending revoke's allocation snapshot and cannot overwrite a newer allocation. It emits no readiness event.
- Revoke operations capture allocation revision. Success carries desired revision, allocation revision, an explicit all-removed assertion, and unique node IDs.
- Provision success carries the complete assigned-node set separately from usable endpoint snapshots, including an unavailable failover in degraded mode.
- Access requires exact set equality with its revisioned assignment snapshot. Empty proof is accepted only for zero allocations; partial or duplicate proof cannot become terminal success.
- Access status derives expiry from PostgreSQL time and cannot report ready after the entitlement boundary.
- PostgreSQL tests cover delayed success, partial proof, zero-allocation proof, and stale operation results.

### T18 - Forged node or provisioning identity mutates VPN access

Risk: an attacker impersonates provisioning-service or redirects it to a different node-agent and applies credentials.

Mitigation:

- Node-agent requires TLS 1.3 client certificates and authorizes only the verified `provisioning-service` SPIFFE URI.
- Provisioning verifies the node server certificate through the platform CA and requires the exact SPIFFE URI stored with that node.
- Container health has a separate per-node client identity authorized only for health routes.
- Node management runs on an internal Compose network; production exposure requires WireGuard and firewall policy in Stage 8.

### T19 - Desired-state replay or reordering restores revoked access

Risk: a delayed provision command or reused operation ID re-adds a credential after revoke.

Mitigation:

- Provision and revoke command envelopes use `desired_revision` as their shared credential sequence; Access readiness events use only the separate outbox delivery sequence and cannot create a hidden command gap.
- Provision and revoke topics share one PostgreSQL credential sequence cursor, and operation claims wait for earlier non-terminal commands of that credential.
- Gaps are deferred without commit; reused operation IDs, stale sequences, and stale revisions become sanitized durable conflicts.
- A higher revision atomically rebinds the allocation generation and fences all writes by desired operation, revision, state, and allocation revision. Stale workers cannot publish or mutate the new generation.
- Node-agent journals operation ID with request SHA-256, rejects operation collisions and lower revisions, and persists absent tombstones.
- Reconciliation refuses to overwrite a node revision newer than the control-plane desired revision.

### T20 - Node config failure interrupts existing users

Risk: an invalid candidate or failed process restart replaces the working Xray config.

Mitigation:

- Node-agent renders allowlisted fields and invokes a fixed pinned Xray binary without a shell.
- Candidate files and node state use owner-only permissions; REALITY private keys are read from separate secret mounts.
- `xray run -test -config` must succeed before swap.
- Validation observes request cancellation, but stop/install/start and rollback run under an independent bounded consistency context once mutation begins.
- Failed startup or cancellation during reload restores and restarts last-known-good before return; tests cover validation, one-shot runtime failure, and cancellation during stop.
- Xray access logging is disabled and process diagnostics are discarded because they may contain configuration details.

### T21 - Poison Kafka payload becomes a second secret store

Risk: malformed command payloads containing credentials are copied to DLQ, logs, or tickets.

Mitigation:

- Durable dead-letter state stores only source coordinates, SHA-256, and bounded reason code.
- Versioned DLQ events contain the same sanitized fields and never the raw record.
- Replay accepts only provision/revoke source topics, re-reads the original Kafka offset, verifies SHA-256, and republishes without outputting the value.

### T22 - Provisioning outcomes reorder across Kafka topics

Risk: provision and revoke results use four topics. A later result can arrive first, causing Access to reject a valid revoke or apply stale endpoint state.

Mitigation:

- Provisioning allocates one positive outcome sequence per credential across all four topics in the same transaction as terminal state and outbox insertion.
- The outbox cannot claim a later outcome while a lower credential sequence is unpublished.
- Access persists one outcome cursor across all four topics; gaps remain unacknowledged and retryable while exact duplicates are no-ops.
- Reuse of an event ID or applied sequence for different content is a durable conflict, and non-positive sequences fail contract validation.

### T23 - Reconciliation starvation leaves control-plane drift unrepaired

Risk: repeatedly selecting the oldest fixed batch can prevent later allocations from ever being checked, while multiple replicas can duplicate work.

Mitigation:

- PostgreSQL claims due rows with a durable claim ID, lease expiry, bounded batch, and `FOR UPDATE SKIP LOCKED`.
- Each allocation is independently rescheduled after success or failure; one error does not block the remainder of the batch.
- Expired claims become eligible after process failure, and integration tests use more allocations than one batch.

### T24 - Retried Telegram notification overtakes a terminal fact

Risk: an extension or payment-confirmed job receives `429`, a later revoke/refund message is delivered, and the old retry then tells the user that service is active.

Mitigation:

- Every job stores source aggregate metadata plus a causal delivery stream and positive delivery sequence.
- Event insertion serializes the delivery stream with a PostgreSQL transaction advisory lock, so concurrent Kafka topics cannot race around a terminal barrier.
- Claims wait for earlier nonterminal jobs in the same stream. A terminal fact suppresses older pending/retry jobs; a processing predecessor either finishes before the terminal message or becomes suppressed when its failed attempt completes.
- Permanently failed predecessors are terminal and cannot block revocation forever. Manual retry is rejected after a successor exists.
- Extension, grace, expiry, and revoke jobs verify the exact current Subscription state before sending.
- Full refund is the terminal payment-stream fact; it suppresses payment confirmation whether the payment fact was already retrying or arrives after the refund.

### T25 - Admin timeout records a false terminal failure

Risk: an owner commits revoke/recovery, its response is lost, and admin-service records `failed`, preventing the same key from recovering the real result.

Mitigation:

- Owner attempts use a durable claim ID, lease, attempt counter, and stable `admin-<action_id>` idempotency key.
- Only bounded `4xx` owner rejections are definitive. Timeout, reset, `5xx`, invalid/lost `2xx`, and semantic response failure become nonterminal `outcome_unknown`.
- Exact admin replay retains action ID and correlation ID, obtains a new fenced attempt, and calls the owner with the same owner key.
- The owner returns its idempotent replay, so admin-service can confirm `succeeded` without a second business mutation.
- Append-only audit records accepted, attempted, retrying, unknown, and confirmed outcomes instead of rewriting history or claiming a false failure.

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
- Manual commit only after atomic inbox/state/outbox processing or durable poison-record metadata.
- A transient dependency/database failure rewinds the partition offset and retries with backoff; it is never converted into poison.
- Consumer dead-letter metadata and outbox recovery have an actionable Stage 4 runbook; production alert routing and replay CLI remain Stage 8 work.
- No production private keys in CI logs or artifacts.

## Open Threat Model Items

- Exact production jurisdiction and legal obligations.
- Real YooKassa receipt/tax flow.
- Admin certificate issuance, rotation, and revocation process.
- Concrete DNS/TLS provider and edge log redaction configuration.
- VPS provider AUP and lawful request process.
- Backup/restore key management.
