# Stage 9 Evidence Index

Review owner: Platform / Security  
Evidence date: 2026-07-26  
Source commit: pending final Stage 9 evidence run  
Heavy artifact policy: generated reports remain under ignored `tmp/`; the final
release manifest/checksums and aggregate outcomes are recorded after execution

No entry may contain a real subscription URL, VLESS UUID, private key, provider
or Telegram payload, customer identifier, destination, DNS history, or packet
content.

| Evidence ID | Control | Command | Safe aggregate result | Owner | Fresh until | Status |
|---|---|---|---|---|---|---|
| EV-01 | source/unit/race/lint/vulnerability/contracts/images | `make verify` | pending final run | Platform / Security | 2026-08-02 | pending |
| EV-02 | payment ambiguity, migrations, service E2E | `make compose-smoke` | pending final run | Service owners | 2026-08-02 | pending |
| EV-03 | full Access/Provisioning/Xray flow | `make vpn-smoke` | pending final run | Access / Provisioning / Node | 2026-08-02 | pending |
| EV-04 | Notification/Admin ordering/recovery | `make stage7-smoke` | pending final run | Notification / Admin | 2026-08-02 | pending |
| EV-05 | metrics/traces/logs privacy and target inventory | `make observability-smoke` | pending final run | Platform / Security | 2026-08-02 | pending |
| EV-06 | forced backup failure cleanup | `make backup-cleanup-test` | pending final run | Platform / DBA | 2026-08-02 | pending |
| EV-07 | encrypted restore of eight databases | `make backup-restore-drill` | pending final run | Platform / DBA | 2026-08-02 | pending |
| EV-08 | Debian host hardening/systemd/firewall | `make node-hardening-test` | admission run passed; final rerun pending | Node / Operations | 2026-08-02 | pending |
| EV-09 | mTLS/provider/Access/REALITY rotation | `make secret-rotation-drill` | pending final run | Security / secret owners | 2026-08-02 | pending |
| EV-10 | load, Kafka/DB/node/observability faults | `make resilience-drill` | pending final run | Platform / Service owners | 2026-08-02 | pending |
| EV-11 | 19 image SBOM/scan/checksum manifest | `make release-bundle` | pending final run | Platform / Security | final candidate only | pending |
| EV-12 | review/license/go-no-go consistency | `make production-readiness` | pending final run | Architecture / Security | 2026-08-26 | pending |
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
