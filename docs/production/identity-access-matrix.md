# Production Identity And Access Matrix

The machine source is `deploy/production/service-bindings.json`. All grants are
deny-by-default and environment-specific. Staging and production credentials,
trust roots, database roles, Kafka principals, and consumer groups never overlap.

## Service Mapping

| Service | Database runtime | Kafka principal and group | mTLS identity |
|---|---|---|---|
| identity-service | identity | none | `spiffe://<trust>/ns/<env>/sa/identity-service` |
| catalog-service | catalog | none | `spiffe://<trust>/ns/<env>/sa/catalog-service` |
| billing-service | billing | producer `<env>.billing-service` | `spiffe://<trust>/ns/<env>/sa/billing-service` |
| subscription-service | subscription | `<env>.subscription-service`, group `<env>.subscription-service-v1` | `spiffe://<trust>/ns/<env>/sa/subscription-service` |
| access-service | access | `<env>.access-service`, group `<env>.access-service-v1` | `spiffe://<trust>/ns/<env>/sa/access-service` |
| provisioning-service | provisioning | `<env>.provisioning-service`, group `<env>.provisioning-service-v1` | `spiffe://<trust>/ns/<env>/sa/provisioning-service` |
| node-agent | none | none | `spiffe://<trust>/ns/<env>/sa/node-agent-<node_id>` |
| telegram-bot | none | none | `spiffe://<trust>/ns/<env>/sa/telegram-bot` |
| notification-service | notification | `<env>.notification-service`, group `<env>.notification-service-v1` | `spiffe://<trust>/ns/<env>/sa/notification-service` |
| admin-service | admin | none | `spiffe://<trust>/ns/<env>/sa/admin-service` |

Topic produce/consume allowlists are exact in the machine contract and mirror
`contracts/events/asyncapi.yaml`; no wildcard topic or group permission is
allowed. Kafka uses the service client certificate as its principal.

## Database Roles

Each of the eight owner databases has four non-interchangeable login roles:

| Role suffix | Allowed | Explicitly denied |
|---|---|---|
| `_runtime` | service DML and exact sequences/functions | DDL, role grant, another schema/database, backup/restore |
| `_migrator` | reviewed forward migrations for one owner schema | business runtime, another owner, role administration |
| `_backup` | read-only consistent export and required catalog inspection | writes, DDL, restore, application traffic |
| `_restore` | isolated empty-target restore under explicit approval | production target, runtime traffic, cross-owner restore |

Schema ownership is separate from all four login roles. `backupctl`, `migrate`,
retention, bootstrap, and application runtime never share a credential. Restore
credentials are unavailable in the production runtime environment.

## Additional Identities

- Observability has dedicated scrape and OTLP client identities and no business
  API or database authority.
- Health probes use client-only health identities and cannot mutate state.
- Administrators use short-lived individual SPIFFE identities; `admin-service`
  still performs local RBAC and immutable audit.
- Deployment may read release/config references and pull accepted digests but
  cannot publish/overwrite artifacts. Build/publish cannot deploy.
- Backup may write immutable encrypted objects but cannot read production keys;
  restore requires a separately approved isolated environment.
- PKI issuer, revocation operator, and break-glass identities are distinct and
  audited. No wildcard SPIFFE URI is accepted by application authorization.
