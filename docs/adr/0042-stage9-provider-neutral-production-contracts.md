# ADR 0042: Provider-Neutral Production Contracts and Offline Preflight

Status: Accepted for Stage 9 remediation

Date: 2026-08-02

## Context

Local Compose proves application behavior but is not a production deployment
specification. Production values, infrastructure providers, credentials,
certificate authorities, regions, domains, budgets, and alert contacts remain
owner decisions. A template containing placeholders must never be mistaken for
an approved environment, and a green local smoke test must never imply that
provider infrastructure exists.

The runtime also needs a uniform boundary that rejects development credentials,
loopback endpoints, plaintext data-store transports, and local trust identities
when `APP_ENV` is `staging` or `production`.

## Decision

1. `deploy/environments/` owns strict, separate staging and production JSON
   schemas and intentionally incomplete templates. Templates contain only
   references and placeholders, never credential values.
2. `deploy/production/service-bindings.json` is the machine-readable mapping of
   service to owner database, four database roles, Kafka principal/topic/group
   permissions, and environment-scoped SPIFFE identity.
3. Environment values are classified as `public`, `config`, `secret`, `key`, or
   `certificate`. Secret, key, and certificate fields in the environment
   document contain opaque references only.
4. `productionpreflight` performs a strict offline check: exact reviewed commit,
   exact 19-image inventory, immutable `sha256:` digests, safe HTTPS hosts,
   complete references, distinct on-call contacts, RPO/RTO, capacity settings,
   and the service-binding contract. It makes no DNS, HTTP, database, Kafka,
   Redis, registry, secret-manager, or provider connection.
5. Every service invokes a shared startup guard. In staging and production it
   requires an explicit environment-bound mTLS trust domain and namespace,
   rejects development/fake/default credential markers and unsafe endpoints,
   requires PostgreSQL `sslmode=verify-full`, Kafka client mTLS, and Redis TLS.
6. Kafka client certificates identify the per-service broker principal. Redis
   uses TLS 1.3 with CA and server-name validation plus its separate password.
   Local Compose remains plaintext only on its isolated development networks.
7. Deployment is digest-only and follows expand/migrate/contract with an N/N-1
   rollback window. Provider selection and provider-specific IaC require owner
   inputs and a later reviewed decision; Kubernetes is not selected.

## Security Properties

- Validation errors identify fields but never echo credential values.
- Actual secret/key/certificate material is staged at runtime by an approved
  secret/PKI integration and is absent from Git, environment templates, logs,
  traces, metrics, and evidence.
- A missing, unknown, additional, mutable, placeholder, local, or malformed
  value fails closed.
- Preflight success is necessary but not sufficient for deployment and never
  changes the Stage 9 `NO-GO` decision.

## Consequences

- Provider-neutral work can be reviewed and tested before accounts exist.
- A deployer must produce an environment-owned candidate document and resolve
  opaque references through authenticated tooling outside this repository.
- Real HA/PITR, broker ACL denial, PKI revocation, alert delivery, node capacity,
  edge redaction, canary rollback, and full DR still require production-like
  staging evidence.
- Existing production gates remain blocked until owner and infrastructure
  evidence is current and the go/no-go document is regenerated for the final
  reviewed commit.
