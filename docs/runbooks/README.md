# Runbooks

Implemented local and sandbox runbooks are listed below. Production operations still require environment-specific ownership, alert routing, and credentials.

- [Local development](local-development.md)
- [Development mTLS](dev-mtls.md)
- [YooKassa sandbox and billing recovery](yookassa-sandbox.md)
- [Subscription lifecycle recovery](subscription-lifecycle.md)
- [Access delivery and leaked-token response](access-delivery.md)
- [Support and abuse](support-abuse.md)

Required before production:

- Payment webhook failures.
- Kafka consumer lag and DLQ replay.
- Outbox stuck.
- DB pool exhaustion.
- Expired mTLS certificate. See [development mTLS](dev-mtls.md) for the local certificate profile.
- Node heartbeat loss.
- Capacity threshold.
- Xray reload failure.
- Leaked subscription token. See [access delivery](access-delivery.md).
- Backup/restore failure.
- Support and abuse intake.
