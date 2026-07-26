# Stage 9 Game-Day Report

Report date: 2026-07-26  
Execution owner: Platform / Operations  
Source commit: pending final Stage 9 evidence run  
Environment: local/disposable Docker Compose only  
Overall result: **incomplete; production decision NO-GO**

No real provider, VPS, credential, DNS, registry, database, customer record, or
traffic was used. Commands produce aggregate output only; heavy generated
artifacts remain ignored and will be checksum-indexed after the final run.

## GD-01 - YooKassa timeout and ambiguous result

- Goal: recover provider-committed payment creation after the response is lost.
- Environment: fake YooKassa plus local Billing/PostgreSQL/Kafka.
- Preconditions: empty disposable databases; sandbox identity; fixed provider
  idempotency key persisted before I/O.
- Fault injection: fake provider commits then returns ambiguous failure.
- Blast radius: one synthetic order/payment.
- Abort condition: second provider mutation, unsupported entitlement, or payload
  leakage.
- Expected metrics/alerts: bounded provider error/reconciliation counters; no
  identifier labels. Real receiver is unavailable.
- Expected business behavior: authenticated same-key reconciliation produces one
  succeeded payment and one entitlement effect.
- Recovery: Billing verification worker re-reads provider truth.
- Rollback: delete disposable project only; never delete a real payment.
- Actual result: pending final `make compose-smoke`.
- Budget: within bounded local retry window; exactly one payment effect.
- Evidence: compose smoke and Billing ambiguous-result regression tests.
- Residual risk: real provider/network and production receipt flow untested.

## GD-02 - Prolonged Kafka outage and replay

- Goal: preserve durable work and avoid duplicate entitlement.
- Environment: unique disposable Compose project.
- Preconditions: one completed synthetic payment and recorded period count.
- Fault injection: stop Kafka, reset the test outbox row to pending, wait, restart.
- Blast radius: disposable Kafka and one synthetic event.
- Abort condition: ordinary project volume changes or second Subscription period.
- Expected metrics/alerts: broker/outbox/lag degradation and recovery; real paging
  unavailable.
- Expected business behavior: services remain recoverable; outbox republishes;
  inbox/idempotency prevents a second period.
- Recovery: restart broker and allow normal publisher/consumer retry.
- Rollback: unique project `down -v`; verify preservation sentinel.
- Actual result: pending final `make resilience-drill`.
- Budget: outbox becomes published within 60 seconds after broker recovery.
- Evidence: resilience script and period-count assertion.
- Residual risk: single local broker is not production quorum recovery.

## GD-03 - Real PostgreSQL failover

- Goal: prove automatic primary failover without split brain or lost committed
  business effect.
- Environment: unavailable; local Compose has one PostgreSQL instance.
- Preconditions: production-like primary/standby, failover manager, WAL archive,
  isolated clients, and approved RPO/RTO.
- Fault injection: terminate/fence primary host while writes and reads execute.
- Blast radius: staging only.
- Abort condition: split brain, unbounded errors, RPO/RTO breach, or inability to
  identify authoritative primary.
- Expected metrics/alerts: role/failover, replication lag, write/read failure,
  readiness and paging.
- Expected business behavior: bounded retry, no duplicate payment/period/action,
  readiness recovers on the new primary.
- Recovery: provider-specific failover and owner reconciliation.
- Rollback: fence old primary; fail back only through approved procedure.
- Actual result: blocked; not simulated as success.
- Budget: must be approved before staging; local stop/start is insufficient.
- Evidence: none for real HA.
- Residual risk: critical production failover path unknown; hard NO-GO.

## GD-04 - Primary VPN node loss

- Goal: prove active user traffic survives through assigned failover.
- Environment: two local node-agents and real pinned Xray VLESS + REALITY.
- Preconditions: primary/failover assignment and successful initial traffic.
- Fault injection: stop primary node-agent/Xray path.
- Blast radius: one synthetic credential and local camouflage destination.
- Abort condition: traffic leaves the disposable network, failover fails, or
  capacity/revision state corrupts.
- Expected metrics/alerts: primary unhealthy, failover/reconciliation and capacity
  state; real paging unavailable.
- Expected business behavior: SOCKS request reaches camouflage through failover.
- Recovery: restart primary, health check, Provisioning reconciliation.
- Rollback: remove disposable project and generated keys.
- Actual result: pending final `make resilience-drill`.
- Budget: failover request succeeds within bounded script retries.
- Evidence: real-Xray failover assertion in resilience/compose smoke.
- Residual risk: no provider network, regional failure, or production load.

## GD-05 - Leaked subscription token

- Goal: rotate bearer token, reject old value uniformly, and prove no telemetry
  leak.
- Environment: generated local Access token and observability stack.
- Preconditions: active Access state and owner-authorized rotation.
- Fault injection: classify the old generated token as exposed and rotate.
- Blast radius: one synthetic subscription.
- Abort condition: old token renders, response fingerprint differs, or token
  appears in logs/metrics/traces.
- Expected metrics/alerts: bounded route/status metrics only.
- Expected business behavior: old token receives uniform no-store 404; new token
  works only when delivered through the intended one-time flow.
- Recovery: issue another clean generation if the new token is suspect.
- Rollback: never reactivate the exposed token.
- Actual result: pending final rotation and observability smoke.
- Budget: old token unusable immediately after committed rotation.
- Evidence: Access credential tests, secret rotation, telemetry sentinel scan.
- Residual risk: production edge caches/access logs not selected.

## GD-06 - Expired internal service certificate

- Goal: prove an expired client certificate cannot call an internal mTLS API.
- Environment: in-process TLS 1.3 server with generated CA and valid/expired
  clients.
- Preconditions: valid server leaf, trusted CA, control request with valid leaf.
- Fault injection: repeat with a client leaf whose validity ended one hour ago.
- Blast radius: test process only.
- Abort condition: expired request reaches the handler.
- Expected metrics/alerts: TLS rejection; production expiry alert is unavailable.
- Expected business behavior: no authenticated request or owner mutation.
- Recovery: issue valid replacement and preserve identity allowlist.
- Rollback: do not extend or bypass validation.
- Actual result: focused test passed before the final evidence run; full rerun
  pending.
- Budget: expired handshake rejected immediately.
- Evidence: `TestMutualTLSRejectsExpiredClientCertificate`.
- Residual risk: production issuance, expiry alert, and fleet replacement blocked.

## GD-07 - Expired node-agent certificate

- Goal: prevent an expired node or provisioning leaf from mutating desired state.
- Environment: generic TLS expiry proof plus node exact-SPIFFE tests.
- Preconditions: node management uses shared TLS 1.3 mTLS primitive and exact
  verified-chain identity.
- Fault injection: expired client leaf / unverified node identity.
- Blast radius: test process only.
- Abort condition: request reaches desired-state handler.
- Expected metrics/alerts: node/auth failure and capacity protection; real paging
  unavailable.
- Expected business behavior: mutation rejected and node removed from placement
  when health expires.
- Recovery: approved reissue and re-enrollment, then reconciliation.
- Rollback: never trust an expired or unverified leaf.
- Actual result: partial local proof; production-like node expiry not executed.
- Budget: immediate rejection; replacement RTO not approved.
- Evidence: TLS expiry and provisioning node-certificate tests.
- Residual risk: production revocation/reenrollment is a hard NO-GO.

## GD-08 - Compromised admin certificate

- Goal: contain a compromised operator identity and prevent new actions.
- Environment: Admin PostgreSQL integration database and local mTLS/RBAC tests.
- Preconditions: enabled principal with operations role.
- Fault injection: migrator/identity owner disables the principal.
- Blast radius: one synthetic administrator.
- Abort condition: disabled identity can authenticate or execute a new action.
- Expected metrics/alerts: safe auth denial and privileged-action audit; real
  security paging unavailable.
- Expected business behavior: existing certificate possession is insufficient
  after principal disable.
- Recovery: investigate audit, revoke certificate, issue replacement only after
  custody review.
- Rollback: re-enable only after Security approval; never reuse compromised key.
- Actual result: integration test added; final PostgreSQL rerun pending.
- Budget: database disable affects the next request.
- Evidence: `TestIntegrationDisabledAdminPrincipalLosesAccessImmediately`.
- Residual risk: CA revocation/session drain and replacement are untested.

## GD-09 - Invalid Xray reload

- Goal: preserve a healthy last-known-good process after invalid candidate.
- Environment: local pinned Xray and disposable systemd host.
- Preconditions: healthy current state and last-known-good snapshot.
- Fault injection: invalid candidate / one-shot startup failure.
- Blast radius: one disposable node.
- Abort condition: Xray remains stopped or candidate becomes current.
- Expected metrics/alerts: reload failure/rollback-restored; real receiver absent.
- Expected business behavior: existing configuration remains/restores healthy;
  node is removed if rollback fails.
- Recovery: fixed helper restores last-known-good and reports sanitized failure.
- Rollback: host role/config rollback only after validation.
- Actual result: pending final resilience and node-hardening drills.
- Budget: bounded consistency context completes before request returns.
- Evidence: systemd manager tests and disposable Ansible test.
- Residual risk: provider host/kernel variance.

## GD-10 - Restore all eight databases

- Goal: prove confidential, integrity-checked, exact-owner recovery.
- Environment: disposable source and isolated restore PostgreSQL clusters.
- Preconditions: generated age identity; eight initialized owner databases.
- Fault injection: treat source cluster as unavailable after encrypted backups.
- Blast radius: disposable databases and ignored artifacts only.
- Abort condition: plaintext artifact, non-empty target, wrong database/owner,
  count mismatch, checksum failure, or cleanup failure.
- Expected metrics/alerts: timed backup/restore result; production monitor absent.
- Expected business behavior: all public table counts and relation owners match.
- Recovery: stream decrypt into single-transaction restore.
- Rollback: destroy isolated cluster, identities, artifacts and volumes.
- Actual result: pending final `make backup-restore-drill`.
- Budget: local RPO <= 24 hours and RTO <= 30 minutes.
- Evidence: backup metadata/checksum and aggregate comparison output.
- Residual risk: off-host immutable storage, PITR and production custody blocked.

## GD-11 - Previous release candidate rollback

- Goal: prove N/N-1 rollback by immutable digest across code, schema, Kafka, and
  configuration.
- Environment: unavailable; no approved registry or two-version staging.
- Preconditions: accepted N and N-1 manifests/digests, compatibility matrix,
  canary, alerting and expand/migrate/contract state.
- Fault injection: deploy bad N canary then trigger abort.
- Blast radius: staging canary only.
- Abort condition: incompatible schema/event, payment integrity risk, or inability
  to return to N-1 without DB downgrade.
- Expected metrics/alerts: fast burn, deployment/digest mismatch, queue and owner
  reconciliation alerts with real acknowledgement.
- Expected business behavior: canary drains; N-1 resumes without duplicate effect.
- Recovery: deploy verified N-1 digest or roll forward.
- Rollback: never rebuild tag or automatically downgrade DB.
- Actual result: blocked; local code-path tests are not accepted as this drill.
- Budget: must be approved with production topology.
- Evidence: rollback runbook only; no execution evidence.
- Residual risk: critical rollback path unknown; hard NO-GO.

## GD-12 - Observability backend unavailable

- Goal: prove telemetry loss cannot stop business processing.
- Environment: unique disposable Compose project.
- Preconditions: healthy app and observability profile.
- Fault injection: stop Prometheus, Grafana, Collector, Tempo, and Loki.
- Blast radius: local telemetry only.
- Abort condition: Access liveness/readiness or public request behavior changes.
- Expected metrics/alerts: backend telemetry is unavailable; production requires
  an out-of-band alert.
- Expected business behavior: Access remains live/ready and uniform public 404
  processing continues.
- Recovery: restart backends and re-run observability smoke.
- Rollback: unique project cleanup.
- Actual result: scenario added to resilience drill; final execution pending.
- Budget: business checks remain successful throughout the fault.
- Evidence: resilience script assertions.
- Residual risk: no real out-of-band alert or durable production telemetry store.

## Current decision

The report cannot pass while GD-03 and GD-11 are unexecuted and GD-07/GD-08 lack
production-like PKI revocation. Local results will be updated after the final
commit-bound run; those blockers still require approved staging.
