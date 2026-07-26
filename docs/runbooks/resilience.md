# Load and Resilience Drills

This runbook covers bounded local Stage 8 load and failure injection. Never aim these scripts at production.

## Complete Drill

```powershell
make resilience-drill
```

Default budgets:

| Scenario | Required result |
|---|---|
| Public subscription negative-path load | 20 rps for 30 s, p95 <= 200 ms, unexpected-status/transport error ratio <= 0.1%; only uniform `404` is expected |
| Kafka outage and replay | Outbox returns to `published`; subscription period count does not increase |
| PostgreSQL outage | Access `/livez` stays `200`, `/readyz` becomes `503`, readiness recovers |
| Primary VPN-node loss | Real VLESS + REALITY request succeeds through the provisioned failover |
| Xray reload failure | Last-known-good is restored and healthy |
| Provisioning recovery | Reconciliation/failover tests preserve generation fencing |

`RESILIENCE_SOAK_DURATION` and `RESILIENCE_RPS` may increase the local probe within hard tool bounds. The target and every redirect must remain HTTPS with a non-empty host and without userinfo or a fragment; it remains environment-only and is never emitted. The 100,000-request cap is computed with a ceiling for fractional durations and caps actual submissions. The drill raises the local Access source/token limits above its bounded 600-request workload so rate-limited `429` responses cannot make lookup latency look artificially healthy; default Compose limits remain 120 requests per source and 30 per token per window.

## Failure Handling

- Load budget exceeded: preserve aggregate output, inspect metrics/traces without adding raw paths, and stop. Do not loosen the budget to make CI pass.
- Kafka did not recover: inspect broker health, producer retry metrics, and outbox state. Never delete inbox/outbox rows.
- PostgreSQL did not recover: inspect pool and migration health. Do not convert readiness to liveness.
- Failover traffic failed: stop new placement, inspect both node desired/actual revisions and Xray health, then use Provisioning reconciliation.
- Reload rollback failed: remove the node from placement and recover through systemd/last-known-good before accepting traffic.

The drill generates a unique Compose project for every run and derives all auxiliary container, network, and cache-volume names from it. Before fault injection it records the ordinary `vpn-service_postgres-data` volume and after cleanup proves that volume was neither removed nor replaced; an otherwise absent volume receives a temporary content sentinel as a regression test. Cleanup may remove only the generated project and its auxiliary volumes. Any cleanup or preservation failure fails the drill.

## Production Blockers

- Approved isolated failure environment and traffic target.
- Capacity model, expected workload, multi-hour soak, and data volume.
- Blast-radius and abort thresholds.
- On-call/incident coverage and provider console access.
- Regional, DNS, network-partition, disk-full, certificate-expiry, and restore-under-load exercises.
