# ADR 0029: Stage 8 trace context and log pipeline

- Status: Accepted for Stage 8 implementation
- Date: 2026-07-25
- Owners: platform and all service owners
- Security impact: High
- Contract impact: W3C transport headers only; no business payload or schema change
- Extends: ADR 0013, ADR 0027, and ADR 0028

## Context

Metrics expose bounded service health but cannot connect one synchronous request to downstream HTTP calls, Kafka work, and retry handling. Existing stdout logs are intentionally support-safe, but collecting every field and message would create a second durable store for bearer URLs, payment/provider data, Telegram content, VPN credentials, or user-traffic metadata.

Transactional outbox rows already persist complete business envelopes. Adding trace fields to those envelopes would change public event contracts and retain operational identifiers in business storage. Trace continuity across a delayed outbox therefore needs a deliberate boundary.

## Decision

1. Service processes use the pinned OpenTelemetry Go SDK with W3C `traceparent` and `tracestate` propagation only. Baggage is disabled because arbitrary caller-controlled values are not an approved telemetry channel.
2. Incoming and outgoing `net/http` operations create server/client spans. Spans contain only bounded method, registered route template, and response status. They never contain URL, host, query, raw path, headers, body, peer address, user agent, error text, request ID, or business identifiers.
3. Every franz-go client injects W3C context into Kafka headers. Owner consumers extract it and create a process span containing only `messaging.system=kafka` and the fixed `publish|process` operation. Topic, key, value, partition, offset, envelope identifiers, retry data, and headers are not span attributes.
4. An outbox publisher creates a new producer span from the worker context. Trace context is not persisted in an outbox row or event payload, so a database delay or restart creates a new trace root. Durable causality remains represented by the existing secret-free envelope correlation and causation IDs, not by retained tracing headers.
5. The default sampler is parent-based with a 10 percent root ratio. Smoke tests set sampling to 100 percent. A remote sampling flag is a hint and does not authorize baggage or additional attributes.
6. Application logs continue to write the existing structured stdout stream. The OTLP branch is a separate fail-closed zap core: only an exact static message allowlist and exact static field allowlist are exported. Arbitrary message bodies and unreviewed fields are dropped before the SDK. OTel trace/span correlation is attached through an internal context field that is never serialized to stdout.
7. Exported log fields are limited to technical route, method, status, duration, bounded failure type, panic metadata, Kafka topic/partition/offset where already required for operator replay, listener address, and request ID. These fields are structured metadata, not Loki index labels. They must not contain a secret or business identifier; adding a field or message requires a privacy review and tests.
8. Each service sends OTLP/HTTP protobuf traces and logs to explicit `/v1/traces` and `/v1/logs` endpoints over TLS 1.3 mutual authentication. Every process has a dedicated client-only OTLP certificate and key, distinct from its HTTP server and service-to-service credentials, staged into that service's existing UID-owned credential volume. The Collector has a separate server-only certificate and key in its own volume.
9. The custom OpenTelemetry Collector `v0.157.0` distribution contains only the OTLP receiver, memory limiter, transform and batch processors, OTLP HTTP exporter, and health extension. Official Git tag object `ebe705f0d7c3b99129ba65e405eafbf0341a79b0` peels to release commit `4908404e59e544297b989a6961ee918b6f84b606`, which is the OCI revision. Its privacy transform repeats the resource/span/log allowlists and redacts bearer, subscription-path, and UUID-shaped content as defense in depth. Processor failure is fail-closed.
10. Collector egress is restricted to private Compose networking. Tempo `v2.10.5` is rebuilt from release commit `991ce39eb956e9ed771fcffe05eff42d33de27ba` and archive SHA-256 `d8d1c1c7949343263621fa5d6b98030486841d1fb64622bbbbcb7ac21b593540`; it stores local traces for 30 days. Loki `v3.7.2` is rebuilt from release commit `7486c4a755a089363f07403dfa1c5fe727f25f4a` and archive SHA-256 `196c5d8214f5bdc8795fedf09a60f7418f4d098cb87edb4632c613abe21ed53d`; it stores local logs for 14 days with Compactor retention enabled. Neither backend publishes a host port. Grafana is the only local query path and remains loopback-only.
11. Loki indexes only the bounded service name, namespace, and environment resource attributes. Trace/span IDs and reviewed log fields remain structured metadata. No user, payment, subscription, credential, event, request, correlation, node, URL, or traffic value may become an index label.
12. The Collector, Tempo, and Loki builds pin source integrity, Go 1.26.5 and a digest-pinned distroless runtime, include verified upstream license material, run without capabilities with read-only root filesystems, and persist only their declared data directories. Tempo and Loki receive explicit reviewed security dependency updates plus narrow compatibility patches; module verification, native config validation, smoke tests, and a zero unsuppressed HIGH/CRITICAL image gate are mandatory. Production storage, authentication, tenancy, backups, and retention require separate approved deployment configuration.
13. End-to-end smoke injects a known W3C parent into an mTLS HTTP request, proves the span is queryable through Grafana/Tempo, proves a trace-correlated structured log is queryable through Grafana/Loki, and proves a query-string sentinel is absent from both results.
14. Tempo's build uses the compatible Prometheus `v0.305.5` module from the 3.5 LTS line. Earlier `v0.305.2` leaves the Azure AD remote-write OAuth client secret as a plain string, and Tempo accepts that configuration and exposes its effective configuration through `/status/config`; treating `CVE-2026-42151` as unreachable is therefore forbidden. The official advisory identifies Prometheus 3.5.3 LTS as patched, and 3.5.5 contains the redacting `prometheus/common/config.Secret` field, which the build asserts before compilation. Trivy's linear module range omits the patched LTS branch, so one exact-product OpenVEX statement uses `vulnerable_code_not_present`; the scan displays it as suppressed and fails on every other HIGH/CRITICAL result.

## Consequences

- Operators can follow sampled synchronous and Kafka execution without recording business payloads or user traffic.
- Delayed outbox publication is intentionally a trace boundary. Existing durable correlation IDs remain available in owner-controlled records and support APIs, but are not indexed or attached to telemetry.
- OTLP log collection is narrower than stdout. A useful stdout message does not automatically become a retained Loki message.
- Trace and log loss does not block business processing; bounded queues and export deadlines prevent an unavailable Collector from creating an unbounded service dependency.
- Security rebuilding Tempo and Loki is slower than consuming upstream images. Pinned source archives and BuildKit module caches keep the build reproducible, while the strict scan and visible, field-asserted LTS VEX correction prevent convenience from weakening the release gate.
- Local retention is executable, but Stage 8 is not complete until production retention/legal-hold behavior, backup/restore, hardening, rotation, failure drills, and release controls are implemented.

## Rejected alternatives

- Propagate W3C baggage: rejected because arbitrary caller-controlled content could become durable telemetry.
- Add trace IDs to business event schemas or outbox rows: rejected because it changes durable contracts and increases identifier retention.
- Export raw zap messages and redact known secrets later: rejected because new sensitive fields or wording would fail open.
- Record HTTP URLs, Kafka topics/keys, SQL, peer addresses, or error text: rejected for disclosure and cardinality risk.
- Expose Tempo or Loki directly on host ports: rejected because Grafana is the single local query boundary.
- Reuse the Prometheus client certificate or a service server key for Collector ingress: rejected because credentials have distinct purposes and least-privilege owners.
