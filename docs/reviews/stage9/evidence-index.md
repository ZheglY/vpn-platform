# Stage 9 Evidence Index

Review owner: Platform / Security
Evidence date: 2026-07-26
Behavioral/drill source commit:
`f4c7d1eb500944b80d6b8749e69a8427468dbd2d`
Final review-control verification commit:
`12fef12063b657639fd4d4ef377bba29c4b1633e`
Heavy artifact policy: generated reports remain under ignored `tmp/`; the
release manifest/checksums bind the aggregate outcomes recorded here

No entry may contain a real subscription URL, VLESS UUID, private key, provider
or Telegram payload, customer identifier, destination, DNS history, or packet
content.

| Evidence ID | Control | Command | Safe aggregate result | Owner | Fresh until | Status |
|---|---|---|---|---|---|---|
| EV-01 | source/unit/race/lint/vulnerability/contracts/images | `make verify` | passed at `12fef12...`: unit/PostgreSQL/race/vet/lint/contracts/secrets/npm and 19 image zero HIGH/CRITICAL gates; direct `govulncheck` timed out, then the same run fetched a temporary official DB from `https://vuln.go.dev` on 2026-07-26 and found zero called vulnerabilities | Platform / Security | 2026-08-02 | passed |
| EV-02 | payment ambiguity, migrations, service E2E | `make compose-smoke` | passed: owner migrations, payment ambiguity recovery, idempotency and end-to-end business flow | Service owners | 2026-08-02 | passed |
| EV-03 | full Access/Provisioning/Xray flow | `make vpn-smoke` | passed: real local pinned VLESS + REALITY path and fail-closed access behavior | Access / Provisioning / Node | 2026-08-02 | passed |
| EV-04 | Notification/Admin ordering/recovery | `make stage7-smoke` | passed: causal delivery suppression, owner replay recovery and 11-target profile | Notification / Admin | 2026-08-02 | passed |
| EV-05 | metrics/traces/logs privacy and target inventory | `make observability-smoke` | passed: 8/11 target profiles, absence/down alerts, trace/log correlation and sentinel redaction | Platform / Security | 2026-08-02 | passed |
| EV-06 | forced backup failure cleanup | `make backup-cleanup-test` | passed: forced post-key failure removed the age identity and drill directory | Platform / DBA | 2026-08-02 | passed |
| EV-07 | encrypted restore of eight databases | `make backup-restore-drill` | passed: 8 encrypted artifacts; checksum/count/owner match; RPO 13.843 s, RTO 52.057 s | Platform / DBA | 2026-08-02 | passed |
| EV-08 | Debian host hardening/systemd/firewall | `make node-hardening-test` | passed: identities, modes, syntax, firewall preservation and hardened units verified in disposable Debian | Node / Operations | 2026-08-02 | passed |
| EV-09 | mTLS/provider/Access/REALITY rotation | `make secret-rotation-drill` | passed: overlap/cutover/retirement/rollback and distinct REALITY generations | Security / secret owners | 2026-08-02 | passed |
| EV-10 | load, Kafka/DB/node/observability faults | `make resilience-drill` | passed locally: bounded load, replay without duplicate period, DB readiness recovery, failover/LKG and telemetry-backend isolation | Platform / Service owners | 2026-08-02 | passed |
| EV-11 | 19 image SBOM/scan/checksum manifest | `make release-bundle` | passed at `f4c7d1e...`: 19 commit-addressed images, 19 SPDX files, 19 Trivy reports and 39 checksummed artifacts; manifest SHA-256 `f008479a865caf5558cdd7390f0c09e0ec30e3a3cb21ab602e888df6e0a00571` | Platform / Security | final candidate only | passed |
| EV-12 | review/license/go-no-go consistency | `make production-readiness` | passed at `12fef12...` as a control: policy remains publication NO-GO with 18 image blockers and all 19 open hard gates are reported | Architecture / Security | 2026-08-26 | passed |
| EV-13 | expired client certificate rejection | `go test -mod=readonly ./internal/platform/httpserver -run TestMutualTLSRejectsExpiredClientCertificate -count=1` | one valid TLS 1.3 control succeeds; expired client request rejected | Security | 2026-08-26 | passed |
| EV-14 | bounded Access keyrings/loadprobe remediation | `go test -mod=readonly ./services/access/internal/credential ./tools/loadprobe` | focused admission suites passed | Security / Platform | 2026-08-26 | passed |

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
