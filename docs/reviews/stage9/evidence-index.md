# Stage 9 Evidence Index

Review owner: Platform / Security
Evidence date: 2026-08-02
Behavioral/drill source commit:
`dfee5bc44cadc8138e6ebe115cb7bf1ddab61b2a`
Final review-control verification commit:
`dfee5bc44cadc8138e6ebe115cb7bf1ddab61b2a`
Heavy artifact policy: generated reports remain under ignored `tmp/`; the
release manifest/checksums bind the aggregate outcomes recorded here

No entry may contain a real subscription URL, VLESS UUID, private key, provider
or Telegram payload, customer identifier, destination, DNS history, or packet
content.

| Evidence ID | Control | Command | Safe aggregate result | Owner | Fresh until | Status |
|---|---|---|---|---|---|---|
| EV-01 | source/unit/race/lint/vulnerability/contracts/images | `make verify` | passed at `dfee5bc...`: unit/PostgreSQL/race/vet/lint/contracts, `govulncheck`, Gitleaks/filesystem secrets, npm audit, preflight/readiness, 19 builds and the zero HIGH/CRITICAL image gate | Platform / Security | 2026-08-09 | passed |
| EV-02 | payment ambiguity, migrations, service E2E | `make compose-smoke` | passed at `dfee5bc...` from a missing disposable `tmp/` directory: owner migrations, payment ambiguity recovery, idempotency and end-to-end business flow | Service owners | 2026-08-09 | passed |
| EV-03 | full Access/Provisioning/Xray flow | `make vpn-smoke` | passed at `dfee5bc...`: real local pinned VLESS + REALITY traffic, failover, revoke, Happ rendering and fail-closed access behavior | Access / Provisioning / Node | 2026-08-09 | passed |
| EV-04 | Notification/Admin ordering/recovery | `make stage7-smoke` | passed at `dfee5bc...`: causal delivery suppression, owner replay recovery, RBAC/audit and 11-target profile | Notification / Admin | 2026-08-09 | passed |
| EV-05 | metrics/traces/logs privacy and target inventory | `make observability-smoke` | passed at `dfee5bc...`: 8/11 target profiles, absence/down alerts, trace/log correlation and sentinel redaction | Platform / Security | 2026-08-09 | passed |
| EV-06 | forced backup failure cleanup | `make backup-cleanup-test` | passed at `dfee5bc...`: forced post-key failure removed the age identity and drill directory | Platform / DBA | 2026-08-09 | passed |
| EV-07 | encrypted restore of eight databases | `make backup-restore-drill` | passed at `dfee5bc...`: 8 encrypted artifacts; checksum/count/owner match; final repeated RPO 9.037 s and RTO 36.911 s | Platform / DBA | 2026-08-09 | passed |
| EV-08 | Debian host hardening/systemd/firewall | `make node-hardening-test` | passed at `dfee5bc...`: identities, real service-user reads, modes, syntax, firewall preservation, hardened units and fail-closed first boot in disposable Debian | Node / Operations | 2026-08-09 | passed |
| EV-09 | mTLS/provider/Access/REALITY rotation | `make secret-rotation-drill` | passed at `dfee5bc...`: overlap/cutover/retirement/rollback and distinct REALITY generations | Security / secret owners | 2026-08-09 | passed |
| EV-10 | load, Kafka/DB/node/observability faults | `make resilience-drill` | passed at `dfee5bc...`: bounded load, replay without duplicate period, DB readiness recovery, real VPN failover/LKG and telemetry-backend isolation; ordinary local volumes preserved | Platform / Service owners | 2026-08-09 | passed |
| EV-11 | 19 image SBOM/scan/checksum manifest | `make release-bundle` | passed at `dfee5bc...`: 19 commit-addressed images, 19 SPDX files, 19 Trivy reports and 39 checksummed artifacts; manifest SHA-256 `2a3c0ec91fa80810eca1886db48457a33e12eda22f77fa726800bb2c8b30f4c5` | Platform / Security | final candidate only | passed |
| EV-12 | review/license/go-no-go consistency | `make production-readiness` | passed at `dfee5bc...` as a validator: policy remains publication NO-GO with 18 image blockers and all 19 open hard gates reported | Architecture / Security | 2026-08-09 | passed |
| EV-13 | expired client certificate rejection | `go test -mod=readonly ./internal/platform/httpserver -run TestMutualTLSRejectsExpiredClientCertificate -count=1` | passed at `dfee5bc...`: valid TLS 1.3 control succeeds; expired client request is rejected | Security | 2026-08-09 | passed |
| EV-14 | bounded Access keyrings/loadprobe remediation | `go test -mod=readonly ./services/access/internal/credential ./tools/loadprobe` | passed at `dfee5bc...`: bounded keyrings and HTTPS/TLS 1.3/redirect/sample-limit admission suites | Security / Platform | 2026-08-09 | passed |

## Reproduction

Use the exact source commit, pinned tool/image digests, a clean worktree, and the
commands above. Configure caches through `VPN_PLATFORM_CACHE_ROOT`; do not copy
generated reports into Git. Environment-specific evidence that contains
confidential host/provider/legal information belongs in an approved restricted
evidence store and is referenced only by opaque case ID.

## Unavailable evidence

- real PostgreSQL HA failover and PITR;
- production PKI revocation and compromised certificate containment;
- real Alertmanager receiver/escalation;
- production domains/TLS/edge redaction;
- registry publication, per-image signature and production attestation;
- N/N-1 canary rollback and full disaster recovery;
- YooKassa production/receipt flow and real provider/VPS controls.

These are hard blockers, not skipped-success entries.
