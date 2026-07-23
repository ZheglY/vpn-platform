# ADR 0026: Stage 7 admin mTLS, RBAC, actions, and audit

- Status: Accepted for Stage 7 implementation
- Date: 2026-07-19
- Owners: admin-service and each action-owning service
- Security impact: Critical
- Contract impact: admin API/CLI and allowlisted owner APIs
- Resolves: OQ-005 and OQ-011 for the sandbox Stage 7 scope

## Context

Operators need support-safe reads and a few recoverable mutations. A web admin, shared database access, arbitrary SQL/Kafka/shell access, and a universal command envelope would turn admin-service into a bypass around bounded contexts. A valid platform certificate alone is not sufficient authorization, and an actor supplied in an HTTP header or body is forgeable.

## Decision

1. Stage 7 uses short-lived mTLS administrator certificates. The verified leaf certificate must contain exactly one accepted URI of the form `spiffe://<trust-domain>/ns/<environment>/admin/<principal>`. Admin-service derives the actor only from `VerifiedChains[0][0].URIs` and looks up the full URI in its own enabled-principal table.
2. No web-admin or password login is implemented. Private keys remain external files referenced by admin-cli flags/environment and are never embedded in a binary, config committed to Git, output, logs, traces, or audit.
   The prior service identity `admin-cli` is removed from owner-service allowlists: the Stage 7 CLI connects only to admin-service with an administrator certificate, and admin-service performs every allowlisted owner call with its own service identity.
3. RBAC is default-deny. There is no `superadmin` or wildcard permission. Built-in roles are:

| Role | Permissions |
|---|---|
| `support_readonly` | support-safe user, consent, subscription, access, provisioning/node, notification, DLQ, health, and audit reads |
| `operations` | support reads plus `notification.retry`, `subscription.revoke`, and `access.provisioning.recover` |
| `security` | security/audit reads and investigation metadata; no business mutation in Stage 7 |
| `finance_readonly` | order/payment status reads only; no refund or payment mutation |

4. Principals and grants are bootstrapped by a validated seed file through a separate one-shot command using migration/bootstrap credentials. Runtime admin-service cannot grant roles. Repository fixtures contain local development SPIFFE IDs only.
5. Admin-service owns action requests, idempotency, role snapshots, and append-only audit. An action key is bound to verified principal, operation, target, canonical request hash, and reason. Exact replay returns the same support-safe action result; changed input conflicts.
6. Every mutation records an accepted audit event before owner execution. Each owner attempt is fenced by a durable claim ID and lease and records `attempted` or `retrying`; only the active claim can record its result. Admin-service calls a fixed configured owner URL over mTLS with a typed endpoint and the stable key `admin-<action_id>`.
7. Owner `400/401/403/404/409` responses are definitive rejections and may complete the action as `failed`. A timeout, connection reset, `5xx`, unreadable/oversized/invalid `2xx`, semantic mismatch, or unsafe successful response has an indeterminate business outcome and becomes recoverable `outcome_unknown`, never terminal failure. Exact admin replay claims another attempt and calls the owner with the original action ID, correlation ID, and owner idempotency key. Owner replay then confirms the terminal result without a second business mutation. A process crash leaves `processing`; only an expired lease permits recovery.
8. Stage 7 mutation allowlist is limited to:

| Action | Owner | Mechanism |
|---|---|---|
| retry an existing safe notification job | notification-service | synchronous typed HTTP |
| revoke a subscription with a bounded reason | subscription-service | synchronous typed HTTP, lifecycle event through owner outbox |
| recover terminal provisioning with a fresh higher revision | access-service | synchronous typed HTTP, normal Access command outbox |

9. URL rotation, suspension, node drain/resume, broad reconciliation, generic DLQ replay, catalog mutation, refunds, manual payment success, order amount changes, role mutation, and manual grants are not exposed in Stage 7. URL rotation in particular is deferred until an accepted user-delivery handoff can guarantee the administrator never receives the bearer URL.
10. Read APIs aggregate only allowlisted DTOs from fixed validated service base URLs. Requests cannot supply an upstream URL. Admin-service never reads another service database and never returns subscription URLs, token hashes, VLESS UUIDs, REALITY private material, ciphertext, Telegram chat IDs, raw webhook/Kafka payloads, or internal response bodies.
11. Audit rows contain actor SPIFFE identity, role/permission snapshot, action, target, reason, safe idempotency hash, request/correlation IDs, `accepted/attempted/retrying/outcome_unknown/succeeded/failed`, bounded error code, and database timestamps. `outcome_unknown` is explicitly nonterminal and does not claim that the owner failed. A database trigger rejects update/delete. The runtime database role receives only `SELECT` and `INSERT` on audit and cannot bypass the trigger; migration/bootstrap uses a separate owner role.

## Consequences

- Stealing a CLI key grants only the permissions assigned to that exact SPIFFE principal; revoking/disabling the principal denies future requests.
- Security and finance roles intentionally remain read-only until an independently approved mutation policy exists.
- Provisioning recovery never resets Stage 6 rows. Access creates the next desired revision and Provisioning creates a fenced generation through the existing ordered command.
- Support may see a temporary `outcome_unknown` or `processing` action and must retry the same admin idempotency key. Creating a new key would lose the recovery binding and is prohibited.
- The sandbox seed mechanism is not a production certificate issuance system. Hardware-backed issuance, SSO, rotation, revocation distribution, and break-glass procedure remain Stage 8/9 work.

## Rejected alternatives

- Trust an actor or role header: rejected because callers can forge it.
- Treat any valid client certificate as admin: rejected because service and unrelated operator certificates would gain privilege.
- Give admin-service cross-service database credentials: rejected because it bypasses ownership, audit, and owner state machines.
- Publish `admin.command` with arbitrary JSON: rejected because it is an unbounded privileged interface.
- Implement a broad superadmin role: rejected because least privilege and reviewable endpoint permission checks are required.
