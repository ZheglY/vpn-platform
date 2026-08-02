# Architecture Decision Records

ADRs are immutable decision records. If a decision changes after implementation starts, create a new ADR that supersedes the old one.

| ADR | Title | Status |
|---|---|---|
| [0001](0001-repository-model-and-go-layout.md) | Repository model and Go layout | Accepted |
| [0002](0002-sync-http-and-async-kafka.md) | Synchronous HTTP and asynchronous Kafka boundaries | Accepted |
| [0003](0003-database-per-service.md) | Database-per-service isolation | Accepted |
| [0004](0004-kafka-client-and-message-naming.md) | Kafka client, message naming, and delivery rules | Accepted |
| [0005](0005-goose-sql-migrations.md) | Goose SQL migrations | Accepted |
| [0006](0006-money-and-plan-pricing.md) | Money representation and plan pricing | Accepted |
| [0007](0007-token-storage-and-subscription-endpoint.md) | Subscription token storage and endpoint behavior | Accepted |
| [0008](0008-xray-management-and-node-agent.md) | Xray management through node-agent | Accepted |
| [0009](0009-mtls-and-wireguard-management-plane.md) | mTLS and private WireGuard management plane | Accepted |
| [0010](0010-yookassa-sandbox-payment-verification.md) | YooKassa sandbox payment verification | Accepted |
| [0011](0011-refund-and-revocation-policy.md) | Refund and revocation policy | Accepted |
| [0012](0012-admin-cli-and-internal-api.md) | Admin CLI and internal API | Accepted |
| [0013](0013-observability-retention-and-privacy.md) | Observability, retention, and privacy | Accepted |
| [0014](0014-provisioning-readiness-active-degraded.md) | Provisioning readiness and degraded state | Accepted |
| [0015](0015-internal-authentication.md) | Internal authentication and service identity | Accepted |
| [0016](0016-one-time-subscription-url-issuance.md) | One-time subscription URL issuance after provisioning | Accepted |
| [0017](0017-credential-material-and-revoke-lifecycle.md) | Credential material delivery and revoke lifecycle | Accepted |
| [0018](0018-spiffe-verified-chain-and-local-mtls.md) | SPIFFE verified-chain identity and local mTLS | Accepted |
| [0019](0019-stage3-billing-state-and-idempotency.md) | Stage 3 billing state and idempotency | Accepted |
| [0020](0020-stage4-subscription-lifecycle.md) | Stage 4 subscription lifecycle | Accepted |
| [0021](0021-stage5-access-token-and-profile-delivery.md) | Stage 5 access token and profile delivery | Accepted |
| [0022](0022-stage5-ordering-revoke-and-public-edge-hardening.md) | Stage 5 ordering, revoke proof, and public edge hardening | Accepted |
| [0023](0023-stage6-provisioning-placement-and-node-convergence.md) | Stage 6 provisioning placement and node convergence | Accepted |
| [0024](0024-stage6-outcome-ordering-and-generation-recovery.md) | Stage 6 outcome ordering and generation recovery | Accepted |
| [0025](0025-stage7-notification-ordering-and-telegram-delivery.md) | Stage 7 notification ordering and Telegram delivery | Accepted for Stage 7 implementation |
| [0026](0026-stage7-admin-mtls-rbac-actions-and-audit.md) | Stage 7 admin mTLS, RBAC, actions, and audit | Accepted for Stage 7 implementation |
| [0027](0027-stage8-observability-metrics-and-local-stack.md) | Stage 8 observability metrics and local stack | Accepted for Stage 8 implementation |
| [0028](0028-stage8-bounded-operational-metrics.md) | Stage 8 bounded operational metrics | Accepted for Stage 8 implementation |
| [0029](0029-stage8-trace-context-and-log-pipeline.md) | Stage 8 trace context and log pipeline | Accepted for Stage 8 implementation |
| [0030](0030-stage8-sli-slo-and-alert-routing.md) | Stage 8 SLI, SLO, and alert routing | Accepted for Stage 8 implementation |
| [0031](0031-stage8-owner-retention-and-legal-holds.md) | Stage 8 owner retention and legal holds | Accepted for Stage 8 implementation |
| [0032](0032-stage8-encrypted-backup-and-restore.md) | Stage 8 encrypted backup and restore | Accepted for Stage 8 implementation |
| [0033](0033-stage8-durable-sli-retention-and-restore-corrections.md) | Stage 8 durable SLI, retention, and restore corrections | Accepted for Stage 8 implementation |
| [0034](0034-stage8-vpn-node-host-hardening.md) | Stage 8 VPN node host hardening | Accepted for Stage 8 implementation |
| [0035](0035-stage8-secret-rotation.md) | Stage 8 secret rotation | Accepted for Stage 8 implementation |
| [0036](0036-stage8-resilience-budgets-and-failure-drills.md) | Stage 8 resilience budgets and failure drills | Accepted for Stage 8 implementation |
| [0037](0037-stage8-release-sbom-and-provenance.md) | Stage 8 release SBOM and provenance | Accepted for Stage 8 implementation |
| [0038](0038-stage9-production-readiness-evidence.md) | Stage 9 production-readiness evidence and fail-closed gates | Accepted for Stage 9 review |
| [0039](0039-stage9-xray-first-boot-and-control.md) | Stage 9 Xray first boot and fixed systemd control | Accepted for Stage 9 remediation |
| [0040](0040-stage9-release-inventory-and-license-gate.md) | Stage 9 release inventory and license publication gate | Accepted for Stage 9 remediation |
| [0041](0041-stage9-evidence-freshness-and-decision-binding.md) | Stage 9 evidence freshness and decision binding | Accepted for Stage 9 remediation |
| [0042](0042-stage9-provider-neutral-production-contracts.md) | Stage 9 provider-neutral production contracts and offline preflight | Accepted for Stage 9 remediation |
