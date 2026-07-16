# Runbooks

Stage 0 contains runbook requirements and placeholders only. Operational runbooks must be completed before the corresponding production capability is enabled.

Required before production:

- Payment webhook failures.
- Kafka consumer lag and DLQ replay.
- Outbox stuck.
- DB pool exhaustion.
- Expired mTLS certificate. See [development mTLS](dev-mtls.md) for the local certificate profile.
- Node heartbeat loss.
- Capacity threshold.
- Xray reload failure.
- Leaked subscription token.
- Backup/restore failure.
- Support and abuse intake.
