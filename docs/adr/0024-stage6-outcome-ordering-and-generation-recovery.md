# ADR 0024: Stage 6 outcome ordering and generation recovery

- Status: Accepted
- Date: 2026-07-19
- Owners: access-service, provisioning-service, node-agent
- Security impact: High
- Contract impact: all four provisioning outcome envelopes and provision success data
- Amends: ADR 0022 and ADR 0023

## Context

Stage 6 initially treated the endpoints returned by a successful provision operation as the complete node assignment. That is false for a degraded allocation: the primary endpoint is usable, while the failed failover node remains assigned and must still be removed before revoke can succeed. The Access revoke proof therefore could not validate a complete degraded assignment.

Provisioning also kept an allocation's original operation identity after a higher desired revision. A revoked or failed allocation was returned unchanged, apply acknowledgements were fenced by the original operation, and capacity could not be reserved exactly once for a new generation. ADR 0023 prohibited automatic recovery after terminal failure, but Stage 6 acceptance requires an explicit higher-revision Access command to recover without changing credential identity.

Provision and revoke outcomes use four Kafka topics. Kafka cannot order records across those topics, and the Stage 6 producer did not assign an aggregate sequence. A later revoke could consequently be applied before an earlier provision outcome. Reconciliation repeatedly selected the oldest fixed batch without a durable claim, allowing starvation and duplicate work across replicas. Finally, cancellation of the inbound HTTP context during Xray stop could terminate the data plane before last-known-good recovery.

These are pre-production correctness and safety corrections. No released consumer depends on the earlier Stage 6 draft contracts.

## Decision

1. `access.provision.succeeded.v1` carries two separate views: required `assigned_node_ids` proves the complete immutable allocation for `allocation_revision`, while `endpoints` contains only currently usable profile endpoints. Access stores assignment nodes independently from endpoint snapshots. Revoke success must exactly prove removal of the stored assignment, including a failed failover; an empty assignment remains valid only for a zero-allocation revoke.
2. Provisioning owns one positive, monotonically increasing `aggregate_sequence` per credential across `access.provision.succeeded.v1`, `access.provision.failed.v1`, `access.revoke.succeeded.v1`, and `access.revoke.failed.v1`. Terminal operation completion increments the cursor and writes the outcome plus sequence to the outbox in the same PostgreSQL transaction. The outbox publisher cannot claim sequence `N` while a lower sequence for the credential is unpublished.
3. Access owns one provisioning-outcome cursor per credential across all four topics. Sequence `N` applies only after `N-1`. An exact event replay is a no-op, a gap is deferred without committing or dead-lettering it, and reuse of an event ID or aggregate sequence with different coordinates or payload is a durable conflict. Outcome envelopes with a missing or non-positive sequence are invalid.
4. A higher-revision Access command creates a new allocation generation. In one transaction Provisioning rejects stale revisions, treats the same operation and revision as replay, rebinds every retained allocation to the new desired operation and revision, and resets its convergence state. Capacity remains reserved for active or pending allocations and is reserved exactly once when a revoked allocation becomes present again. Node acknowledgements and reconciliation writes are fenced by the current desired operation, desired revision, desired state, and allocation revision so a stale worker cannot mutate the new generation.
5. A fresh higher-revision command may recover a terminal provision failure or reactivate a credential while an older revoke is pending. The older worker becomes stale and may not publish a terminal outcome after the generation transition. This supersedes ADR 0023's Stage 6 prohibition on terminal-operation recovery; recovery is allowed only through the normal ordered Access command contract, never by resetting a terminal operation in place.
6. Reconciliation uses a durable due time and lease claim. A bounded `FOR UPDATE SKIP LOCKED` claim advances scheduling metadata before returning work, so multiple replicas do not receive the same live lease and allocations beyond one batch become eligible. Success and failure both release or reschedule the claim; one failing allocation cannot block later rows.
7. Xray validation remains cancelable by the request context. After a validated candidate enters the stop/install/start transition, node-agent uses an independent bounded consistency context. `Apply` does not return until either the candidate is healthy or the previous configuration has been restored and is healthy. Request cancellation cannot leave the managed Xray process stopped.
8. `make vpn-smoke` is a full Compose acceptance path. It must traverse Access command outbox, Kafka, Provisioning, authenticated Access material and Subscription placement reads, both node agents, real Xray, Provisioning outcome outbox, Kafka, Access state and profile rendering, and real client traffic. Revoke must remove traffic. Direct node-agent mutation remains a lower-level test and cannot satisfy this gate.
9. Access command envelope sequencing is the credential's `desired_revision`, shared only by provision and revoke commands. Access keeps its separate outbox delivery sequence for the publication barrier across all Access-produced events. Readiness events therefore cannot create an invisible command-sequence gap at Provisioning.

## Consequences

- Access and Provisioning each require a new forward migration. Existing endpoint rows are backfilled into assignment snapshots; existing Provisioning outcome rows receive deterministic sequences before constraints become required.
- The four outcome v1 schemas gain a required positive `aggregate_sequence`; provision success also gains required unique `assigned_node_ids`. AsyncAPI examples and contract tests change atomically.
- Operators can diagnose deferred outcome gaps and reconciliation leases from durable state without exposing credential material.
- A canceled node-agent request may take the configured consistency bound to return because data-plane restoration has priority over request lifetime.
- Higher-revision recovery preserves allocation and operation history while making the current generation explicit and fenced.
- Command payload backfill resets provision and revoke envelope sequences to their desired revisions without changing Access outbox delivery order.

## Rejected alternatives

- Infer assignment from usable endpoints: rejected because degraded provisioning intentionally omits an unusable failover endpoint.
- Merge four outcomes into one new Kafka topic: rejected because it would replace approved public topics; a producer-owned aggregate sequence provides the required cross-topic order with a smaller pre-production correction.
- Use timestamps for outcome order: rejected because transaction clocks and Kafka delivery across topics do not establish causality.
- Delete and recreate allocations on every revision: rejected because it loses audit history and makes exact capacity accounting harder.
- Use an in-memory reconciliation cursor: rejected because it is neither crash-safe nor safe across replicas.
- Let HTTP cancellation abort Xray rollback: rejected because request latency cannot take precedence over data-plane availability.
