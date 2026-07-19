# Open Questions

Open questions must be resolved before the milestone where they affect code, contracts, payments, privacy, or infrastructure.

| ID | Question | Blocks | Recommended next step |
|---|---|---|---|
| OQ-001 | Which jurisdiction is the seller registered in, and which buyer countries are allowed? | Real payments, production launch, geoblocking, privacy policy | Legal review before Stage 9 production readiness. |
| OQ-002 | Are YooKassa receipts, VAT, 54-FZ, or buyer email/phone required? | Stage 3 real-payment readiness | Keep YooKassa sandbox only until decided. |
| OQ-003 | Which domain, DNS/TLS provider, and edge stack will be used? | Stage 8 deployment and edge log redaction | Select provider and write runbook before production. |
| OQ-004 | Which VPS providers and regions are allowed by AUP and law? | Stage 6 production node onboarding | Review AUP and document approved provider list. |
| OQ-005 | What exact admin authentication will the CLI/internal API use? | Stage 7 admin operations | Decide between short-lived mTLS certs, SSO/OIDC, or hardware-backed credentials. |
| OQ-006 | What support and abuse contacts are real for production? | Stage 8/9 operations | Add contacts and response process before launch. |
| OQ-007 | What is the production lawful request process? | Stage 9 production readiness | Define process with legal counsel. |
| OQ-010 | What are production backup encryption keys and restore ownership? | Stage 8 | Define key management and restore drill. |
| OQ-011 | Which audited Stage 7 admin action creates a fresh higher-revision Access operation after terminal provisioning failure? | Stage 7 terminal recovery | Define authorization, reason, idempotency, and whether user confirmation is required; never reset Stage 6 rows manually. |

## Resolved During Stage 5

- OQ-009: retain a uniform `404 text/plain` response for malformed, unknown, expired, and revoked tokens. Happ accepts the standard subscription response, and the uniform failure minimizes account/token oracle behavior. ADR 0021 records the decision.

## Resolved During Stage 6

- OQ-008: pin official Xray-core `26.3.27` source at commit `d2758a023cd7f4174a5a5fa4ff66e487d4342ba0` and archive SHA-256 `14fa566ee0a801d3d51144c67018b449f5dcf462ddfacadd032da069787e61f9`; rebuild with the pinned Go toolchain and fixed `x/crypto`/`x/net` versions because the upstream binary fails the image vulnerability gate. Validate candidates with `xray run -test -config <path>`. ADR 0023 records the source and process.
