# ADR 0017: Credential Material Delivery and Revoke Lifecycle

Status: Accepted

Date: 2026-07-12

## Context

Kafka commands cannot contain VLESS UUIDs, subscription tokens, REALITY private keys, or other sensitive material. At the same time, provisioning must receive enough material to add or remove credentials on Xray nodes. Revoke also needs result events so access-service can know whether credentials were removed from every assigned node.

## Decision

Provisioning command messages contain only:

- `operation_id`;
- `credential_id`;
- `desired_revision`.

When handling `access.provision.request.v1` or `access.revoke.request.v1`, `provisioning-service` calls access-service over mTLS:

```text
GET /internal/v1/credentials/{credential_id}/provisioning-material
```

Only `provisioning-service` identity may call this endpoint.

`access-service` stores VLESS client UUID encrypted at rest using versioned envelope encryption. The endpoint returns only the minimum required material for provisioning:

- credential ID;
- desired revision;
- protocol;
- VLESS client UUID;
- target node IDs/roles and public endpoint data;
- REALITY public parameters required for client URI rendering.

REALITY private keys remain only on VPN nodes and are never returned by access-service or sent through Kafka.

Revoke lifecycle:

- Access enters `revoking`.
- `access-service` publishes `access.revoke.request.v1`.
- `provisioning-service` removes the credential from all assigned nodes idempotently.
- Success publishes `access.revoke.succeeded.v1`.
- Terminal or operator-actionable failure publishes `access.revoke.failed.v1`.
- `access-service` consumes result events and marks access `revoked` only after all assigned nodes confirm removal.
- Reconciliation periodically compares access state, provisioning allocations, and node actual state.
- Terminal revoke failures create alerts and require operator escalation/runbook.

## Consequences

- AsyncAPI must include provision/revoke request and result events.
- Kafka schemas must be checked to ensure no credential material leaks.
- Stage 6 must include revoke e2e tests, duplicate command tests, and reconciliation tests.
- Admin views must expose `revoking` and terminal failure states without revealing credential material.
