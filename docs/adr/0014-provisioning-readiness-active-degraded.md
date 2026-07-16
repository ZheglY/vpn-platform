# ADR 0014: Provisioning Readiness and Degraded State

Status: Accepted

Date: 2026-07-12

## Context

Each subscription receives one primary node and one failover node. Users should not receive access if the primary node failed, but failover failures should not block initial access when the primary works.

## Decision

Subscription entitlement can become `active` before VPN access is ready. This only means the user has a paid entitlement.

VPN access becomes `ready` only after successful credential application on the primary node. A subscription URL can be issued only in `ready` or `degraded` access state.

If the primary node succeeds and failover fails or is delayed, provisioning state is `degraded`. The user may receive the subscription URL after the explicit one-time issuance flow, but the failover problem must be visible in:

- metrics;
- admin CLI/internal API;
- alerting.

If the primary node fails, access must not become `ready`.

## Consequences

- Stage 6 placement and operation state machines must distinguish primary and failover allocations.
- Alerts must cover failover degradation and primary provisioning failure separately.
- E2E tests must prove primary failure does not issue a subscription URL.
