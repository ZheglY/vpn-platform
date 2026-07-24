# Architecture

## Purpose

The platform sells prepaid VPN subscription periods through a Telegram bot, confirms payment through YooKassa sandbox in the first version, provisions VLESS + REALITY credentials to Xray-core nodes through a control plane and node-agent, then issues a secret Happ-compatible subscription URL through a one-time synchronous flow.

Happ is only the user client. It is not the VPN provider. User VPN traffic must pass through owned or rented Xray-core nodes.

## Current Scope

Stage 7 adds durable user notifications and bounded administrator operations. `notification-service` owns Kafka inbox/cursors, causal delivery stream/sequence barriers, business deduplication, typed template versions, durable delivery jobs, leases, retries, and sanitized dead-letter metadata in its own PostgreSQL database. `admin-service` owns principals, role grants, fenced owner attempts, recoverable unknown outcomes, idempotent action requests, and append-only audit in another database; it reaches fixed owning-service APIs over mTLS and has no credentials for their databases. Stage 8 is in progress; its first slice adds a shared privacy-safe HTTP RED contract and a local protected Prometheus/Grafana profile.

The current environment is portfolio/sandbox only. It does not use real YooKassa credentials, issue receipts, initiate provider refunds, enroll production VPS hosts, or carry real user traffic. Normal Compose smoke keeps a contract-injected provisioning result for the Stage 5 delivery path. The `vpn` profile runs the full Access command, Kafka, Provisioning, two-node Xray, outcome, Happ delivery, and revoke path with real local VLESS + REALITY traffic. `stage7-smoke` extends that path with fake Telegram failures and local development administrator identities. Production certificate issuance, key custody, alert routing, and VPS deployment remain Stage 8.

## Product Decisions Already Accepted

- Portfolio/sandbox project only. No real sales until legal review.
- One prepaid 30-day MVP plan seeded by configuration.
- No hard traffic cap in v1.
- One selected region, one primary node, and one failover node.
- Grace period is 24 hours.
- Buying while active extends from `current_period_end`; buying after expiry starts from confirmed payment time.
- Only full operator-initiated refunds in v1.
- Confirmed full refund is tied to the specific payment-funded subscription period; it revokes current access when recalculation creates a gap, while historical or future-only refunds preserve currently valid access.
- Happ HWID and device limit are not used in v1.
- Only aggregate traffic and health data may be collected.
- Management plane uses private WireGuard networking and mTLS.
- Admin surface is protected CLI plus internal admin API with RBAC and audit. No web-admin in v1.
- Capacity reserve is at least 20%; no new users are assigned when a node reaches 80% of configured capacity.

See `docs/product-decisions.md` for the full decision log.

## System Context

```mermaid
flowchart LR
    customer["Customer in Telegram"]
    happ["Happ app"]
    operator["Operator/Admin"]
    tg["Telegram Bot API"]
    yk["YooKassa sandbox"]
    platform["VPN subscription platform"]
    node["VPN nodes with Xray-core"]

    customer --> tg
    tg --> platform
    platform --> yk
    platform --> tg
    customer --> happ
    happ --> platform
    platform --> node
    operator --> platform
    happ --> node
```

## Containers and Services

| Component | Responsibility | Owns durable data |
|---|---|---|
| `edge` | TLS, routing, coarse rate limits, security headers, access log redaction | No |
| `telegram-bot` | Telegram webhook ingestion, UX, FSM, user commands | No durable domain state |
| `identity-service` | Telegram identity, users, consent, blocking, roles | Yes |
| `catalog-service` | Plans, prices, locations, published catalog | Yes |
| `billing-service` | Orders, YooKassa sandbox payments, refunds, webhook inbox, payment outbox | Yes |
| `subscription-service` | Entitlement lifecycle, periods, expiry, revocation | Yes |
| `access-service` | Subscription URL tokens, Happ document rendering, credential lifecycle | Yes |
| `provisioning-service` | Nodes, allocations, desired state, operations, health snapshots | Yes |
| `notification-service` | Durable Telegram notifications and retry | Yes |
| `admin-service` | Administrator identity mapping, RBAC, typed action orchestration, and append-only audit | Yes |
| `node-agent` | Applies Xray desired state on a VPN node, validates config, reports aggregate health | Local last-known-good and operation journal |
| `Xray-core` | Data-plane VPN traffic processing | Local config/state only |

## Corrected Repository Layout

The original specification showed a root `cmd/` next to `services/<service>/internal/`. That conflicts with Go `internal` import rules because root command packages cannot import sibling service internals.

Use this layout instead:

```text
.
├── AGENTS.md
├── PLANS.md
├── go.mod
├── internal/
│   └── platform/
│       ├── config/
│       ├── logging/
│       ├── httpserver/
│       ├── postgres/
│       ├── kafka/
│       ├── outbox/
│       ├── observability/
│       └── cryptoutil/
├── services/
│   ├── identity/
│   │   ├── cmd/
│   │   │   └── identity-service/
│   │   │       └── main.go
│   │   ├── internal/
│   │   │   ├── domain/
│   │   │   ├── application/
│   │   │   ├── repository/
│   │   │   └── transport/
│   │   ├── migrations/
│   │   ├── openapi/
│   │   └── Dockerfile
│   └── ...
├── contracts/
│   ├── http/
│   └── events/
├── deploy/
├── docs/
└── scripts/
```

For the first version use one root `go.mod`. Service boundaries are enforced by folder ownership, Go `internal`, architecture tests, CI checks, and code review. `go.work` and per-service modules are deferred until there is a concrete need.

Root `internal/platform` may contain technical helpers only. It must not define `User`, `Payment`, `Subscription`, `Credential`, plan models, or other cross-service domain entities.

## Communication Model

| Interaction | Mechanism | Rule |
|---|---|---|
| Immediate command/query | HTTP/JSON over `net/http` | Use when caller needs a direct response |
| Domain fact | Kafka event with outbox/inbox | Past-tense event, at-least-once, idempotent consumer |
| Async command | Kafka command message | Explicit owner, timeout, retry, DLQ, terminal failure |
| Credential material retrieval | HTTP/mTLS | Provisioning-service fetches minimum material from access-service by credential ID |
| Node desired state | HTTPS/mTLS over private WireGuard network | Idempotent operation ID and desired revision |
| Notification delivery | HTTP/mTLS from notification-service to telegram-bot | Stable delivery ID; typed send only; Telegram token remains in telegram-bot |
| Administrator read/action | Admin mTLS to admin-service, then fixed owner HTTP/mTLS | Explicit permission and typed owner endpoint; no cross-service SQL |
| Public subscription document | `GET /s/{token}` on separate hostname | Bearer token in path, redacted everywhere, no-store |

Kafka never replaces HTTP when an immediate answer is required. PostgreSQL commit and Kafka publish are connected through transactional outbox, not a distributed transaction.

## Kafka Naming

- Domain facts use past tense: `billing.payment.succeeded.v1`.
- Async commands use imperative command naming: `access.provision.request.v1`.
- Command result facts use past tense: `access.provision.succeeded.v1`, `access.provision.failed.v1`.
- Revoke result facts are explicit: `access.revoke.succeeded.v1`, `access.revoke.failed.v1`.
- User-delivery readiness is separate: `access.ready.v1`.
- Every message uses the common envelope with `event_id`, `schema_version`, `producer`, `correlation_id`, `aggregate_type`, `aggregate_id`, and a stable `partition_key`.
- Events and commands are documented in AsyncAPI and JSON Schema.
- Partition keys are exact per message family: billing/subscription/user-delivery events use `user:{user_id}`, credential operation commands/results use `credential:{credential_id}`.

## Data Ownership

Each service owns its database or schema, DB user, migrations, and data model. A local Compose environment may use one PostgreSQL instance, but services must use separate logical databases or schemas and separate credentials. Cross-schema joins and direct cross-service SQL are forbidden.

Redis is only for ephemeral FSM, cache, rate limits, and short locks. It is never a source of truth.

## Primary User Flows

### Registration

```mermaid
sequenceDiagram
    participant Telegram
    participant Bot as telegram-bot
    participant Identity as identity-service
    Telegram->>Bot: webhook update with secret token
    Bot->>Bot: dedupe update_id
    Bot->>Identity: PUT /internal/v1/telegram-users/{telegram_id}
    Identity-->>Bot: user identity
    Bot-->>Telegram: menu
```

### Purchase and Payment Confirmation

```mermaid
sequenceDiagram
    participant User
    participant Bot as telegram-bot
    participant Catalog as catalog-service
    participant Billing as billing-service
    participant Yoo as YooKassa sandbox
    participant Kafka
    participant Sub as subscription-service
    User->>Bot: choose plan
    Bot->>Catalog: GET published plans
    Bot->>Billing: POST order/payment with Idempotency-Key
    Billing->>Yoo: create payment with provider Idempotence-Key
    Yoo-->>Billing: confirmation URL
    Billing-->>Bot: confirmation URL
    Yoo->>Billing: webhook
    Billing->>Billing: store normalized inbox/dedupe record
    Billing-->>Yoo: fast 2xx
    Billing->>Yoo: worker GET payment for verification
    Billing->>Billing: state transition + outbox in one DB transaction
    Billing->>Kafka: billing.payment.succeeded.v1
    Kafka->>Sub: payment fact with user partition key
    Sub->>Billing: GET immutable order snapshot over mTLS
    Sub->>Sub: inbox + period + entitlement + outbox transaction
    Sub->>Kafka: subscription.activated.v1 or subscription.extended.v1
```

The existing payment event remains v1-compatible and intentionally carries no mutable catalog lookup. Subscription validates event identity, plan, and money against the immutable Billing order snapshot before committing the Kafka offset. Duplicate event IDs and duplicate source payment IDs cannot create another period.

### Entitlement Time Lifecycle

- A purchase while active or in grace appends a complete period at the current entitlement end.
- A purchase after expiry or revocation starts at provider-confirmed `paid_at`.
- At `current_period_end`, active becomes grace. At `grace_ends_at`, active/grace becomes expired and emits one event.
- Scheduler rows use recoverable leases and `FOR UPDATE SKIP LOCKED`; exact boundaries use `now >= boundary` semantics.
- A confirmed full refund marks only its immutable source period and recalculates remaining paid periods. It emits terminal revoke when nothing remains, refund-gap revoke when only a future period remains, and no revoke for historical or future-only changes while current access remains valid.
- Refund-before-payment is retained as normalized pending inbox work and reconciled after the source period arrives.

### Access and Provisioning

```mermaid
sequenceDiagram
    participant Kafka
    participant Sub as subscription-service
    participant Access as access-service
    participant Prov as provisioning-service
    participant Agent as node-agent
    participant Xray
    Kafka->>Sub: billing.payment.succeeded.v1
    Sub->>Kafka: subscription.activated.v1 or subscription.extended.v1
    Kafka->>Access: subscription event with aggregate_sequence
    Access->>Access: require previous sequence and unexpired DB-time entitlement
    Access->>Access: create credential only; no subscription token yet
    Access->>Kafka: access.provision.request.v1 with desired_revision sequence
    Kafka->>Prov: next provision command for credential
    Prov->>Access: GET credential material over mTLS
    Access->>Access: append secret-free security audit event
    Access-->>Prov: minimum VLESS material; no REALITY private key
    Prov->>Sub: GET immutable placement over mTLS
    Prov->>Prov: reserve primary and failover generation
    Prov->>Agent: PUT credential over mTLS to both assigned nodes
    Agent->>Xray: validate, atomic apply, reload
    Agent-->>Prov: operation result
    Prov->>Kafka: sequenced provision outcome with full assignment proof
    Kafka->>Access: next outcome across all four result topics
    Access->>Kafka: access.ready.v1 only if entitlement still valid
```

Subscription entitlement can be `active` while VPN access is still pending. VPN access becomes `ready` only after the primary node successfully applies the credential. If failover is not ready, provisioning is `degraded`, access may be issued through the one-time link flow, and the failure must be visible in metrics, admin CLI, and alerting. If the primary node fails, access must not become `ready`. Provision success keeps the complete two-node assignment proof separate from usable Happ endpoints so a later revoke must remove even a failed failover assignment.

### One-Time Subscription URL Issuance

```mermaid
sequenceDiagram
    participant Kafka
    participant Notify as notification-service
    participant Bot as telegram-bot
    participant User
    participant Access as access-service

    Kafka->>Notify: access.ready.v1
    Notify->>Bot: send "VPN ready" command/message
    Bot->>User: VPN ready, button "get link"
    User->>Bot: click get link
    Bot->>Access: POST /internal/v1/subscriptions/{id}/subscription-url/issue over mTLS
    Access->>Access: generate token, store HMAC/hash only
    Access-->>Bot: full URL returned once
    Bot-->>User: sends URL immediately
```

The plaintext subscription token is never created before provisioning and is never sent through Kafka. The bot must not persist or log the returned URL.

### Notification Delivery

Subscription and Access producers own independent positive sequences across their user-facing topics. Notification keeps one cursor per `(producer, aggregate_type, aggregate_id)`, defers a gap without committing it, treats exact replay as a no-op, and rejects a changed reuse. Billing facts remain unsequenced and are deduplicated by event identity plus a business key.

Jobs never persist rendered text or Telegram chat ID. At each attempt Notification resolves the current consent-eligible target from Identity, checks current entitlement in Subscription for access-related messages, checks current Access state for readiness messages, renders an HTML-escaped versioned template, and calls telegram-bot over mTLS. PostgreSQL leases and bounded retries are durable; Redis only guards the stable delivery ID immediately around the Telegram call. Telegram can accept a message while its HTTP response is lost, so user-visible delivery is at-least-once and a duplicate remains possible in that ambiguous case.

### Administrator Operations

Admin certificates use exactly one verified URI `spiffe://<trust-domain>/ns/<environment>/admin/<principal>`. Admin-service maps that URI to local role grants and checks a distinct permission on every endpoint. A valid non-admin platform certificate receives `403`; missing verified client identity is unauthenticated.

Read paths proxy only support-safe owner DTOs from configured fixed URLs. Mutations are limited to notification retry, subscription revoke, and higher-revision Access recovery. Admin records accepted intent, fenced attempts, unknown outcomes, and final confirmation as append-only audit. Timeout/reset/`5xx`/invalid `2xx` remains recoverable and exact replay uses the same action ID, correlation ID, and owner idempotency key; only definitive rejection is terminal failure. Changed input conflicts. No payment mutation, arbitrary URL, SQL, shell, Kafka payload, role mutation, subscription URL, VLESS UUID, or node configuration operation exists.

### Revoke Lifecycle

```mermaid
sequenceDiagram
    participant Sub as subscription-service
    participant Kafka
    participant Access as access-service
    participant Prov as provisioning-service
    participant Agent as node-agent

    Sub->>Kafka: subscription.revoked.v1
    Kafka->>Access: entitlement revoked
    Access->>Access: token blocked, access state revoking
    Access->>Kafka: access.revoke.request.v1
    Kafka->>Prov: revoke command
    Prov->>Agent: DELETE credential over mTLS
    Agent-->>Prov: removed or redacted failure
    Prov->>Kafka: exact allocation-revision removal proof or failed.v1
    Kafka->>Access: revoke result
    Access->>Access: mark revoked only after all assigned nodes confirm removal
```

Reconciliation claims due allocations with PostgreSQL leases and `FOR UPDATE SKIP LOCKED`, then compares provisioning desired state with node actual state. Claims are bounded, safely shared by replicas, rescheduled independently, and cannot starve rows beyond the first batch. Terminal revoke failure creates alert and operator escalation.

## Subscription Endpoint

`GET /s/{token}` is served from a dedicated subscription hostname by `access-service`.

Implemented access and Stage 6 provisioning behavior:

- Unknown, expired, and revoked tokens return the same external response.
- Empty and nested malformed `/s` paths return that same response without redirecting to a distinguishable document.
- Response does not reveal user or subscription existence.
- Use `404 Not Found`, `Content-Type: text/plain; charset=utf-8`, `Cache-Control: no-store`.
- Response body is generic and contains no token, user ID, credential ID, or reason.
- Edge, reverse proxy, traces, metrics, and error reporting must not record the path segment.
- Happ standard headers are `profile-title`, `profile-update-interval`, `subscription-userinfo`, and optional `support-url`.
- The body contains one deterministic VLESS + REALITY share URI per validated primary/failover endpoint snapshot.
- Provisioning success is the only event that stores an endpoint snapshot and moves access to `active` or `degraded`.
- Access stores the complete assigned-node snapshot separately from usable endpoint snapshots and validates revoke against the complete assignment.
- A delayed physical provisioning success after entitlement expiry stores the allocation only to initiate revoke and never makes access ready.
- Subscription owns immutable period placement; Provisioning atomically selects one primary and one failover below the 80% threshold.
- Provisioning outcomes have one credential-owned sequence across all four result topics; Access durably defers gaps and rejects sequence collisions.
- A higher desired revision creates and fences a new allocation generation, preserving existing capacity reservations and reserving revoked allocations exactly once on reactivation.
- Node-agent applies revisioned present/absent state. Once validated mutation starts, an independent bounded context completes candidate startup or last-known-good restoration even if the caller disconnects.

## Security Boundaries

| Boundary | Required control |
|---|---|
| Public Telegram webhook | TLS, Telegram secret token header, body limit, dedupe, no raw payload logs |
| Public YooKassa webhook | TLS, body limit, inbox dedupe, fast durable store, async provider verification before fulfillment |
| Public subscription URL | Dedicated hostname, no-store, token hash lookup, path redaction, rate limit |
| Internal service APIs | mTLS service identity from platform CA; private network is defense in depth |
| Provisioning material API | mTLS restricted to provisioning-service; audit; no REALITY private key |
| Admin operations | CLI/internal API over admin mTLS, RBAC, deny-by-default, audit log |
| Management plane | Private WireGuard network, mTLS, firewall default deny |
| Node agent | Provisioning-only mTLS, exact server SPIFFE pin, allowlisted fields, no shell command execution, pinned Xray config test before reload |

## HTTP Authentication Model

- Internal service APIs require mTLS and per-endpoint service identity allowlists.
- Admin APIs require admin mTLS identity, RBAC, and audit.
- Telegram webhook requires `X-Telegram-Bot-Api-Secret-Token`.
- YooKassa webhook is public ingress; trust is established by storing the notification and verifying provider state through YooKassa API before fulfillment.
- Published plans are public and use `security: []`.
- `GET /s/{token}` is public bearer-by-URL for Happ compatibility and uses path redaction everywhere.

## Observability

Metrics must be useful without exposing secrets or high-cardinality identifiers. Labels must not contain user IDs, payment IDs, subscription tokens, VLESS UUIDs, raw paths, destination IPs, or Telegram payload data.

The implemented HTTP baseline exports request count, duration, and in-flight requests. Labels contain only service, an allowlisted method, the registered `net/http` route pattern, and response status class. Unknown values collapse to bounded labels. In particular, `/s/<bearer>` is represented only as `GET /s/{token}`. Every service, including node-agent, protects `/metrics` with the dedicated `observability` mTLS identity; telegram-bot serves metrics only on its internal TLS listener.

The local `obs` profile uses integrity-pinned Prometheus and Grafana security rebuilds, scrapes explicit 8-target or 11-target inventories over TLS 1.3 mTLS, evaluates target/down/absence/error/latency alerts linked to runbooks, and provisions a read-only Grafana overview. Host keys pass through a network-isolated allowlist init into per-owner named volumes; a Linux UID verifier gates service startup. Both observability images are HIGH/CRITICAL scan gates. Local Prometheus retains 15 days and has no remote write. Production alert routing, authentication, storage, Kafka/DB/domain metrics, tracing, and log collection remain later Stage 8 slices.

Initial SLOs come from the specification:

- Control API availability: 99.9% monthly.
- Subscription endpoint availability: 99.95% monthly.
- p95 simple internal HTTP requests: under 300 ms without external provider.
- p95 subscription document: under 200 ms with warm DB.
- 99% of successful payment events start provisioning within 60 seconds.

## Deferred Production Risks

The architecture intentionally keeps several decisions as future approval points:

- Production jurisdiction and legal compliance.
- Real YooKassa receipts, tax fields, and buyer data.
- Actual domain, DNS/TLS, and VPS provider choices.
- Production admin credential issuance and rotation procedure.
