# Encrypted Backup and Restore

This runbook covers the Stage 8 local clean-room drill for all eight PostgreSQL service databases. It does not define production storage, key custody, backup scheduling, or disaster authority.

## Local Drill

```powershell
make backup-restore-drill
```

The drill:

1. starts a fresh source PostgreSQL instance and applies every migration;
2. creates a disposable age X25519 identity;
3. streams one custom-format dump per service database directly into age encryption;
4. exports one read-only PostgreSQL MVCC snapshot, uses it for both `pg_dump` and source table/owner inspection, and records ciphertext SHA-256;
5. starts a separate empty PostgreSQL instance on tmpfs;
6. restores all eight databases with their service owners;
7. requires exact public-table row counts and relation ownership;
8. checks local RPO at most 24 hours and restore RTO at most 30 minutes;
9. removes the disposable identity, encrypted dumps, and Compose volumes.

The only retained local evidence is the ignored, bounded report at `tmp/backup-restore-last-report.json`. It contains timings, counts, and integrity status, never database rows, credentials, paths containing secrets, or encryption keys.

## Backup Safety

- Never write a plaintext dump to disk. `backupctl backup` must stream `pg_dump` output into age.
- Use a recipient file or approved public recipient. The production private identity must be held separately from both the database and backup storage.
- Do not expose a password-bearing URI in process arguments, shell history, CI logs, metadata, or tickets.
- Store one artifact per database. Do not replace service ownership with an application-wide superuser archive.
- Copy encrypted artifacts off-host only to an approved immutable store with access audit and retention controls.

## Restore Procedure

1. Declare an incident owner and choose an isolated empty target cluster.
2. Provision the eight database roles and empty databases from reviewed infrastructure code. Do not restore passwords or cluster-global grants from an archive.
3. Verify the metadata and ciphertext SHA-256 before decryption.
4. Confirm the metadata database exactly matches both the connection URL and `current_database()`. Require zero non-system relations, routines, and user-defined types before `pg_restore` starts.
5. Stream decryption directly into `pg_restore` with `--single-transaction`, `--no-owner`, `--no-acl`, and the exact service owner. Never use `--clean` or `--if-exists`.
6. Run `backupctl inspect` against the restored database, then `backupctl compare` against the inspection created inside the exported backup snapshot.
7. Require no relation-owner violations and exact table-count equality.
8. Run service migrations in status/check mode, owner health checks, and business reconciliation before any cutover.
9. Record observed backup age and total restore time. Missing an approved RPO/RTO is an incident even if the data is usable.

Do not connect a restored database to production services until payment, entitlement, provisioning, notification, and audit owners sign off. A restore can replay old pending work; outbox/inbox and idempotency records must remain intact.

## Failure Handling

- Integrity mismatch: quarantine the artifact; do not attempt restore.
- Decryption failure: verify key custody and recipient metadata without printing the identity.
- `pg_restore` failure: discard the isolated target, correct the cause, and restart from an empty database.
- Non-empty target: stop before decryption/restore, preserve the target for investigation, and provision a new empty database.
- Owner mismatch or row-count mismatch: fail the drill. Do not repair ownership or counts silently.
- Lost private key: the artifact is unrecoverable by design. Escalate through the approved key-custody process.
- Cleanup failure: treat the drill as failed and remove the verified `tmp/backup-drill-*` path before another run. `make backup-cleanup-test` and `make filesystem-secret-scan` must pass.

## Production Blockers

- Approved RPO/RTO and backup frequency.
- Off-host immutable storage and retention.
- Hardware-backed or otherwise approved age identity custody and rotation.
- Backup success/freshness monitoring with a real Alertmanager receiver.
- HA/PITR design, restore access control, and a production-scale disaster exercise.
