# Runbooks

Implemented local and sandbox runbooks are listed below. Production operations still require environment-specific ownership, alert routing, and credentials.

- [Local development](local-development.md)
- [Development mTLS](dev-mtls.md)
- [YooKassa sandbox and billing recovery](yookassa-sandbox.md)
- [Subscription lifecycle recovery](subscription-lifecycle.md)
- [Access delivery and leaked-token response](access-delivery.md)
- [Provisioning, node health, DLQ replay, and Xray recovery](provisioning.md)
- [Telegram notifications and notification recovery](notifications.md)
- [Administrator CLI, RBAC, action recovery, and audit](admin-operations.md)
- [Prometheus, Grafana, metrics privacy, and HTTP alerts](observability.md)
- [Owner-local data retention and legal holds](data-retention.md)
- [Encrypted database backup and clean restore](backup-restore.md)
- [VPN node host hardening and rollback](vpn-node-hardening.md)
- [mTLS, provider, Access, and REALITY secret rotation](secret-rotation.md)
- [Bounded load and resilience drills](resilience.md)
- [Release images, SBOMs, scans, and keyless provenance](release.md)
- [Support and abuse](support-abuse.md)
- [Incident response](incident-response.md)
- [Production-readiness game day](game-day.md)
- [Production rollback and disaster recovery](production-rollback.md)
- [Provider-neutral deployment and rollback contract](../production/deployment-and-rollback.md)
- [Production security and operations contracts](../production/security-operations-contracts.md)
- [Production configuration inventory](../production/configuration-inventory.md)
- [Production identity and access matrix](../production/identity-access-matrix.md)

Required before production:

- Payment webhook failures.
- Kafka consumer lag and production alert routing for DLQ replay. Local replay is covered by the provisioning runbook.
- Outbox stuck.
- DB pool exhaustion.
- Expired mTLS certificate. See [development mTLS](dev-mtls.md) for the local certificate profile.
- Production node heartbeat alert routing. Local scrape diagnosis is covered by the observability and provisioning runbooks.
- Production capacity alert routing. Local diagnosis is covered by the provisioning runbook.
- Production Xray/systemd ownership. Local host-state validation and rollback are covered by the node-hardening runbook.
- Leaked subscription token. See [access delivery](access-delivery.md).
- Production backup/restore monitoring, storage, and key custody. The local encrypted drill is covered by the backup runbook.
- Support and abuse intake.
