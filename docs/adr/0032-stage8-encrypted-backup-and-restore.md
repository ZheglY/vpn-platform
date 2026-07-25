# ADR 0032: Stage 8 encrypted backup and restore

- Status: Accepted for Stage 8 implementation
- Date: 2026-07-25
- Owners: platform, database owners, and operations
- Security impact: High
- Contract impact: maintenance tooling only
- Extends: ADR 0003 and ADR 0013

## Context

Database-per-service isolation requires recoverable backups without combining databases into one privileged application store. A raw `pg_dump` file on the host would expose payment, entitlement, audit, and encrypted credential metadata. A backup is not trustworthy until it has been restored into an empty environment and checked for data and ownership.

No production key custodian, off-host storage provider, or recovery target has been approved. The Stage 8 slice therefore needs reproducible local evidence without creating production secrets.

## Decision

1. `backupctl` produces one PostgreSQL custom-format artifact per service database with PostgreSQL 18 client tools. `pg_dump --no-owner --no-acl` stdout is streamed directly into authenticated `age` X25519 encryption; no plaintext dump is written to a host or container filesystem.
2. The tool uses `filippo.io/age` v1.3.1. Local drill key generation creates an identity file with mode `0600` and a separate public recipient file. The private identity and encrypted drill artifacts are temporary, ignored, and removed after the drill.
3. Database connection secrets are parsed into libpq environment variables. Password-bearing connection URIs are never added to process arguments, reports, or errors. Subprocess stderr is bounded and mapped to safe failure classes.
4. Metadata stores a format version, database name, expected owner, creation time, fixed tool identifier, duration, ciphertext byte count, and ciphertext SHA-256. It contains no DSN, host, user, password, key, row data, or plaintext digest. Restore verifies metadata and the full ciphertext hash before decryption.
5. Restore streams age plaintext directly to `pg_restore --clean --if-exists --single-transaction --no-owner --no-acl --role <owner>` for an explicitly named empty target database. Database roles and empty databases are provisioned separately by infrastructure; global roles and passwords are not backed up.
6. `inspect` records exact row counts for every public table and verifies public relation ownership. `compare` requires the restored inspection to match the source inspection exactly and refuses any owner violation. Reports contain table names and counts only, never row data or identifiers.
7. The local clean-room drill covers all eight owner databases, starts a second PostgreSQL instance on tmpfs, restores every encrypted artifact, compares row counts and ownership, and removes its volumes and temporary artifacts on success or failure.
8. Temporary Stage 8 evidence targets are RPO at most 24 hours and restore RTO at most 30 minutes for the local eight-database drill. The command timeout is 30 minutes. These are portfolio acceptance targets, not approved production commitments.
9. Production scheduling, off-host immutable storage, retention, key custody/rotation, access audit, HA/PITR, and approved RPO/RTO remain production-readiness blockers. A production private key must not be stored beside its backups.

## Consequences

- Backup artifacts are encrypted before reaching persistent storage and are independently integrity checked.
- Restore evidence validates all service databases and relation owners, rather than treating a successful `pg_dump` exit as recovery proof.
- Restoring roles separately preserves database-per-service ownership and avoids copying passwords or cluster-global grants into backup artifacts.
- Exact row-count comparison proves the current logical drill but does not replace WAL-based point-in-time recovery, application reconciliation, or a disaster exercise against production-scale data.

## Rejected alternatives

- Write a plaintext dump and encrypt it afterward: rejected because crash cleanup could leave recoverable plaintext.
- Use one superuser dump containing every database and global role: rejected because it expands credential and recovery blast radius.
- Treat checksum verification without restore as sufficient: rejected because it cannot prove schema compatibility, ownership, or usable data.
- Commit or persist the local drill identity: rejected because test keys must remain disposable and production custody is undecided.
