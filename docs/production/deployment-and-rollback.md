# Deployment And Rollback Contract

Status: procedure defined; production-like staging drill not executed

## Offline Preflight

Create an environment-owned candidate from the matching template. Keep only
opaque secret/key/certificate references and immutable image digests. Then run:

```text
make production-preflight ENVIRONMENT_CONFIG=<path> DEPLOY_ENVIRONMENT=<staging|production> SOURCE_COMMIT=<40-char-reviewed-commit>
```

The command is deliberately offline. A pass confirms repository consistency,
not provider reachability or deployment authorization. The committed templates
must fail because placeholders, zero capacity values, and mutable image values
are prohibited.

## Deployment Order

1. Freeze the reviewed commit, license decision, release manifest, signatures,
   provenance, SBOMs, scans, and all 19 image digests.
2. Approve the environment document and resolve references through authenticated
   deployment tooling. Stage credentials with owner-only modes; verify effective
   UID access before starting any process.
3. Establish private networks, edge policy, PKI/trust bundles, secret manager,
   registry pull, observability, real alert routing, and immutable backup target.
4. Create eight databases and distinct schema-owner, migrator, runtime, backup,
   and restore identities. Prove denials before granting traffic.
5. Create Kafka topics, retention/replication/min-ISR, exact principals/ACLs and
   groups. Configure Redis private TLS/auth and documented eviction behavior.
6. Verify recent backup/PITR and an isolated restore before migrations.
7. Run additive migrations with each owner migrator in dependency order:
   Identity, Catalog, Billing, Subscription, Access, Provisioning, Notification,
   Admin. Stop on any mismatch; do not run contracting migrations.
8. Start Identity and Catalog, then Billing and Subscription, then Access and
   Provisioning, then Telegram Bot and Notification, then Admin. Readiness must
   prove only owned dependencies; Kafka consumers remain paused until their
   producers and schemas are compatible.
9. Enroll the minimum node cohort through authenticated inventory, apply the
   hardened host baseline, bootstrap node-agent, validate Xray, and keep nodes
   out of placement until health and capacity evidence pass.
10. Deploy one canary cohort by digest. Keep external traffic disabled until
    mTLS, ACL denial, backup, alert, privacy, payment sandbox, Happ, provisioning,
    and VPN tests pass.
11. Shift bounded traffic cohorts while watching abort metrics. Enable public
    edge routes last; payment mode stays sandbox until its separate legal and
    provider approval.

## Release And Compatibility

- Deploy `registry/repository@sha256:<digest>` only. Tags are display metadata.
- The environment commit, release manifest, image inventory, config version,
  secret generation, trust generation, and migration phase form one approval.
- N and N-1 must both read the expanded schema and active key/trust generations.
- New producers publish only event versions understood by every active consumer.
- Contracting migrations occur only after the rollback window closes and a new
  accepted backup/restore point exists.
- Canary checks include payment idempotency, entitlement ordering, access
  delivery, revoke proof, Kafka lag/DLQ, node capacity, privacy sentinels,
  telemetry, and real alert acknowledgement.

## Abort And Rollback Order

1. Stop traffic expansion, migrations, retention, node enrollment, and unrelated
   changes. Preserve audit and evidence.
2. Remove the candidate cohort from public/admin routing. Pause only affected
   consumers if continued processing is unsafe; never reset offsets.
3. Confirm N-1 compatibility with current schema, event retention, config,
   credentials, and trust generations.
4. Return the canary to the last accepted digest, verify owner reconciliation,
   then roll back bounded cohorts. Use a forward fix if compatibility is absent.
5. Roll back edge weights/routes while retaining token-path suppression, TLS,
   body/rate limits, and old-origin capacity.
6. Drain or quarantine affected VPN nodes; node-agent restores only an
   uncompromised last-known-good config. Never restore a compromised key.
7. Database rollback is forward-only. PITR/restore is a disaster action into an
   isolated empty target, never an automatic inverse migration.
8. Close only after queues converge, one business effect remains per key,
   alerts recover, and Security/Operations/data owners approve.

The detailed business and cryptographic recovery rules remain in
`docs/runbooks/production-rollback.md`.
