# Privacy Policy Notes for MVP

This is an engineering privacy document for the sandbox/MVP. It is not a legal privacy policy.

## Current Status

- Portfolio/sandbox only.
- No real sales.
- No final production jurisdiction.
- No production legal privacy policy yet.

## Data Minimization

Allowed data:

- Telegram user ID as stable identity input.
- Nullable Telegram display metadata only when needed for UX/support.
- Consent document type, version, accepted time, and source.
- Orders, payments, and refund records required for financial correctness.
- Subscription state and period ledger.
- AES-256-GCM-encrypted access credential records, token lookup HMACs, and client-facing endpoint snapshots.
- Aggregate bytes by credential/node.
- Node state, load, active credential count, health snapshots, config revision.
- Security/admin audit records.

Forbidden data:

- DNS requests.
- Visited domains.
- Destination IP addresses from user traffic.
- Packet contents.
- Browsing history.
- Full Telegram update/message payloads.
- Full YooKassa webhook/provider payloads unless a later ADR proves necessity, redaction, and retention.
- Subscription URL/token in plaintext storage, logs, metrics, traces, or support views.
- VLESS UUIDs and REALITY private keys in logs, metrics, traces, or support views.
- Happ Provider ID or HWID/device identifiers in Stage 5.

## Sandbox Retention

| Data class | Retention |
|---|---:|
| Application logs | 14 days |
| Security/admin audit, including provisioning-material read metadata without credential payload | 365 days |
| Diagnostic data | 30 days |
| Aggregate traffic statistics | 30 days |
| Payment records | No automatic deletion until legal requirements are known |

Retention must be configurable.

## User-Facing Workflows Required Later

- Privacy export.
- Privacy deletion/anonymization where legally allowed.
- Token rotation for leaked or lost subscription URL.
- Support-safe views without credential leakage.

## Production Blockers

- Legal privacy policy.
- Seller jurisdiction.
- Buyer countries.
- Data processor/subprocessor list.
- VPS provider AUP and data handling review.
- Lawful request process.
- Financial record retention requirements.
