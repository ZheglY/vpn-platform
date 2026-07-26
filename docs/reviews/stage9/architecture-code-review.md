# Stage 9 Architecture and Code Review

Review date: 2026-07-26
Review baseline: `76f5624e664c556c3ec598055524319eab0af1f3`
Review owner: Architecture
Scope: current repository, local/disposable environments, and release-candidate artifacts
Production decision impact: blocking findings are carried into `go-no-go-checklist.md`

## Result

No P0 or P1 defect was found in the reviewed application baseline. The service
boundaries, durable integration patterns, and security-sensitive state machines
are internally consistent with the accepted ADRs and contract tests.

Two P2 production-readiness findings remain open. They are not defects in the
local Compose profile, but production use is blocked until an approved
infrastructure design supplies the missing isolation and compatibility evidence.
Neither finding has product-owner acceptance as a deferred production risk.

## Method

The review combined:

- ownership mapping against `docs/architecture.md` and ADRs 0003, 0004, 0005,
  0007, 0014, 0015, 0017, 0022, 0024, 0025, and 0026;
- imports, SQL identifiers, domain-package, placeholder, money-type, and
  Dockerfile inventory scans;
- inspection of all migrations, HTTP servers, Kafka producers/consumers,
  outbox/inbox implementations, owner clients, leases, and state transitions;
- OpenAPI, AsyncAPI, and JSON Schema inventory;
- accepted Stage 3-8 regression suites and the Stage 9 verification commands
  indexed in `evidence-index.md`.

Static scans supplement tests; they do not replace transaction, concurrency,
protocol, restore, or real-Xray evidence.

## Ownership and boundaries

| Context | Owns | Synchronous boundary | Asynchronous boundary |
|---|---|---|---|
| Identity | Telegram identity, consent, terms acceptance | internal identity reads | none |
| Catalog | plans, immutable plan snapshots | public plan list, internal snapshot read | none |
| Billing | orders, payments, refunds, provider reconciliation | internal order/payment reads, YooKassa adapter | payment/refund events |
| Subscription | paid periods and entitlement lifecycle | internal current-state/admin revoke | subscription lifecycle events |
| Access | encrypted VPN credential, bearer-token lookup, delivery profile | public subscription render, internal material/admin recovery | access/provisioning commands and outcomes |
| Provisioning | node registry, placement, desired operations, reconciliation | node-agent and owner reads | provisioning results and DLQ notices |
| Node agent | node-local desired/actual state and Xray mutation | WireGuard-scoped mTLS management API | none |
| Telegram bot | Telegram update ingress and Bot API credential | public webhook, internal send and owner calls | none |
| Notification | notification inbox, causal ordering, delivery jobs | owner state reads and Telegram send | subscription/payment/access consumers |
| Admin | principals, RBAC, action recovery, append-only audit | admin mTLS API and typed owner calls | none |

No service imports another service's `internal` tree. No application SQL names a
foreign owner database. `internal/platform` contains technical HTTP, Kafka,
metrics, tracing, TLS, logging, maintenance, and test primitives, not shared
business entities. Kafka is used for durable facts and commands that tolerate
asynchronous completion; immediate reads and command admission use HTTP.

## Control review

| Control | Result | Evidence |
|---|---|---|
| Database per service | Pass locally | eight owner initialization scripts and migrations; Compose uses distinct databases |
| Cross-service SQL/imports | Pass | repository scans; owner access occurs through typed HTTP clients or Kafka |
| Transactional outbox/inbox | Pass | Billing, Subscription, Access, Provisioning, and Notification transaction tests |
| Duplicate/replay/collision handling | Pass | unique business keys, request hashes, normalized inboxes, contract and PostgreSQL tests |
| Delayed/out-of-order delivery | Pass | producer sequences, durable cursors, gap deferral, FIFO barriers, terminal suppression |
| Money representation | Pass | integer minor units in contracts/storage; no production money `float` |
| Payment state machine | Pass for sandbox | persisted provider idempotency key, authenticated status verification, terminal transaction |
| Entitlement lifecycle | Pass | immutable periods, PostgreSQL clock, refund recalculation, scheduler leases |
| Credential lifecycle | Pass | encrypted UUID, HMAC token lookup, revision fencing, exact revoke proof |
| Provisioning convergence | Pass | desired/actual reconciliation, capacity reservation, node tombstones, generation fencing |
| Notification ordering | Pass | causal streams, terminal preemption, current owner-state checks, permanent-failure release |
| Admin action recovery | Pass | stable owner key, `outcome_unknown`, lease/replay, append-only attempt audit |
| HTTP safety | Pass | body limits, bounded headers/timeouts, route templates, TLS policies, graceful shutdown |
| Health semantics | Pass locally | dependency-aware readiness and process liveness tested through outage drills |
| Contract compatibility | Pass | linted OpenAPI/AsyncAPI/JSON Schema and event contract tests |
| Placeholder production logic | Pass | no TODO/FIXME/not-implemented path in Go or SQL; fake providers are release-excluded |
| Release inventory | Pass | every production Dockerfile is classified in the 19-image inventory |

## Findings

### ACR-001 - Production database roles are not yet least-privilege separated

- Severity: P2
- Component: PostgreSQL / all owner services except Admin
- Evidence: `deploy/postgres/init/*` grants the runtime role database ownership
  for seven owner databases; Admin alone demonstrates separate runtime and
  migrator roles.
- Impact: a compromised runtime could alter schema or bypass intended
  append-only/protection constraints in its own database. The boundary between
  services remains intact, but owner-local blast radius is larger than the
  production least-privilege target.
- Required correction: approve a production role model with separate
  database/schema owner, migrator, runtime, backup, and restore identities;
  generate environment-specific grants; exercise migration, runtime, backup,
  restore, and credential rotation with those exact roles.
- Owner: Platform / DBA
- Due date: before production infrastructure approval
- Rationale for deferral: local Compose optimizes repeatable disposable setup;
  no production database exists or is authorized.
- Product-owner acceptance: pending
- Status: blocked
- Regression/evidence: required staging role/grant test is `INF-DB-01` in the
  go/no-go checklist.

### ACR-002 - No production schema compatibility and canary rollback window has been exercised

- Severity: P2
- Component: release management / all stateful services
- Evidence: migrations are forward-only and release artifacts are immutable,
  but there is no approved production topology or two-version staging drill
  proving old/new HTTP consumers, Kafka producers/consumers, and database
  runtime coexist through an expand/migrate/contract window.
- Impact: a control-plane rollback after a migration could restore binaries
  that cannot safely interpret the current schema or event stream.
- Required correction: select a release topology, define the supported
  N/N-1 window per contract, run a two-version staging canary and rollback by
  digest, then attach evidence to `ROLL-01`.
- Owner: Platform / service owners
- Due date: before the first production release candidate is approved
- Rationale for deferral: the repository has no registry, staging environment,
  or approved production deployment model.
- Product-owner acceptance: pending
- Status: blocked
- Regression/evidence: production rollback runbook and mandatory staging
  acceptance procedure.

## Residual observations

- Local database initialization must not be reused as production IAM.
- YooKassa behavior is verified only against sandbox/fake-provider controls.
- Local Compose availability is not evidence for PostgreSQL HA, Kafka quorum,
  Redis failover, multi-host scheduling, or DNS/edge behavior.
- At-least-once Telegram delivery can still produce a duplicate when Telegram
  accepts a message and the response is lost; ADR 0025 correctly avoids an
  exactly-once claim.
- Contract and state-machine changes after this review invalidate the relevant
  sections and require a focused re-review.

## Acceptance

The application architecture/code review is complete for the stated baseline.
It has no open P0/P1. A production `GO` is prohibited while ACR-001 or ACR-002
is blocked.
