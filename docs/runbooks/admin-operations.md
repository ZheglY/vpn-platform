# Administrator Operations

This runbook covers the Stage 7 local admin-service and Go admin-cli. It does not define production certificate issuance, SSO, break-glass access, or key rotation; those are Stage 8/9 blockers.

## Identity and Roles

Admin-service accepts only a verified leaf URI of the form `spiffe://<trust-domain>/ns/<environment>/admin/<principal>`. Actor, roles, and username headers/body fields are ignored. A valid platform certificate with a service URI is forbidden, and an enabled principal still needs the endpoint permission.

| Role | Scope |
|---|---|
| `support_readonly` | identity, consent, subscription, Access, provisioning, notification, DLQ, health, and audit reads |
| `operations` | support reads plus notification retry, subscription revoke, and higher-revision Access recovery |
| `security` | identity, DLQ, health, and audit investigation reads only |
| `finance_readonly` | order/payment status and health reads only |

There is no wildcard or superadmin role. Runtime admin-service cannot grant a role or edit principals. The local bootstrap command validates `deploy/local/admin-principals.json` using the separate migrator credential; never put a production identity in that file.

## CLI Setup

Generate local certificates and point the CLI at the loopback-bound API:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/dev-mtls.ps1
go run ./services/admin/cmd/admin-cli --base-url https://127.0.0.1:8092 `
  --cert secrets/dev-mtls/admin-support-local.crt `
  --key secrets/dev-mtls/admin-support-local.key `
  --ca secrets/dev-mtls/ca.crt health
```

Private keys stay in ignored local files and must not be embedded in command output, tickets, repository configuration, or a binary. The CLI validates the server certificate, uses TLS 1.3, applies a bounded timeout, generates a request ID, rejects forbidden response fields, and exits nonzero on HTTP/action failure.

## Safe Reads

Typed commands include `get-user`, `get-consent`, `get-order`, `get-payment`, `get-subscription`, `get-access`, `get-notification`, `get-provisioning`, `notification-dlq`, `health`, and `audit`. Each maps to one fixed route. The client cannot supply an upstream URL, SQL, Kafka topic, shell command, or raw payload.

Admin output must never contain a subscription URL/token, VLESS client UUID, credential ciphertext, Telegram chat ID, payment confirmation URL/provider ID, REALITY private key, node management URL, or raw owner response. Stop and report a security incident if the CLI rejects an unsafe owner response.

## Notification Retry

```powershell
go run ./services/admin/cmd/admin-cli <mTLS flags> retry-notification `
  --notification-id <uuid> --reason "dependency restored" `
  --idempotency-key notification-retry-20260719-001
```

Use only for `retry` or `permanently_failed` jobs after correcting the cause. Notification-service owns the reset and durable retry record. The same key and request returns the same action; a changed reason/target conflicts.

## Subscription Revoke

```powershell
go run ./services/admin/cmd/admin-cli <mTLS flags> revoke-subscription `
  --subscription-id <uuid> --reason "confirmed abuse case" `
  --reason-code abuse --idempotency-key subscription-revoke-20260719-001
```

Allowed reason codes are `admin_block`, `abuse`, and `deleted`. Subscription-service owns the state transition and transactional lifecycle event. This is not a payment refund and does not alter Billing data. Do not use direct SQL or node operations to accelerate physical revoke.

## Provisioning Recovery

```powershell
go run ./services/admin/cmd/admin-cli <mTLS flags> recover-access `
  --credential-id <uuid> --reason "terminal node failure corrected" `
  --idempotency-key access-recovery-20260719-001
```

Access accepts recovery only for a terminal failed credential with unexpired entitlement. It creates a fresh higher desired revision and the normal provisioning command. It never resets old operation history or returns credential material.

## Action Failure and Crash Recovery

1. Query audit/action status by safe IDs. An `accepted` event proves authorization and durable intent happened before owner execution.
2. A pending action after restart is retried with the same owner action ID/idempotency key. The owner decides exact replay; never create a second key merely because the first response was lost.
3. A failed action stores only a bounded error code. Diagnose through the owner service's safe status/read path and its runbook, not through response bodies or database access.
4. Keep the original human reason stable. Reusing the key with changed input must return conflict.

## Audit Integrity

Each accepted action and final outcome records verified SPIFFE identity, principal, role/permission snapshot, action, target, human reason, safe idempotency hash, request/correlation IDs, bounded result, and PostgreSQL time. Runtime `admin_app` can insert audit through the service transaction but cannot update, delete, or truncate history; triggers also reject mutation.

Do not store secrets in the human reason. Requests containing `vless://`, subscription paths, credential/ciphertext markers, or secret-bearing fields are rejected. Preserve audit rows for investigation; do not grant the runtime role migration ownership.

## Stolen Key or Forged Certificate

1. Disable the exact principal using the reviewed bootstrap/credential-owner procedure; do not add a broader deny role.
2. Revoke/rotate the certificate through the environment PKI process. Local dev CA material is disposable and never production-valid.
3. Review audit by verified SPIFFE identity, time, action, target, and outcome. Exclude secret-bearing application data.
4. A certificate with the wrong URI shape, multiple accepted admin URIs, another trust domain/environment, or only a service identity must not authenticate as an administrator.

## Local Verification

```powershell
make compose-smoke
make stage7-smoke
```

The Stage 7 smoke proves read-only denial, operations allow, replay/conflict, non-admin SPIFFE `403`, safe output/audit, and durability across admin-service restart.
