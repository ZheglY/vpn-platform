# Stage 9 Go/No-Go Checklist

Review date: 2026-08-02
Valid until: 2026-08-09
Decision owner: Product owner
Decision: **NO-GO**

The machine-readable source is `go-no-go.json`. Expiring evidence is valid
through its UTC `valid_until` date; immutable evidence has no synthetic expiry.
A blocking gate passes only with current evidence. `pending` is blocking.
Local evidence is not promoted to production or staging evidence.

| Gate ID | Requirement | Owner | Evidence | Status | Severity | Evidence type | Review date | Valid until | Blocking |
|---|---|---|---|---|---|---|---|---|---|
| BASE-01 | Stage 8 accepted | Product owner | PLANS.md and commit 76f5624e664c556c3ec598055524319eab0af1f3 | passed | P1 | immutable | 2026-08-02 | - | yes |
| ARCH-01 | No open P0 or P1 architecture/code findings | Architecture | docs/reviews/stage9/architecture-code-review.md | passed | P1 | expiring | 2026-08-02 | 2026-08-09 | yes |
| ARCH-02 | Architecture/code review complete | Architecture | docs/reviews/stage9/architecture-code-review.md | passed | P1 | expiring | 2026-08-02 | 2026-08-09 | yes |
| ARCH-03 | Deferred P2 findings accepted or closed | Product owner / Platform | ACR-001 and ACR-002 have no accepted production deferral | blocked | P2 | expiring | 2026-08-02 | 2026-08-30 | yes |
| SEC-01 | Security/privacy review complete and threat model current | Security / Privacy | docs/reviews/stage9/security-privacy-review.md and docs/threat-model.md | passed | P1 | expiring | 2026-08-02 | 2026-08-09 | yes |
| LIC-01 | License inventory complete | Platform / Security | docs/reviews/stage9/dependency-license-review.md and deploy/release/license-policy.json | passed | P1 | expiring | 2026-08-02 | 2026-08-09 | yes |
| LIC-02 | No unknown/prohibited license or unmet distribution obligation | Legal / Release approver | LIC-01 through LIC-06 remain unresolved; publication policy is NO-GO | blocked | P1 | expiring | 2026-08-02 | 2026-08-30 | yes |
| LEGAL-01 | Professional legal review complete | Product owner / Legal | docs/reviews/stage9/legal-payment-provider-checklist.md | blocked | P1 | expiring | 2026-08-02 | 2026-08-30 | yes |
| PAY-01 | YooKassa production agreement and product requirements resolved | Finance / Legal | PAY-01 and PAY-02 are unresolved; sandbox only | blocked | P1 | expiring | 2026-08-02 | 2026-08-30 | yes |
| PAY-02 | Refund, cancellation, receipt, tax and buyer-field policy resolved | Finance / Tax counsel / Product owner | PAY-03 through PAY-09 are unresolved | blocked | P1 | expiring | 2026-08-02 | 2026-08-30 | yes |
| VPS-01 | VPS provider and VPN/proxy/exit-traffic AUP approved | Operations / Legal | VPS-01 and VPS-02 are unresolved; no provider selected | blocked | P1 | expiring | 2026-08-02 | 2026-08-30 | yes |
| PKI-01 | Production PKI approved and revocation tested | Security / Operations | SPR-001, SPR-007 and GD-07/GD-08 | blocked | P1 | expiring | 2026-08-02 | 2026-08-30 | yes |
| SECRET-01 | Production secret custody and rotation approved | Security / Operations | SPR-004 and INF-SECRET-01 | blocked | P1 | expiring | 2026-08-02 | 2026-08-30 | yes |
| DB-01 | PostgreSQL HA/PITR and least-privilege roles verified | DBA / Platform | ACR-001, INF-DB-01 and GD-03 | blocked | P1 | expiring | 2026-08-02 | 2026-08-30 | yes |
| KAFKA-01 | Production Kafka quorum, ACL and recovery verified | Platform | Local replay passes but INF-KAFKA-01 production topology is unresolved | blocked | P1 | expiring | 2026-08-02 | 2026-08-30 | yes |
| BACKUP-01 | Encrypted off-host immutable backup configured | DBA / Security | INF-BACKUP-01 is unresolved | blocked | P1 | expiring | 2026-08-02 | 2026-08-30 | yes |
| RESTORE-01 | All eight owner databases restored with integrity and ownership checks | DBA / Platform | EV-07: eight encrypted databases restored at f4c7d1eb500944b80d6b8749e69a8427468dbd2d; checksum, row-count and owner checks passed | passed | P1 | expiring | 2026-08-02 | 2026-08-09 | yes |
| GAME-01 | Incident game day passed | Operations / Security | GD-03, GD-07, GD-08 and GD-11 remain staging blockers | blocked | P1 | expiring | 2026-08-02 | 2026-08-30 | yes |
| ALERT-01 | Real alert receiver and escalation tested | SRE | SPR-006 and OPS-ALERT-01; local receiver is inert | blocked | P1 | expiring | 2026-08-02 | 2026-08-30 | yes |
| EDGE-01 | Domains, DNS, TLS, edge and bearer-path redaction approved | Platform / Security | SPR-002 and INF-DNS-01 through INF-EDGE-01 | blocked | P1 | expiring | 2026-08-02 | 2026-08-30 | yes |
| REGISTRY-01 | Registry publication, immutable digest and verification approved | Platform / Security | INF-REG-01; no registry selected | blocked | P1 | expiring | 2026-08-02 | 2026-08-30 | yes |
| PROVENANCE-01 | Final release provenance verified for publishable image digests | Release approver / Security | Local release metadata exists; publication/attestation and license approval are blocked | blocked | P1 | expiring | 2026-08-02 | 2026-08-30 | yes |
| CAPACITY-01 | Control-plane and per-region VPN reserve approved | Platform / Operations / Finance | INF-CAP-01; no production load/region/provider selected | blocked | P1 | expiring | 2026-08-02 | 2026-08-30 | yes |
| OPERATIONS-01 | Abuse, support, breach and lawful-request processes assigned | Product owner / Legal / Support | OPS-LEGAL-01 through OPS-LEGAL-06 are unresolved | blocked | P1 | expiring | 2026-08-02 | 2026-08-30 | yes |
| ROLLBACK-01 | Critical N/N-1 rollback and disaster recovery drill passed | Platform / Operations | ACR-002, GD-11 and docs/runbooks/production-rollback.md | blocked | P1 | expiring | 2026-08-02 | 2026-08-30 | yes |
| CI-01 | All required local CI, security, smoke, restore, node, rotation, resilience and release checks green | Platform / Security | EV-02 through EV-11 passed at f4c7d1eb500944b80d6b8749e69a8427468dbd2d; EV-01 and EV-12 passed at 12fef12063b657639fd4d4ef377bba29c4b1633e; production-only gates remain separately blocked | passed | P1 | expiring | 2026-08-02 | 2026-08-09 | yes |

## Decision rationale

The technical repository review can finish successfully while launch remains
NO-GO. Current hard blockers include legal/payment/privacy decisions, license
obligations, production PKI and revocation, secret custody, DB/Kafka production
topology, off-host backup, real alerts/on-call, domains/edge, registry,
provenance, capacity, provider/AUP, operational processes, and critical staging
game-day/rollback evidence.

Changing this decision to `GO` requires:

1. every blocking row in both this document and `go-no-go.json` to be `passed`;
2. no stale evidence;
3. `make license-publication-gate` against the exact release bundle;
4. `make production-readiness` and all required release/staging gates green;
5. a dated product-owner launch approval.
