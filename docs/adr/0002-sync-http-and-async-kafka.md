# ADR 0002: Synchronous HTTP and Asynchronous Kafka Boundaries

Status: Accepted

Date: 2026-07-12

## Context

The system contains commands that need immediate answers and domain facts that should decouple services. Kafka must not become RPC in disguise, and HTTP must not be used to hide eventually consistent workflows that require durable retries.

## Decision

Use HTTP/JSON over standard `net/http` for commands and queries where the caller needs an immediate result.

Use Kafka for:

- domain facts that already happened;
- asynchronous commands with explicit owner, timeout, retry policy, DLQ, and terminal failure;
- event-driven notifications and reconciliation triggers.

All Kafka publishing from service state transitions uses transactional outbox. All consumers are idempotent and persist inbox/dedupe state before acknowledging messages.

## Consequences

- Payment creation returns a direct HTTP response with a confirmation URL.
- Payment fulfillment after webhook verification publishes `billing.payment.succeeded.v1`.
- Provisioning can be modeled as async commands because node application may take time and requires retries.
- There are no distributed transactions across PostgreSQL and Kafka.
