# Production Security And Operations Contracts

These contracts are provider-neutral acceptance requirements. They do not claim
that a provider resource or staging environment exists.

## Secret Manager

- Separate staging/production namespaces and service identities; default deny.
- Workloads receive only exact secret versions needed by their component.
- Prefer short-lived workload identity and file projection; never command-line
  secrets or persisted shell history. Files are owner-only `0400`/`0440`.
- Version, creator, approver, consumer, issue/activate/retire/destroy times and
  break-glass use are auditable. Dual control applies to root/recovery material.
- Rotation supports bounded overlap and verified rollback; compromise skips
  overlap and disables the old identity immediately.
- Backup/escrow and recovery are encrypted, separately authorized, and drilled.

## PKI

- Environment-specific offline/root and issuing hierarchy; issuing keys are not
  present on application hosts. TLS 1.3 is the minimum.
- One exact SPIFFE URI per leaf, correct EKU, short lifetime, automated renewal,
  inventory, expiry alert, revocation status distribution, and emergency deny.
- Service, admin, observability, health, Kafka, deployment, and node identities
  are separate. Node identity includes immutable enrolled node ID.
- Rotation trusts old/new roots only for a bounded measured window. Revocation
  and stolen-key game days must prove denial before production GO.

## Backup And PITR

- PostgreSQL HA/fencing and WAL archive achieve owner-approved RPO/RTO. Backups
  are encrypted before leaving the DB boundary, checksum inventoried, immutable,
  off-host/off-account where practical, and monitored for freshness.
- Backup and restore roles are separate from runtime/migrator. Restore occurs
  only in an isolated exact-empty target with production egress disabled.
- Quarterly restore and annual full-DR minimums verify all eight owners, object
  ownership, business invariants, Kafka reconciliation, Redis empty recovery,
  credentials, node enrollment, privacy, and alert delivery.

## Observability And Alerts

- Metrics/traces/logs use authenticated private ingress, encryption at rest,
  tenant/RBAC separation, bounded retention, access review, capacity alerting,
  and repository privacy allowlists.
- A real receiver contract names primary/secondary contacts, severity routing,
  acknowledgement SLO, escalation timer, maintenance behavior, and out-of-band
  fallback. An end-to-end staging alert must be acknowledged by a human.
- Observability outage cannot block business processing and must itself reach an
  independent receiver. No raw URL, payload, identifier, credential, destination
  IP, DNS history, or packet data is exported.

## Capacity And Node Enrollment

- Measure edge, service, PostgreSQL, Kafka, Redis, telemetry, and VPN throughput;
  retain regional headroom and provider lead-time forecasts.
- At least two independent healthy VPN nodes per offered region and enough spare
  capacity to lose one primary without exceeding the 80% allocation ceiling.
- Enrollment records provider/account/region, immutable node ID, image digest,
  host baseline, WireGuard peer, SPIFFE identity, Xray generation, capacity,
  attestation evidence, owner, and lifecycle state.
- New nodes are quarantined until baseline, mTLS, firewall, Xray validation,
  telemetry, canary traffic and revoke pass. Drain precedes wipe/decommission;
  all peers/certificates/keys are revoked and inventory closes.

## Public Edge And `/s/{token}`

- Public, subscription, and private admin hostnames have separate routing and
  log policies. Admin ingress is never public.
- For `/s/{token}`, access/error/WAF/APM/CDN logs store only the route template
  `/s/{token}`. Query, original URI, referrer, redirect location, request/trace
  capture, cache key, and analytics dimensions containing the token are disabled.
- The edge preserves uniform no-store `404`, never redirects the route, strips
  untrusted identity headers, normalizes once, bounds body/header/concurrency,
  and rate-limits without exposing the bearer path.
- Staging injects a synthetic token sentinel and searches every edge,
  observability, support, and backup surface before production approval.
