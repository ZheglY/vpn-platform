# ADR 0023: Stage 6 provisioning placement and node convergence

- Status: Accepted
- Date: 2026-07-19
- Deciders: Product owner and engineering
- Supersedes: ADR 0008 only where it left the concrete desired-state API, Xray version, and process reload mechanism open

## Context

Access commands intentionally contain only credential, operation, and revision identifiers. Provisioning still needs the encrypted-at-rest VLESS UUID, the paid period's immutable region policy, two distinct healthy nodes, and a safe way to converge Xray without exposing a node management API publicly. Commands span provision and revoke topics, so per-topic ordering alone cannot prevent a delayed provision from overriding a later revoke.

## Decision

1. `provisioning-service` owns a separate PostgreSQL database containing nodes, command cursors, inbox records, operations, allocations, health snapshots, sanitized dead-letter coordinates, and an outbox. It never reads another service database.
2. Access adds `subscription_id` to its audited provisioning-material response. Provisioning then calls the Subscription mTLS endpoint `GET /internal/v1/subscriptions/{subscription_id}/placement`. Subscription returns the current paid period's immutable region and exactly one primary plus one failover requirement. This keeps placement policy with the entitlement snapshot without adding secret or placement data to Kafka.
3. Provision and revoke commands share one credential-owned sequence cursor across both Kafka topics. Only the next sequence is committed, and an operation cannot be claimed while an earlier operation for the credential is non-terminal. Gaps pause that source partition and remain retryable; exact event replay is a no-op; reused event or operation IDs, stale sequences, and stale desired revisions are durable conflicts.
4. Placement locks eligible node rows and selects two distinct nodes in one transaction. Both must be healthy in the selected region. New allocation stops at 80% of configured capacity by reserving at least 20%. Primary must succeed before access is ready; exhausted failover retries produce an explicit `degraded` result.
5. Node management uses TLS 1.3 mutual authentication on a private management network. Node-agent accepts only `provisioning-service`. Provisioning verifies normal server PKI validation and the exact registered node SPIFFE URI. Local container health uses a separate per-node client identity that cannot call management routes.
6. Node-agent accepts allowlisted desired fields only at `PUT /internal/v1/credentials/{credential_id}`. `(operation_id, request SHA-256)` is journaled for bounded replay; the journal never contains a VLESS UUID or request body. A repeated operation with another payload conflicts. Lower revisions are rejected. Revoke persists an `absent` tombstone so delayed provision cannot restore access.
7. The node-local desired snapshot is mode `0600`. REALITY private keys remain separate node-local secret files. Candidate Xray JSON is mode `0600`, validated with `xray run -test -config <candidate>`, atomically installed, and started through a fixed argument vector without a shell. A failed validation or process start restores the last-known-good config.
8. Stage 6 pins official Xray-core `26.3.27` source at commit `d2758a023cd7f4174a5a5fa4ff66e487d4342ba0`. The build downloads that commit archive, verifies SHA-256 `14fa566ee0a801d3d51144c67018b449f5dcf462ddfacadd032da069787e61f9`, and compiles it with the pinned Go `1.26.5` builder. The official release binary is not shipped because its Go `1.26.1`, `golang.org/x/crypto v0.49.0`, and `golang.org/x/net v0.52.0` components contain HIGH vulnerabilities with vendor fixes. The rebuild raises only those security dependencies to `x/crypto v0.52.0` and `x/net v0.55.0`; `go mod tidy`, `go mod verify`, the image vulnerability scan, and the real data-plane smoke are required gates. The decision was checked against the [official release list](https://github.com/XTLS/Xray-core/releases), [official build instructions](https://github.com/XTLS/Xray-core), [official command documentation](https://xtls.github.io/en/document/command.html), [official REALITY transport documentation](https://xtls.github.io/en/config/transports/reality.html), and the [pinned VLESS config source](https://github.com/XTLS/Xray-core/blob/v26.3.27/infra/conf/vless.go) on 2026-07-19. The renderer deliberately emits inbound `clients`, which is the field accepted by this pin; a future Xray upgrade must re-check schema compatibility instead of following rolling website examples blindly.
9. Reconciliation compares allocation desired state with node actual state. It may restore a succeeded active allocation or enforce any revoke tombstone, then records the converged allocation state and capacity effect. It never overwrites a newer node revision and never converts an already published terminal failure into success. Stage 6 has no terminal-operation reset: recovery requires a fresh higher-revision Access command through an approved future admin flow.
10. Poison records produce a versioned DLQ notice containing only source topic, partition, offset, SHA-256, and a bounded reason code. Replay is allowlisted to the two command topics and re-reads the original Kafka coordinate, verifies the stored hash, and republishes the original record without logging or persisting it.

## Consequences

- Node assignment and capacity are deterministic, transactional, and independent of Access availability after allocation.
- Provisioning temporarily sees the VLESS UUID in memory, but Kafka, PostgreSQL, logs, metrics, traces, and DLQ notices do not.
- The node image is reproducible from an integrity-checked upstream archive and explicit dependency versions. It is a security rebuild of the official stable source, not the upstream prebuilt artifact; each dependency or Xray change requires a fresh ADR review, vulnerability scan, and protocol smoke.
- Local Compose co-locates node-agent and its managed Xray process in one non-root container to make reload and rollback testable. Production keeps separate non-root OS identities and systemd units; that deployment work remains Stage 8.
- Nodes marked draining remain reconcilable but receive no new allocations. Heartbeat loss makes a node ineligible and operator-visible without deleting allocation history.
- Production VPS enrollment, WireGuard setup, certificate rotation, firewall policy, and real traffic are still blocked on provider/legal review and Stage 8 hardening.

## Rejected Alternatives

- Put region or VLESS UUID in Kafka: rejected because the region already belongs to an immutable entitlement snapshot and the UUID is secret material.
- Let Provisioning read Subscription or Access tables: rejected because it violates database ownership.
- Use Kafka for node apply and immediate status: rejected because apply needs an immediate authenticated result and nodes must not expose Kafka credentials.
- Mutate Xray through arbitrary shell commands: rejected because command injection and privilege escalation would become part of the API surface.
- Mark terminal failure recovered automatically: rejected because consumers may already have acted on the failed fact; recovery requires an explicit operator workflow.
