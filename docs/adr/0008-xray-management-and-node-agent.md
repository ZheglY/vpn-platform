# ADR 0008: Xray Management Through Node-Agent

Status: Accepted

Date: 2026-07-12

## Context

VPN data-plane nodes run Xray-core. The central platform should not directly mutate Xray files over ad hoc SSH commands, and VPN nodes should not connect directly to shared Kafka or PostgreSQL over the public internet.

## Decision

Run a small Go `node-agent` on each VPN node.

The provisioning-service sends desired credential state to node-agent over mTLS on the private management network. The agent:

- accepts only allowlisted fields;
- never executes a shell command received from the network;
- applies operations idempotently using operation ID and desired revision;
- builds a candidate Xray config;
- validates the candidate config;
- atomically swaps config after validation;
- reloads Xray with timeout;
- preserves last-known-good config;
- reports aggregate health and redacted operation diagnostics.

The initial protocol is VLESS + REALITY.

Kafka provisioning commands contain only operation ID, credential ID, and desired revision. Provisioning-service retrieves the minimum required credential material from access-service over mTLS as described in ADR 0017. REALITY private keys remain on VPN nodes.

## Consequences

- Stage 6 must include real local Xray e2e tests.
- Production VPS must not be connected before threat review and e2e revoke pass.
- Xray-core version must be pinned after checking official releases and security notes.
- Provisioning and revoke result events must allow access-service to reconcile applied state.
