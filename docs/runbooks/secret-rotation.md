# Secret Rotation

This runbook defines Stage 8 overlap and rollback order. Local evidence uses generated credentials only.

## Local Drill

```powershell
make secret-rotation-drill
```

The drill must emit only a bounded status report. It must not print keys, provider credentials, subscription URLs, UUIDs, request bodies, or certificate subjects from a real environment.

## mTLS

1. Issue the new CA/leaf generation through the approved PKI.
2. Add new trust while retaining old trust; verify both generations.
3. Roll server and client leafs in bounded batches. Confirm TLS 1.3, exact SPIFFE authorization, scrape/OTLP health, and service readiness.
4. Observe for the approved overlap window.
5. Remove old trust and revoke the old generation.

Rollback before compromise confirmation restores the old leaf while both roots are trusted. After compromise confirmation, do not restore the compromised key; issue a third generation and revoke.

## Access Encryption and HMAC

1. Add a new version to `ACCESS_CREDENTIAL_KEYS` and `ACCESS_TOKEN_HMAC_KEYS`. Keep encryption and HMAC material distinct.
2. Deploy readers with old and new versions while the old version remains active.
3. Change the explicit active versions so new ciphertext and token hashes use the new keys.
4. Verify database version counts, one-time issue/rotation, profile fetch, replay behavior, and restart recovery.
5. Retain old read keys for the approved maximum overlap. Remove them only after no required row/version remains.

Rollback changes the active writer back while preserving both read generations. Never rewrite ciphertext or token hashes through ad hoc SQL.

## Telegram and YooKassa

1. Create the new provider credential and keep the old one valid where the provider supports overlap.
2. Inject only into the owning service, restart a canary, and verify typed success/authentication behavior plus safe metrics.
3. Roll all replicas, observe webhook and delivery health, then revoke the old credential.
4. On ambiguous failures, restore the old credential without logging either value. Reconcile payment state through the Billing runbook.

The local drill tests client behavior only and does not contact Telegram or YooKassa.

## REALITY

1. Generate a distinct X25519 generation on the target node or replacement endpoint.
2. Keep the previous endpoint available and publish both endpoints through the normal Access/Provisioning flow.
3. Wait for the approved Happ refresh window and verify canary traffic through the new endpoint.
4. Drain old allocations, remove the old private key, and verify revoke.

Do not replace the key under one live endpoint and assume cached clients will recover. Rollback re-admits the old endpoint only if its key is not compromised.
