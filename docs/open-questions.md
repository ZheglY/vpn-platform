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
| OQ-008 | What is the Xray-core version pin and validation command? | Stage 5/6 | Re-check official Xray-core release and security notes before implementation. |
| OQ-009 | Does Happ UX require changing invalid token response from `404` to another strategy? | Stage 5 | Run compatibility tests against official Happ behavior. |
| OQ-010 | What are production backup encryption keys and restore ownership? | Stage 8 | Define key management and restore drill. |
