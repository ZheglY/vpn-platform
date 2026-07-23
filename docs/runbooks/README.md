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
- [Support and abuse](support-abuse.md)

Required before production:

- Payment webhook failures.
- Kafka consumer lag and production alert routing for DLQ replay. Local replay is covered by the provisioning runbook.
- Outbox stuck.
- DB pool exhaustion.
- Expired mTLS certificate. See [development mTLS](dev-mtls.md) for the local certificate profile.
- Production node heartbeat alert routing. Local diagnosis is covered by the provisioning runbook.
- Production capacity alert routing. Local diagnosis is covered by the provisioning runbook.
- Production Xray/systemd ownership. Local rollback is covered by the provisioning runbook.
- Leaked subscription token. See [access delivery](access-delivery.md).
- Backup/restore failure.
- Support and abuse intake.
