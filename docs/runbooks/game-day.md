# Production Readiness Game Day

Status: approved only for local/disposable environments  
Production-like staging: not yet available  
Production execution: prohibited

## Safety boundary

- Use a unique Compose project and generated local credentials.
- Verify the target project, hosts, volumes, database names, and environment
  before every fault.
- Never point a drill at real providers, DNS, registries, VPS, databases,
  credentials, users, or traffic without a separate written approval.
- Do not print secrets, subscription URLs, UUIDs, provider/Telegram payloads,
  private keys, customer data, destinations, DNS history, or packet contents.
- Abort on unexpected project/volume ownership, evidence leakage, loss of the
  cleanup sentinel, inability to restore last-known-good, or effects outside the
  declared blast radius.

## Preconditions

1. Clean committed worktree and recorded commit.
2. Docker/Compose available; repository caches remain under the configured
   `VPN_PLATFORM_CACHE_ROOT`, preferably on drive D on Windows.
3. No unrelated Compose project uses the selected project name.
4. Required images are digest-pinned or built from the recorded commit.
5. Disposable credentials and databases only.
6. Incident roles and stop authority assigned.

## Local execution order

Run the individual commands from a clean commit:

```text
make compose-smoke
make stage7-smoke
make vpn-smoke
make secret-rotation-drill
make resilience-drill
make backup-cleanup-test
make backup-restore-drill
```

`make resilience-drill` owns a unique Compose namespace, proves preservation of
the ordinary local PostgreSQL volume, exercises Kafka outage/replay, PostgreSQL
stop/start readiness behavior, primary VPN-node loss with real VLESS + REALITY
traffic, Xray recovery, and observability-backend isolation.

These commands do not prove real PostgreSQL HA failover, production certificate
revocation, real alert delivery, registry rollback, DNS rollback, or full
multi-host disaster recovery.

## Scenario catalog

| ID | Scenario | Local command/evidence | Budget | Staging requirement |
|---|---|---|---|---|
| GD-01 | YooKassa timeout/ambiguous result | `make compose-smoke` | one payment effect; provider ambiguity resolved within retry window | production-equivalent YooKassa test merchant and network fault proxy |
| GD-02 | prolonged Kafka outage and outbox replay | `make resilience-drill` | no duplicate period; backlog drains after recovery | production quorum/broker loss and ACL topology |
| GD-03 | real PostgreSQL failover | unavailable locally | approved RPO/RTO; no split brain or lost committed effect | production-like primary/standby, failover manager and WAL/PITR |
| GD-04 | primary VPN node loss with real traffic | `make resilience-drill` | failover request succeeds within drill bound; no capacity corruption | approved regional VPS topology |
| GD-05 | leaked subscription token | `make secret-rotation-drill`, Access tests, observability smoke | old token uniformly rejected immediately after committed rotation | edge cache/log validation in staging |
| GD-06 | expired internal service certificate | `go test ./internal/platform/httpserver -run TestMutualTLSRejectsExpiredClientCertificate` | handshake rejected; service does not trust expired peer | issuance/expiry alert/replacement drill |
| GD-07 | expired node-agent certificate | generic TLS rejection plus node identity tests | node mutation rejected; placement removed before capacity risk | real node leaf expiry, revocation and re-enrollment |
| GD-08 | compromised admin certificate | Admin principal disable integration test | disable blocks new action immediately | CA revocation/session drain/operator replacement |
| GD-09 | invalid Xray reload | `make resilience-drill`, `make node-hardening-test` | last-known-good healthy before response budget expires | approved VPS/systemd canary |
| GD-10 | restore all eight databases | `make backup-restore-drill` | local RPO <= 24h, RTO <= 30m, exact counts/owners | off-host immutable artifacts and approved production RPO/RTO |
| GD-11 | rollback previous release candidate | no N/N-1 registry/staging candidate | canary abort and owner reconciliation within approved RTO | immutable registry digests and two-version staging |
| GD-12 | observability backend unavailable | `make resilience-drill` | business live/ready and public request behavior continue | out-of-band alert and production storage outage |

## Fault procedure

For every scenario:

1. Record preconditions and aggregate owner state.
2. Start the timer and inject exactly one fault.
3. Observe expected metrics/alerts and business behavior.
4. Abort if blast radius or integrity differs from the scenario.
5. Recover using the owner runbook, not direct data edits.
6. Reconcile durable state and verify budget.
7. Roll back the drill environment and prove cleanup.
8. Write the actual result and residual risk in
   `docs/reviews/stage9/game-day-report.md`.

## Staging acceptance

The staging topology must use production-equivalent identity, network, HA,
registry/digest, alert receiver, backup storage, and release procedures while
containing no real customer/provider data. A scenario passes only with dated,
commit/digest-bound evidence and the named owner. A simulation cannot be
relabeled as a real failover or revocation.
