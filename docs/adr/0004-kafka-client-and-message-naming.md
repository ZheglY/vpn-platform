# ADR 0004: Kafka Client, Message Naming, and Delivery Rules

Status: Accepted

Date: 2026-07-12

## Context

Kafka is the asynchronous backbone for domain facts, notification triggers, and provisioning commands. The project needs a mature Go client and a naming scheme that avoids mixing facts with commands.

## Decision

Use `franz-go` as the Kafka client.

Naming:

- Domain facts use past tense: `billing.payment.succeeded.v1`.
- Async commands use command naming: `access.provision.request.v1`, `access.revoke.request.v1`.
- Command result facts use past tense: `access.provision.succeeded.v1`, `access.provision.failed.v1`, `access.revoke.succeeded.v1`, `access.revoke.failed.v1`.
- User-delivery readiness is separate from entitlement activation and uses `access.ready.v1`.

Every message has:

- schema version;
- owner/producer;
- stable partition key;
- JSON Schema;
- AsyncAPI documentation;
- retry policy;
- DLQ policy;
- timeout and terminal failure definition where applicable.
- exact partition key rule.

Consumers are at-least-once. Exactly-once is achieved only at business-effect level through idempotency, unique constraints, inbox tables, and state machines.

## Consequences

- Stage 1 must pin an exact `franz-go` version after checking current releases.
- Contract tests must validate schemas and backward compatibility.
- DLQ requires alerts, reason, replay tooling, and runbook. It is not a passive dump.
- Kafka payloads never contain subscription tokens, VLESS UUIDs, REALITY private keys, full provider payloads, or browsing data.
- `subscription.activated.v1` means entitlement is active; it does not mean VPN access is ready.
