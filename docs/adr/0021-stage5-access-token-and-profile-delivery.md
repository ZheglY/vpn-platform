# ADR 0021: Stage 5 access token and profile delivery

- Status: Accepted
- Date: 2026-07-18
- Owners: access-service
- Security impact: High
- Contract impact: OpenAPI and AsyncAPI Stage 5 contracts
- Supersedes: ADR 0017 only where it listed placement/public endpoint data in the provisioning-material response

## Context

Stage 5 must turn an active entitlement into a Happ-compatible subscription document without claiming that Xray provisioning succeeded prematurely. Subscription URLs and VLESS UUIDs are bearer credentials. Normal HTTP idempotency response replay would require retaining the plaintext URL, which conflicts with ADR 0007 and ADR 0016.

Provisioning placement and node mutation belong to Stage 6. Access-service nevertheless needs a durable endpoint snapshot after provisioning succeeds so it can render a deterministic client profile without reading another service's database.

## Decision

1. Access-service owns credentials, token lookup HMACs, endpoint snapshots, access operations, inbox, and outbox in its own PostgreSQL database.
2. A VLESS UUID is generated from `crypto/rand` and stored with versioned AES-256-GCM encryption bound to its credential ID as associated data. The active key version and a versioned key set are supplied outside the database so old rows remain decryptable during rotation. The same key is not used for token lookup.
3. A subscription token is 32 random bytes encoded with unpadded base64url. Only HMAC-SHA-256 under a separate 32-byte key is stored. Plaintext exists only while building the successful issue or rotate response.
4. Issue and rotate requests require an idempotency key and serialize on the subscription. The database records that a key completed but cannot replay its secret response. A repeated completed key returns `409 idempotency_response_unavailable`; callers must not silently rotate. This is an intentional one-time-delivery contract.
5. Initial issue is allowed once when credential state is `active` or `degraded` and entitlement time has not elapsed. Rotation immediately revokes the previous token in the same transaction and returns one replacement.
6. `GET /s/{token}` computes the HMAC before lookup and gives malformed, unknown, expired, revoked, and unavailable credentials the same generic `404 text/plain` response. Both success and failure use `Cache-Control: no-store`; logs use the route template, never the URL path.
7. A provisioning command carries only operation ID, credential ID, and revision. Provisioning-service fetches the VLESS UUID through its allowlisted mTLS endpoint. REALITY private keys never leave nodes.
8. `access.provision.succeeded.v1` supplies a validated snapshot of client-facing endpoint data: node ID, role, address, port, server name, REALITY public key, short ID, optional spider path, and label. Access-service stores that snapshot atomically with readiness. It contains no VLESS UUID, subscription token, or REALITY private key.
9. `access.ready.v1` is emitted only after a current provisioning operation succeeds. The event contains identifiers and readiness state, never a URL or credential.
10. Entitlement expiry/revocation immediately invalidates active tokens and emits a secret-free revoke command. Physical removal from Xray and terminal revoke reconciliation are Stage 6.
11. Happ Provider ID, HWID, advanced management flags, and traffic collection are omitted. Standard `profile-title`, `profile-update-interval`, `subscription-userinfo`, and optional `support-url` headers are sufficient for Stage 5.
12. The public endpoint applies an atomic Redis fixed-window limit by HMAC-derived source-IP and token keys with TTL. Redis contains no raw token or IP and remains ephemeral; an unavailable limiter fails closed with a no-store 503.

## Consequences

- A response lost after commit cannot be recovered; support or the user must explicitly rotate. This limits plaintext retention and makes duplicate behavior visible.
- Database theft alone does not reveal subscription URLs or VLESS UUIDs without both external keys.
- Access-service can render profiles from its own durable snapshot and does not read provisioning storage.
- Stage 5 integration tests can inject provisioning outcomes, but a real Xray connection is not an acceptance claim until Stage 6.
- Key rotation requires retaining old credential-encryption keys until all rows are re-encrypted. The token HMAC key cannot be rotated without reissuing URLs; its operational procedure is deferred to Stage 8 hardening.

## Rejected alternatives

- Persisting plaintext or reversibly encrypted subscription URLs for idempotent replay: rejected because it creates a durable credential-recovery path.
- Marking access ready immediately after entitlement activation: rejected because payment entitlement and VPN provisioning are separate facts.
- Letting provisioning-service write access-service endpoint tables: rejected as cross-service database access.
- Rendering from provisioning-service at request time: rejected because it couples the public endpoint to another service and weakens availability.
