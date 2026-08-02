# Production Configuration Inventory

Status: provider-neutral contract complete; environment values not approved

The deployer combines the common fields below with the component row. The
machine schemas are in `deploy/environments/`; values classified as secret,
key, or certificate are references only. Runtime material is delivered as
owner-readable files or process environment by the approved secret integration.

## Classification

| Class | Exact values | Storage and logging rule |
|---|---|---|
| public | `CATALOG_SEED_*`, `CONSENT_DOCUMENT_TYPE`, `CONSENT_VERSION`, `HAPP_PROFILE_TITLE`, `HAPP_PROFILE_UPDATE_HOURS`, `HAPP_SUPPORT_URL`, `PAYMENT_RETURN_URL`, `SUBSCRIPTION_PUBLIC_BASE_URL`, `TERMS_URL`, `TELEGRAM_API_BASE_URL`, `YOOKASSA_BASE_URL` | Reviewed environment config; may be published, but raw request paths are still excluded. |
| config | all remaining non-credential names listed below, including endpoints, IDs, limits, timeouts, topic groups, paths, and retention periods | Environment config store with owner, reviewer, audit, version, and rollback. Topology values are not emitted to public logs. |
| secret | `DATABASE_URL`, `REDIS_PASSWORD`, `TELEGRAM_BOT_TOKEN`, `TELEGRAM_WEBHOOK_SECRET`, `YOOKASSA_SHOP_ID`, `YOOKASSA_SECRET_KEY` | Secret manager reference; never Git, chat, CLI arguments, logs, traces, metrics, or evidence. |
| key | `ACCESS_CREDENTIAL_KEYS`, `ACCESS_TOKEN_HMAC_KEYS`, `ACCESS_TOKEN_HMAC_KEY_BASE64`, `ADMIN_CLIENT_KEY_FILE`, `CLIENT_TLS_KEY_FILE`, `HEALTHCHECK_CLIENT_KEY_FILE`, `IDENTITY_CLIENT_KEY_FILE`, `INTERNAL_CLIENT_KEY_FILE`, `INTERNAL_SERVER_TLS_KEY_FILE`, `KAFKA_TLS_KEY_FILE`, `SERVER_TLS_KEY_FILE`, `XRAY_REALITY_PRIVATE_KEY_FILE` | Key manager/HSM or staged owner-only file; versioned rotation and destruction record required. |
| certificate | `ADMIN_CA_FILE`, `ADMIN_CLIENT_CERT_FILE`, `CLIENT_CA_FILE`, `CLIENT_TLS_CERT_FILE`, `HEALTHCHECK_CA_FILE`, `HEALTHCHECK_CLIENT_CERT_FILE`, `IDENTITY_CLIENT_CERT_FILE`, `IDENTITY_SERVER_CA_FILE`, `INTERNAL_CLIENT_CA_FILE`, `INTERNAL_CLIENT_CERT_FILE`, `INTERNAL_SERVER_CA_FILE`, `INTERNAL_SERVER_TLS_CERT_FILE`, `KAFKA_TLS_CA_FILE`, `KAFKA_TLS_CERT_FILE`, `REDIS_TLS_CA_FILE`, `SERVER_CA_FILE`, `SERVER_TLS_CERT_FILE` | PKI inventory/reference; trust bundle and leaf lifecycle are independently versioned. |

`ACCESS_CREDENTIAL_KEY_VERSION` and `ACCESS_TOKEN_HMAC_KEY_VERSION` are config,
not key material. A path ending in `_KEY_FILE` is classed as key even though the
environment contains only a path. `DATABASE_URL` is secret because it currently
contains the runtime database credential.

## Common Runtime Fields

| Area | Names |
|---|---|
| process | `APP_ENV`, `LOG_LEVEL`, `HTTP_ADDR`, `HTTP_MAX_BODY_BYTES` |
| HTTP bounds | `HTTP_READ_HEADER_TIMEOUT`, `HTTP_READ_TIMEOUT`, `HTTP_WRITE_TIMEOUT`, `HTTP_IDLE_TIMEOUT`, `HTTP_SHUTDOWN_TIMEOUT`, `OUTBOUND_TIMEOUT` |
| service identity | `MTLS_TRUST_DOMAIN`, `MTLS_NAMESPACE`, `INTERNAL_AUTH_MODE`, `SERVER_TLS_CERT_FILE`, `SERVER_TLS_KEY_FILE`, `CLIENT_CA_FILE` |
| outbound identity | `INTERNAL_CLIENT_CERT_FILE`, `INTERNAL_CLIENT_KEY_FILE`, `INTERNAL_SERVER_CA_FILE` |
| health identity | `HEALTHCHECK_CLIENT_CERT_FILE`, `HEALTHCHECK_CLIENT_KEY_FILE`, `HEALTHCHECK_CA_FILE` |
| PostgreSQL | `DATABASE_URL`; staging/production requires `sslmode=verify-full` and the component's runtime role |
| Kafka | `KAFKA_BROKERS`, `KAFKA_CONSUMER_GROUP`, `KAFKA_TLS_CA_FILE`, `KAFKA_TLS_CERT_FILE`, `KAFKA_TLS_KEY_FILE` |
| Redis | `REDIS_ADDR`, `REDIS_PASSWORD`, `REDIS_DB`, `REDIS_TLS_CA_FILE` |

## Component Fields

| Component | Additional exact names |
|---|---|
| identity-service | common process/HTTP, PostgreSQL, service identity and health fields |
| catalog-service | `CATALOG_SEED_PLAN_ID`, `CATALOG_SEED_NAME`, `CATALOG_SEED_DURATION_DAYS`, `CATALOG_SEED_GRACE_HOURS`, `CATALOG_SEED_AMOUNT_MINOR`, `CATALOG_SEED_CURRENCY`, `CATALOG_SEED_REGIONS` |
| billing-service | `IDENTITY_BASE_URL`, `CATALOG_BASE_URL`, `YOOKASSA_BASE_URL`, `YOOKASSA_SHOP_ID`, `YOOKASSA_SECRET_KEY`, `PAYMENT_RETURN_URL`, `YOOKASSA_TIMEOUT`, `YOOKASSA_CREATE_WINDOW`, `BILLING_WORKER_POLL_INTERVAL`, `BILLING_WORKER_RETRY_DELAY`, `BILLING_WORKER_LEASE` plus Kafka |
| subscription-service | `BILLING_BASE_URL`, `SUBSCRIPTION_WORKER_POLL_INTERVAL`, `SUBSCRIPTION_WORKER_RETRY_DELAY`, `SUBSCRIPTION_WORKER_LEASE` plus Kafka |
| access-service | `ACCESS_CREDENTIAL_KEY_VERSION`, `ACCESS_CREDENTIAL_KEYS`, `ACCESS_TOKEN_HMAC_KEY_VERSION`, `ACCESS_TOKEN_HMAC_KEYS`, `ACCESS_TOKEN_HMAC_KEY_BASE64`, `ACCESS_PUBLIC_IP_RATE_LIMIT`, `ACCESS_PUBLIC_TOKEN_RATE_LIMIT`, `ACCESS_PUBLIC_RATE_WINDOW`, `SUBSCRIPTION_PUBLIC_BASE_URL`, `HAPP_PROFILE_TITLE`, `HAPP_SUPPORT_URL`, `HAPP_PROFILE_UPDATE_HOURS`, `ACCESS_WORKER_POLL_INTERVAL`, `ACCESS_WORKER_RETRY_DELAY`, `ACCESS_WORKER_LEASE` plus Kafka and Redis |
| provisioning-service | `ACCESS_BASE_URL`, `SUBSCRIPTION_BASE_URL`, `PROVISIONING_WORKER_POLL_INTERVAL`, `PROVISIONING_WORKER_RETRY_DELAY`, `PROVISIONING_WORKER_LEASE`, `PROVISIONING_HEALTH_INTERVAL`, `PROVISIONING_HEALTH_STALE_AFTER`, `PROVISIONING_RECONCILIATION_INTERVAL`, `PROVISIONING_RECONCILIATION_BATCH`, `PROVISIONING_MAX_ATTEMPTS`, `PROVISIONING_NODE_SEED_FILE` plus Kafka |
| node-agent | `NODE_ID`, `NODE_HEALTH_CALLER_IDENTITY`, `NODE_STATE_DIRECTORY`, `NODE_OPERATION_JOURNAL_LIMIT`, `XRAY_BINARY`, `XRAY_CONFIG_DIRECTORY`, `XRAY_MANAGER_MODE`, `XRAY_CONTROL_COMMAND`, `XRAY_CONTROL_ARGS`, `XRAY_REALITY_PRIVATE_KEY_FILE`, `XRAY_REALITY_TARGET`, `XRAY_REALITY_SERVER_NAMES`, `XRAY_REALITY_SHORT_IDS`, `XRAY_LISTEN_ADDRESS`, `XRAY_LISTEN_PORT`, `XRAY_VALIDATE_TIMEOUT`, `XRAY_RELOAD_TIMEOUT`, `XRAY_STARTUP_GRACE`; systemd supplies `NOTIFY_SOCKET` |
| telegram-bot | `INTERNAL_HTTP_ADDR`, `IDENTITY_BASE_URL`, `CATALOG_BASE_URL`, `BILLING_BASE_URL`, `IDENTITY_AUTH_MODE`, `DELIVERY_AUTH_MODE`, `IDENTITY_CLIENT_CERT_FILE`, `IDENTITY_CLIENT_KEY_FILE`, `IDENTITY_SERVER_CA_FILE`, `INTERNAL_SERVER_TLS_CERT_FILE`, `INTERNAL_SERVER_TLS_KEY_FILE`, `INTERNAL_CLIENT_CA_FILE`, `TELEGRAM_API_BASE_URL`, `TELEGRAM_BOT_TOKEN`, `TELEGRAM_WEBHOOK_SECRET`, `CONSENT_VERSION`, `TERMS_URL`, `TELEGRAM_DEDUPE_TTL`, `TELEGRAM_PROCESSING_TTL`, `TELEGRAM_DEDUPE_CLEANUP_TIMEOUT`, `TELEGRAM_FSM_TTL`, `TELEGRAM_WEBHOOK_RATE_LIMIT`, `TELEGRAM_WEBHOOK_RATE_WINDOW` plus Redis |
| notification-service | `IDENTITY_BASE_URL`, `SUBSCRIPTION_BASE_URL`, `ACCESS_BASE_URL`, `TELEGRAM_BOT_BASE_URL`, `CLIENT_TLS_CERT_FILE`, `CLIENT_TLS_KEY_FILE`, `SERVER_CA_FILE`, `CONSENT_DOCUMENT_TYPE`, `CONSENT_VERSION`, `NOTIFICATION_OUTBOUND_TIMEOUT`, `NOTIFICATION_WORKER_POLL_INTERVAL`, `NOTIFICATION_RETRY_BASE`, `NOTIFICATION_WORKER_LEASE`, `NOTIFICATION_MAX_ATTEMPTS` plus Kafka |
| admin-service | `IDENTITY_BASE_URL`, `BILLING_BASE_URL`, `SUBSCRIPTION_BASE_URL`, `ACCESS_BASE_URL`, `PROVISIONING_BASE_URL`, `NOTIFICATION_BASE_URL`, `CLIENT_TLS_CERT_FILE`, `CLIENT_TLS_KEY_FILE`, `SERVER_CA_FILE`, `ADMIN_OUTBOUND_TIMEOUT` |
| admin-cli/bootstrap | `ADMIN_BASE_URL`, `ADMIN_CLIENT_CERT_FILE`, `ADMIN_CLIENT_KEY_FILE`, `ADMIN_CA_FILE`, `ADMIN_SEED_FILE`, `APP_ENV`, `MTLS_TRUST_DOMAIN`, bootstrap migrator `DATABASE_URL` |
| retention jobs | owner-specific `DATABASE_URL` and `ACCESS_SECURITY_AUDIT_RETENTION_DAYS`, `ADMIN_AUDIT_RETENTION_DAYS`, `BILLING_WEBHOOK_RETENTION_DAYS`, `NOTIFICATION_DLQ_RETENTION_DAYS`, or `PROVISIONING_DIAGNOSTIC_RETENTION_DAYS` |
| migrate | `DATABASE_URL`, `MIGRATIONS_DIR`, `MIGRATION_COMMAND`; run with migrator role only |
| backupctl | `DATABASE_URL`; recipient/identity and artifact paths are explicit non-logged flags and use backup/isolated restore roles |
| loadprobe | `LOAD_PROBE_URL`, `LOAD_PROBE_CA_FILE`, `LOAD_PROBE_DURATION`, `LOAD_PROBE_RPS`, `LOAD_PROBE_CONCURRENCY`, `LOAD_PROBE_EXPECTED_STATUS`, `LOAD_PROBE_EXPECTED_STATUSES`, `LOAD_PROBE_MAX_ERROR_RATIO`, `LOAD_PROBE_P95_BUDGET_MS`; never target an unapproved environment |

## Source Of Truth And Change

- Schemas/templates: `deploy/environments/`.
- Identity/role/ACL contract: `deploy/production/service-bindings.json`.
- Release images: `deploy/release/images.json`.
- Secret/key/certificate values: approved external manager, referenced by the
  environment document, resolved only by an authenticated deployment identity.
- Every change requires owner and reviewer, immutable audit, preflight against
  the reviewed commit, canary, rollback reference, and refreshed readiness
  evidence. The repository intentionally contains no valid production file.
