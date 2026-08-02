# ADR 0035: Stage 8 secret rotation

- Status: Accepted for Stage 8 implementation
- Date: 2026-07-26
- Owners: security, platform, billing, access, telegram, and node operations
- Security impact: High
- Contract impact: deployment configuration and Access owner schema only
- Extends: ADR 0013, ADR 0017, ADR 0021, and ADR 0034

## Context

Long-lived mTLS, provider, Access, and REALITY credentials need overlap and rollback rules. Immediate replacement without read overlap can break subscriptions, while indefinite overlap leaves compromised generations valid.

## Decision

1. mTLS rotation uses four phases: add the new CA/certificate to trust, cut clients and servers to new leafs, observe, then retire the old CA. Rollback restores the old leaf and trust only before confirmed compromise; a compromised generation is revoked instead.
2. Access encryption and token HMAC use independent versioned keyrings with at most four versions. Writes use one explicit active version. Reads may try only the configured bounded set. Encryption-key versions are persisted with ciphertext; token rows persist the active HMAC version and lookup computes bounded candidates without storing plaintext tokens.
3. No HMAC key may equal any active or overlapping credential-encryption key. Old keys are removed only after the maximum subscription/token overlap and database verification. Rollback changes the active writer while retaining both required read generations.
4. YooKassa and Telegram rotations are provider-managed two-generation procedures where supported: introduce the new credential, restart/canary the owning process, verify only typed responses and safe metrics, then revoke the old credential. Full provider payloads and secrets never enter drill output.
5. REALITY generations use distinct X25519 private keys. Rotation provisions a new endpoint or drained node generation, exposes both subscription endpoints during overlap, waits for the approved client refresh window, and then retires the old endpoint. A private key is never copied into logs, metrics, reports, or tickets.
6. `make secret-rotation-drill` proves mTLS overlap/cutover/retirement/rollback in memory, distinct REALITY generations, Access encryption/HMAC overlap and rollback, fixed provider-client failure handling, and Xray last-known-good rollback. It uses no real provider or production credential.

## Consequences

- Rotation is bounded and reversible without turning old keys into permanent fallback.
- Existing encrypted credentials and subscription tokens survive approved overlap windows.
- Provider and REALITY production rotations remain operator changes requiring provider/VPS access, monitoring, and a separate approval.

## Rejected alternatives

- Replace every key atomically with no overlap: rejected because cached clients and rolling processes would fail.
- Try an unbounded key history on every request: rejected for denial-of-service cost and indefinite compromise exposure.
- Reuse one 32-byte key for encryption and HMAC: rejected because it couples two cryptographic domains and their rotation.
