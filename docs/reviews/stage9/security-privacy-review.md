# Stage 9 Security and Privacy Review

Review date: 2026-07-26  
Review baseline: `76f5624e664c556c3ec598055524319eab0af1f3`  
Review owner: Security / Privacy  
Environment: repository and local/disposable test topology only

## Result

No P0 or P1 application defect was found. Existing local controls consistently
protect payment credentials, bearer URLs, VLESS UUIDs, REALITY private keys,
Telegram data, provider payloads, and user traffic metadata.

Seven P2 production controls remain unimplemented or unapproved. They are hard
launch blockers, not accepted residual risk. Production secrets, customer data,
public DNS, real providers, and real VPN nodes must not be introduced until the
corresponding gates are closed.

## Authentication and authorization matrix

| Boundary | Authentication | Authorization | Local evidence | Production gap |
|---|---|---|---|---|
| Telegram webhook | secret header, bounded HTTPS ingress contract | exact route and update dedupe | webhook security tests | edge/DDoS/source policy and real secret custody |
| YooKassa webhook | provider object re-fetch and account/object verification | sandbox account, amount, currency, metadata, status | Billing tests and sandbox runbook | production agreement, official source policy, credentials |
| `/s/{token}` | 256-bit bearer token, HMAC lookup | current Access state only | uniform 404/no-store and redaction tests | dedicated domain, edge log suppression, DDoS policy |
| Internal service HTTP | TLS 1.3 mTLS with exact SPIFFE identities | per-route identity allowlist | dev CA and Compose tests | issuing CA/HSM, revocation, inventory, expiry alerting |
| Admin API | TLS 1.3 mTLS admin SPIFFE identity | enabled principal plus explicit RBAC permission | Admin security/PostgreSQL tests | operator device and certificate custody/revocation |
| Node management | WireGuard reachability plus TLS 1.3 mTLS | exact provisioning SPIFFE; health identity is read-only | Compose and disposable Debian host | approved VPS, production PKI and enrollment |
| Kafka | private Compose network | owner topics, schema/cursor checks in application | contract and replay tests | broker authentication and per-principal ACL |
| PostgreSQL | per-service credentials | database-per-service | eight isolated local databases | split runtime/migrator/backup roles and HA |
| Redis | password and private network | ephemeral keys only | Compose/outage tests | production topology, TLS/auth rotation |
| Metrics/OTLP | private TLS 1.3 mTLS identities | exact observability identity/allowlist | 8/11-target and trace/log smoke | production tenancy, storage, receiver auth |
| Operator CLI | TLS client certificate | server RBAC and output guard | typed CLI tests | managed workstation, short-lived credential, break-glass |
| CI/release | commit-pinned actions; GitHub OIDC only in manual workflow | no registry/deploy permission | release manifest/SBOM/scan tests | protected environment, registry IAM, production approval |

## Trust-boundary review

- Public HTTP paths have bounded bodies, headers, server timeouts, explicit
  methods, sanitized errors, route-template telemetry, and no arbitrary proxy.
- YooKassa confirmation URLs and webhook bodies are neither logged nor retained
  as general payloads. Ambiguous provider results preserve idempotency and are
  reconciled through authenticated reads.
- Access stores only token HMAC and encrypted VLESS credential; rendering is
  no-store and malformed, expired, revoked, and unknown tokens share a uniform
  rejection fingerprint.
- Internal and management mTLS uses exact URI identities rather than DNS-name
  possession alone. Node mutation cannot accept shell fragments or arbitrary
  Xray configuration.
- Kafka DLQ/inbox data is normalized to coordinates, hashes, bounded reason
  codes, and allowlisted metadata. No raw record is copied into operator paths.
- Containers are non-root, capability-reduced, read-only where compatible, and
  receive private keys through per-owner credential staging with restrictive
  ownership and mode.
- Telemetry has bounded labels and explicit trace/log allowlists. User traffic,
  destination IPs, DNS history, packet contents, raw URLs, identifiers, SQL,
  message keys, payloads, and error text are outside the telemetry contract.
- Backups stream from a consistent snapshot into age encryption, verify
  ciphertext checksums, restore only into an exact empty database, and clean
  temporary identities/artifacts.
- Retention is owner-local, dry-run-first, bounded, PostgreSQL-clocked, and
  protects financial/correctness records, legal holds, fresh rows, and unresolved
  dead letters.
- Repository and release checks cover committed/filesystem secrets, known
  vulnerabilities, immutable image subjects, SBOMs, scan reports, and provenance
  metadata. A production registry and verification policy do not yet exist.

## Findings

### SPR-001 - Production PKI, revocation, and privileged identity custody are undefined

- Severity: P2
- Component: internal mTLS, node management, Admin
- Evidence: development CA and rotation drills exist; no approved production CA,
  HSM/secret manager, inventory, revocation distribution, or compromise drill.
- Impact: a stolen admin or provisioning certificate may remain usable.
- Required correction: approve offline/root and issuing hierarchy, key custody,
  leaf lifetime, automated issuance, revocation mechanism, emergency disable,
  audit, alerting, and tested recovery for service, node, and admin identities.
- Owner: Security / Operations
- Due date: before production credentials or node enrollment
- Status: blocked
- Evidence gate: `SEC-PKI-01`

### SPR-002 - Production edge and abuse controls are not selected or tested

- Severity: P2
- Component: Telegram webhook, YooKassa webhook, subscription endpoint
- Evidence: loopback/local ingress, application limits, and redaction tests exist;
  no domains, reverse proxy, DDoS service, source policy, or immutable redacted
  access-log configuration is approved.
- Impact: credential-path leakage, ingress exhaustion, forged-webhook storage
  pressure, or loss of availability.
- Required correction: approve separate domains, TLS/edge ownership, exact
  logging exclusions for bearer paths, provider ingress verification, rate
  limits, WAF/request normalization, and a load/abuse test.
- Owner: Platform / Security
- Due date: before public DNS
- Status: blocked
- Evidence gate: `INF-EDGE-01`

### SPR-003 - Kafka and database production least privilege are not proven

- Severity: P2
- Component: Kafka / PostgreSQL
- Evidence: private local network and application ownership checks exist; broker
  ACLs and split database runtime/migrator/backup roles do not.
- Impact: a compromised workload may read or mutate more owner-local data or
  integration streams than required.
- Required correction: per-service broker principals and topic/group ACLs;
  separate database identities and grants; rotation and denial tests.
- Owner: Platform / DBA / Security
- Due date: before production infrastructure acceptance
- Status: blocked
- Evidence gate: `SEC-DATA-01`

### SPR-004 - Production secret manager and rotation custody are unapproved

- Severity: P2
- Component: all credentials and cryptographic keyrings
- Evidence: `_FILE`/mount ingestion, bounded keyrings, local rotation, and
  no-command-line-secret tests exist; production storage and operator access do
  not.
- Impact: unmanaged copies, weak access review, or unrecoverable key loss.
- Required correction: approve secret manager, envelope/HSM policy where
  applicable, service identities, dual control, backup/escrow, audit, emergency
  rotation, and environment separation.
- Owner: Security / Operations
- Due date: before any production secret is generated
- Status: blocked
- Evidence gate: `SEC-SECRET-01`

### SPR-005 - Privacy rights, abuse, breach, and lawful-request workflows are unapproved

- Severity: P2
- Component: product operations and all data owners
- Evidence: data minimization, retention tooling, support-safe reads, and
  support-abuse draft exist; jurisdiction, lawful basis, data-controller roles,
  DSAR identity verification, deletion exceptions, breach notice, and request
  handling are unresolved.
- Impact: personal-data handling could violate applicable law or user promises.
- Required correction: counsel-approved privacy inventory/policy, data-subject
  procedure, subprocessor register, breach and lawful-request playbooks, abuse
  ownership, and tested operator access.
- Owner: Product owner / Privacy / Legal
- Due date: before real customer records
- Status: blocked
- Evidence gate: `LEGAL-PRIV-01`

### SPR-006 - Production observability tenancy, storage, and alert delivery are absent

- Severity: P2
- Component: Prometheus, Alertmanager, Grafana, Tempo, Loki
- Evidence: local private topology and inert receiver pass smoke tests; no
  authenticated production UI, durable storage, tenancy, real receiver, or
  escalation acknowledgement exists.
- Impact: incidents may be invisible or sensitive telemetry may be overexposed.
- Required correction: approve production identity/tenancy and retention,
  encrypted storage, backup requirements, real receiver, on-call ownership,
  escalation, and end-to-end alert acknowledgement drill.
- Owner: SRE / Security
- Due date: before production traffic
- Status: blocked
- Evidence gate: `OPS-ALERT-01`

### SPR-007 - No production-like compromised-certificate containment drill exists

- Severity: P2
- Component: Admin and node/service mTLS
- Evidence: expired/untrusted certificate rejection and local trust-overlap
  rotation are tested; production revocation and fleet propagation are not.
- Impact: containment time and service impact after certificate compromise are
  unknown.
- Required correction: run staging compromise drills for admin, provisioning,
  node-agent, and service identities; prove revocation propagation, connection
  termination, audit, replacement, and bounded recovery.
- Owner: Security / Operations
- Due date: before production PKI acceptance
- Status: blocked
- Evidence gate: `GAME-PKI-01`

## Privacy data-flow conclusions

The application intentionally persists Telegram identifiers only in Identity,
provider object references only in Billing, entitlements only in Subscription,
encrypted credentials/token hashes only in Access, node state only in
Provisioning/node-agent, and safe action/audit data only in Admin. Redis is not a
source of truth. No design requires destination logging or inspection of user
traffic.

Actual retention periods, lawful basis, geographic transfers, controller/
processor roles, and deletion exceptions are legal/product decisions and remain
blocked in the Stage 9 legal checklist.

## Acceptance

The repository-level security/privacy review is complete for the baseline and
has no open P0/P1. All seven P2 findings are launch blockers because no explicit
product-owner risk acceptance or production evidence exists.
