# Production Rollback and Recovery

Status: design complete; critical staging drill not yet passed
Production decision: **NO-GO**

## Principles

1. Roll back only to a previously accepted immutable registry digest whose
   source commit, per-image SBOM, vulnerability decision, license approval,
   signature, provenance, and configuration compatibility verify.
2. Never rebuild an old mutable tag. Different bytes are a new candidate.
3. Database migrations are forward-only. There is no automatic schema downgrade.
4. Prefer forward fix when old code cannot read the current schema or event
   contract.
5. Restore a database only into an isolated empty target after explicit incident
   and data-owner approval.
6. Preserve payment, entitlement, credential, provisioning, notification, and
   audit facts; do not repair by deleting inbox/outbox rows or resetting offsets.

## Preconditions

- offline preflight passes for the exact environment document, reviewed full
  commit, service-binding contract, and all 19 immutable image digests;
- accepted current and previous release manifests with immutable image digests;
- N/N-1 compatibility matrix for HTTP, Kafka, schema, and configuration;
- canary cohort and abort alerts;
- expand/migrate/contract phase recorded for every migration;
- exact production configuration version and secret/trust generations;
- on-call, service owners, Security, DBA, and release approver available;
- verified recent backup/PITR and isolated restore environment.

If any precondition is missing, stop deployment and use forward remediation.
The preflight command and complete rollout order are defined in
`docs/production/deployment-and-rollback.md`; a preflight pass is not deployment
authorization.

## Control-plane service rollback

1. Freeze rollout, migrations, retention, node enrollment, and unrelated changes.
2. Identify first bad digest/config and affected services from canary metrics and
   owner-safe state.
3. Confirm the previous digest supports the current schema, current and prior
   Kafka versions, and active trust/key generations.
4. Deploy the previous digest to the canary by digest. Do not change the database.
5. Verify liveness/readiness, error/latency SLOs, Kafka lag, outbox/inbox progress,
   and owner reconciliation.
6. Expand rollback one bounded cohort at a time.
7. Stop and roll forward if any compatibility check fails.

Success: all instances run the intended verified digest, queues drain in order,
owner states agree, and no duplicate payment/entitlement/access effect exists.

## Canary abort

Abort immediately on payment integrity mismatch, secret/privacy leakage,
authentication bypass, schema error, event poison/conflict growth, inability to
restore node last-known-good, or fast-burn availability alert. Drain/stop the
candidate, preserve evidence, and return traffic to the last accepted digest
only when compatibility is proven.

## Schema compatibility

Use:

1. **Expand:** additive nullable/default-safe schema and code that reads both
   forms.
2. **Migrate:** bounded, observable, idempotent backfill while N and N-1 coexist.
3. **Contract:** remove old fields/constraints only after rollback window closes,
   all producers/consumers advance, and a new backup/restore point verifies.

A contracted schema makes binary rollback unsafe. Use a forward fix or restore
into isolation; never run an inverse migration automatically.

## Kafka compatibility

- New producers publish only versions supported by active N/N-1 consumers.
- Consumers tolerate documented additive fields and reject unknown semantic
  versions through sanitized durable policy.
- Keep producer-owned sequence, partition key, event ID, correlation/causation,
  and idempotency semantics unchanged in a rollback window.
- Before rolling a producer back, confirm it cannot reuse an aggregate sequence
  or omit state introduced by the newer producer.
- Before rolling a consumer back, confirm its inbox/cursor understands every
  retained event. Otherwise pause the group and roll forward.

Never reset offsets or purge topics as rollback.

## Business reconciliation

### Payment

- authenticate provider truth using the existing provider idempotency key;
- compare safe account/object/amount/currency/status facts;
- ensure one terminal payment/outbox fact and preserve refund state;
- do not create a replacement payment for an ambiguous result.

### Subscription and Access

- verify one immutable period per successful payment;
- recompute current entitlement from owner facts and PostgreSQL time;
- verify Access desired revision, credential status, issue/rotation audit, and
  exact assignment/revoke proof;
- replay only through owner idempotent commands with existing event/action keys.

### Provisioning and Notification

- reconcile desired/actual node revisions and capacity;
- retain causal notification streams and terminal suppression;
- do not manually resurrect a stale notification or provisioning operation.

## Node-agent and Xray

The node-agent validates the candidate, atomically installs it, and health-checks
Xray. A failed reload restores last-known-good under an independent bounded
context. Remove an unhealthy node from placement before manual host recovery.
Do not pass arbitrary shell/config through the management API.

## Cryptographic rollback

### REALITY

Keep old/new public generations during the approved overlap. Roll back traffic
to an uncompromised old generation only while its private key remains protected.
If compromise is suspected, issue a third generation and revoke/reprovision;
never restore the compromised key.

### mTLS

Trust old and new roots during leaf cutover. Roll back an uncompromised leaf
while both roots are trusted. After compromise, disable identity, revoke/retire
the credential, issue a clean generation, and remove old trust after fleet
verification.

### Subscription token

Rotation commits a new token hash and invalidates the old token uniformly. A
rollback must not reactivate an exposed/revoked token. Recover by issuing another
new token, not by database edits.

## DNS and edge

- version DNS records, edge routes, certificate references, rate limits, and log
  policy as one reviewed configuration change;
- use a TTL compatible with the rollback objective and verify both old/new
  origins before shift;
- preserve `/s/{token}` log suppression in every version;
- roll back weighted traffic/config, not certificate validation or request
  limits;
- account for resolver/cache propagation and keep old origin capacity until the
  window closes.

## Backup restore and full disaster recovery

1. Declare disaster and freeze writes/credentials through approved controls.
2. Build an isolated clean environment from verified infrastructure/config.
3. Restore all eight owner databases from checksum-verified encrypted artifacts
   or approved PITR point with exact owner roles.
4. Restore Kafka only through the approved broker recovery procedure; reconcile
   retained outbox/inbox facts and offsets.
5. Recreate Redis empty and allow ephemeral state to repopulate.
6. Restore secret/PKI trust generations through custody procedure.
7. Enroll nodes and reconcile desired/actual state; do not import unknown host
   state.
8. Deploy verified digests and run owner, payment, VPN, notification, audit,
   privacy, and observability acceptance checks before traffic.

Success criteria:

- approved RPO/RTO met;
- no split brain or unaccounted write window;
- exact database owners and integrity checks pass;
- one payment/entitlement effect per business key;
- all active credentials are converged or safely revoked;
- real VLESS + REALITY traffic succeeds through primary and failover;
- alert delivery and escalation acknowledge;
- Security/Privacy/Finance/Operations approve reopening.

## Drill state

Local last-known-good, key overlap, token rotation, Kafka replay, eight-database
restore, and node failover paths are executable. The critical registry-digest
N/N-1 canary rollback, production schema compatibility window, DNS/edge rollback,
real PKI revocation, PostgreSQL HA, alert delivery, and full DR are untested
without approved staging. `ROLL-01` therefore remains a hard NO-GO gate.
