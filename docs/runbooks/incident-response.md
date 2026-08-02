# Incident Response

Status: Stage 9 draft for local/disposable exercises
Production owner, contacts, paging system, legal matrix, and providers: blocked

This runbook coordinates technical containment and recovery. It does not
authorize interception or logging of VPN traffic, disclosure of personal data,
payment mutation, certificate issuance, or production access.

## Activation and severity

| Severity | Definition | Initial response objective |
|---|---|---|
| SEV-1 | confirmed credential compromise, payment integrity failure, broad VPN/control-plane outage, data breach, or unrecoverable state | acknowledge within 5 minutes; incident commander and security immediately |
| SEV-2 | material regional/service degradation, growing backlog, failed failover, restore risk, or privileged control anomaly | acknowledge within 15 minutes |
| SEV-3 | bounded defect with workaround and no integrity/security impact | acknowledge within business support objective |

Production objectives are not approved until a real receiver and escalation
test closes `OPS-ALERT-01`.

## Roles

- Incident commander: owns severity, scope, decisions, and timeline.
- Operations lead: executes approved infrastructure actions.
- Service owner: diagnoses owner state and performs reconciliation.
- Security lead: controls credential compromise, evidence, and access.
- Communications lead: publishes approved user/provider updates.
- Legal/privacy lead: decides breach, lawful-request, and regulatory actions.
- Scribe: records sanitized timestamps, commands, decisions, and aggregate
  outcomes.

One person may hold several roles in a local drill. Production must name primary
and backup people before launch.

## First 15 minutes

1. Open an incident record with UTC start time, detector, suspected systems, and
   a non-sensitive summary.
2. Assign incident commander and severity. Start a decision log.
3. Protect users and evidence: stop new sales, deployments, placement, or
   privileged actions when integrity is uncertain.
4. Confirm the symptom through aggregate metrics and owner-safe status APIs.
   Do not paste payloads, bearer paths, chat IDs, provider IDs, VLESS UUIDs,
   private keys, destination data, or database rows into the incident record.
5. Bound scope by service, region, release digest, credential generation, and
   time window.
6. Choose containment that preserves durable owner state. Do not delete queues,
   truncate tables, flush Redis as a repair, rebuild mutable tags, downgrade a
   database, or edit node Xray state manually.
7. Notify Security/Legal/Privacy immediately for suspected secret or personal
   data exposure; notify Finance for payment integrity.

## Containment by incident class

### Payment or entitlement ambiguity

- stop new checkout only if reconciliation cannot establish provider truth;
- keep the original provider idempotency key;
- authenticate a provider status read and compare account, object, amount,
  currency, metadata, status, capture/refund state;
- preserve webhook inbox, order/payment state, outbox and audit;
- replay/reconcile through Billing, then verify one immutable Subscription period
  and the resulting Access state;
- never grant entitlement from an unauthenticated webhook body.

### Kafka or database outage

- preserve outbox/inbox rows and committed offsets;
- pause deployments and destructive maintenance;
- recover quorum/primary using the approved infrastructure procedure;
- verify leases expire, readiness recovers, and backlogs drain in causal order;
- compare payment, period, credential, operation, notification, and audit
  aggregate counts before and after replay.

### Subscription token or VPN credential exposure

- suppress the exposed bearer path at edge and telemetry;
- rotate the subscription token through Access; require uniform old-token 404;
- if VLESS/REALITY material may be exposed, revoke/re-provision through normal
  higher-revision commands and node last-known-good controls;
- never place old/new values in evidence;
- review Access audit, token issue/rotation aggregate state, and node convergence.

### Certificate or privileged identity compromise

- disable the Admin principal or workload enrollment immediately;
- revoke/retire the certificate through the approved PKI, remove active sessions
  where supported, and rotate dependent trust safely;
- retain overlap only for uncompromised generations;
- inspect safe admin action audit and owner mutations;
- issue a replacement identity only after scope and custody are established.

The repository proves local principal disable and untrusted/expired certificate
rejection. Production revocation remains blocked.

### VPN node or Xray failure

- stop placement on an unhealthy or suspect node;
- preserve desired/actual revision and allocation proof;
- allow failover only when real traffic and capacity are healthy;
- use fixed node-agent reconciliation and last-known-good rollback;
- revoke/decommission through revisioned owner commands, not shell or direct
  configuration edits.

### Observability failure

- business processing must continue; do not make readiness depend on telemetry
  backends;
- use out-of-band host/provider signals and direct safe health checks;
- restore Collector/backends, verify backlog/dropped telemetry expectations, and
  test privacy filters before normal access resumes;
- never weaken mTLS or expose a backend publicly as a workaround.

## Recovery and validation

Recovery is complete only when:

- liveness and dependency-aware readiness are correct;
- durable backlogs drain without duplicate business effect;
- payment, Subscription, Access, Provisioning, Notification, and Admin owner
  states reconcile;
- node desired/actual revisions and capacity agree;
- compromised generations are retired and old bearer tokens reject uniformly;
- dashboards/alerts recover without forbidden data;
- the active image/config/migration set is identified by immutable evidence;
- a user-impact, security, privacy, and financial assessment is approved.

## Evidence handling

Record command, UTC time, source commit/image digest, aggregate result, operator,
and expiration. Store sensitive provider/legal/host evidence only in an approved
restricted system and reference its opaque case ID. Do not attach raw logs,
payloads, URLs, credentials, packet captures, or customer exports to Git or CI.

## Closure

The incident commander owns closure after service, Security, Finance, Privacy,
and Legal approvals as applicable. Create tracked corrective actions with owner,
severity, due date, regression evidence, and risk-register update. Re-run the
relevant game-day scenario after correction.
