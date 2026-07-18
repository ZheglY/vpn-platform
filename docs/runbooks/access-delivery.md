# Access Delivery and Leaked-Token Response

This runbook covers the Stage 5 access-service. It does not operate VPN nodes or Xray; physical provisioning and revoke reconciliation begin in Stage 6.

## Safe Signals

Use aggregate state and counts only. Do not print `vless_uuid_ciphertext`, token lookup HMACs, full URLs, endpoint profile bodies, Kafka payloads, or raw request paths into tickets or chat.

Useful health endpoints:

```text
GET /livez
GET /readyz
GET /version
GET /metrics
```

Useful state fields from the allowlisted internal API are `access_status`, `provisioning_status`, and `token_status`. A Subscription `active` entitlement does not imply Access `ready`.

## Provisioning Does Not Become Ready

1. Confirm access-service PostgreSQL and Kafka readiness.
2. Check consumer lag for subscription lifecycle and provisioning outcome topics without dumping records.
3. Check that one `access.provision.request.v1` outbox row is pending, processing, or published by aggregate count.
4. If the command is published, hand off to the Stage 6 provisioning runbook. Do not manually mark access active or insert endpoint snapshots.
5. A terminal `access.provision.failed.v1` leaves the credential failed. Retry policy and operator replay tooling require Stage 6 approval.

If public profile requests return 503 while PostgreSQL and Kafka are healthy, check Redis readiness and the ephemeral rate-limit Lua operation. Do not bypass the limiter in production; restore Redis or drain traffic to a healthy instance.

## Lost One-Time Issue Response

The plaintext URL is intentionally not recoverable. A replay of the completed idempotency key returns `409 idempotency_response_unavailable`.

1. Confirm the caller did not receive or send the original response using caller-side delivery state, never by searching logs for the URL.
2. Invoke the authenticated rotate endpoint with a new idempotency key.
3. Deliver the returned URL once and do not persist it.
4. Confirm the previous URL returns the generic 404 and the replacement returns a no-store Happ profile.

## Suspected Leaked Subscription URL

1. Treat the full URL as a bearer credential. Do not paste it into issue trackers, logs, metrics queries, or shell history.
2. Use the authenticated rotate endpoint with a new idempotency key. Rotation revokes the previous token in the same transaction that creates the replacement.
3. Deliver the replacement through the approved user channel.
4. Confirm old-token 404 and new-token 200 without recording response bodies.
5. Investigate edge, proxy, client, and support-system path logging. Access-service application logs should contain only `GET /s/{token}`.
6. If the HMAC key may be exposed, all subscription URLs require reissuance; stop public issuance and escalate as a security incident.

## Credential Encryption Key Incident

1. Stop new credential creation if the active AES key may be unavailable or compromised.
2. Do not delete an old key version while rows still reference it.
3. Restore key access from the approved secret manager or backup owner. Stage 8 must define production re-encryption and rotation tooling.
4. Database-only restoration without the matching key versions is incomplete and must not be declared successful.

## Local Verification

```powershell
go test ./services/access/...
npm run lint:contracts
make compose-config
make compose-smoke
```

Compose smoke injects a contract-valid provisioning result, tests mTLS authorization, one-time issue replay, Happ headers/body, token log redaction, and Access PostgreSQL invariants. It does not prove a live VPN connection.

## Rollback

- Stop access-service consumers and public ingress before rolling back application code.
- Do not run the destructive Goose `down` migration in a shared or production database.
- Keep the database and external keys intact so a forward fix can resume inbox/outbox processing.
- A rollback must preserve uniform public 404 behavior and must never re-enable a previously rotated or revoked token.
