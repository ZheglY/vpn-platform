# ADR 0009: mTLS and Private WireGuard Management Plane

Status: Accepted

Date: 2026-07-12

## Context

Provisioning credentials to VPN nodes is a high-impact operation. Exposing node-agent management endpoints to the public internet would create an unacceptable attack surface.

## Decision

Use a private WireGuard management network between control plane and VPN nodes.

Use mTLS between provisioning-service and node-agent. Node-agent endpoints must not be publicly reachable.

Firewall policy is default deny. Only required management ports are allowed on the management interface.

## Consequences

- Certificate issuance, rotation, expiry alerting, and revocation runbooks are required before production.
- Node-agent rejects clients without trusted mTLS identity.
- Internal HTTP authentication must not rely on spoofable headers such as `X-Internal`.
