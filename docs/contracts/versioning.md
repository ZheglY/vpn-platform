# Contract Versioning Rules

Stage 0 creates contract skeletons only. Endpoints, events, and async commands become implementation commitments only when their milestone is approved and tests are added.

## HTTP

- HTTP contracts use OpenAPI 3.1.
- Public and internal APIs must be documented before implementation.
- Every operation must define security explicitly or inherit a deliberate global security requirement.
- Internal service APIs use mTLS service identity and per-endpoint allowlists.
- Admin APIs use admin mTLS identity plus RBAC and audit.
- Telegram webhook uses `X-Telegram-Bot-Api-Secret-Token`.
- YooKassa webhook is public ingress and must be verified through provider API before fulfillment.
- Public operations such as published plans must use `security: []` explicitly.
- Public paths must not expose secrets except the intentional bearer token path `GET /s/{token}`.
- Logs, traces, metrics, and error reporting must use route templates and redacted attributes.
- Error responses use a standard safe envelope:
  - `code`
  - `message`
  - `request_id`
  - optional `fields`
- Public errors must not contain stack traces, SQL details, provider details, secrets, raw payloads, or PII.
- Breaking changes require an ADR and either a new path/version or an explicit migration plan.
- OpenAPI lint and breaking-change checks are required in CI once contracts are implemented.

## Idempotency-Key

Payment/order/token issue/token rotation commands and other externally retried commands use `Idempotency-Key`.

Rules:

- key has bounded length and accepted character set;
- key is scoped to subject, operation, and request hash;
- token issue/rotation keys are scoped to authenticated service subject and subscription;
- repeated same key and same payload returns the original result;
- same key and different payload returns conflict;
- TTL is documented per operation;
- idempotency storage is durable when it protects money or access effects.

## Kafka

Kafka contracts use AsyncAPI plus JSON Schema.

Message categories:

- Facts: past tense, for example `billing.payment.succeeded.v1`.
- Commands: imperative command naming, for example `access.provision.request.v1`.
- Command results: past tense, for example `access.provision.succeeded.v1`, `access.revoke.failed.v1`.
- User VPN readiness: `access.ready.v1`, separate from subscription entitlement events.

Every Kafka message has:

- common envelope;
- `schema_version`;
- stable `event_id`;
- `producer`;
- `correlation_id`;
- `causation_id` when applicable;
- `aggregate_type`;
- `aggregate_id`;
- stable `partition_key`;
- data schema;
- owner;
- retry policy;
- timeout policy for async commands;
- DLQ policy and replay rules.
- exact partition key rule.

Compatibility:

- Adding optional fields is backward-compatible.
- Removing fields, changing field type, changing field meaning, or changing delivery semantics requires a new schema version.
- Secrets, subscription tokens, VLESS UUIDs, REALITY private keys, full provider payloads, and browsing data are forbidden in Kafka messages.
- Consumers are at-least-once and must be idempotent.
- Credential operation commands/results use `credential:{credential_id}` as partition key.
- Billing, subscription, and user-delivery readiness events use `user:{user_id}` as partition key.
- `subscription.activated.v1` means entitlement activation only; user-facing VPN readiness uses `access.ready.v1`.

## Schema Files

- HTTP: `contracts/http/openapi.yaml`
- Async messaging: `contracts/events/asyncapi.yaml`
- Common Kafka envelope schema: `contracts/events/schemas/envelope.schema.json`
