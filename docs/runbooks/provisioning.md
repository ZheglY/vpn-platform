# Provisioning and Node Recovery

This runbook covers the Stage 6 local control plane. It does not authorize production VPS enrollment, public node-agent exposure, manual database state changes, or copying credential payloads into tickets.

## Local Verification

Generate ephemeral material and run the isolated data-plane smoke:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/dev-mtls.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/dev-xray.ps1
make vpn-smoke
```

Generated material is under ignored `secrets/dev-mtls` and `secrets/dev-xray` on D. The smoke removes its Compose containers and volumes. Never reuse these keys outside local development.

## Provisioning Failure

1. Check aggregate operation counts by `state`, `kind`, and bounded `last_error_code`. Do not select credential material or outbox payloads.
2. Check node health, configured capacity, allocated count, region, and last-seen age. Do not force a node active while heartbeat is absent.
3. A primary failure remains terminal after bounded retries. A failover exhaustion may publish `degraded` only after primary success.
4. Restore the dependency or node first. A terminal result cannot be reset in Stage 6; a fresh higher-revision Access command requires the approved Stage 7 admin flow. Do not update operations, allocations, Access state, or Xray JSON manually.
5. Confirm reconciliation sees the expected revision. It refuses to overwrite a newer node revision by design and cannot turn an already published terminal failure into success.

## Node Heartbeat Loss

1. Confirm the management network and node-agent process are reachable with the expected certificate identity.
2. Check `/readyz` using that node's health certificate. The health identity cannot call desired-state routes.
3. Check Xray process health without printing its configuration. A missing heartbeat makes the node ineligible for new placement; existing allocations remain durable.
4. Restore node-agent and allow health polling plus reconciliation to converge. Do not delete allocation history to free capacity.

## Capacity

New allocations stop before `allocated_clients` reaches 80% of `capacity_limit`. A two-node placement requires two distinct healthy nodes in the paid region. Add capacity through reviewed seed/configuration and rerun health checks; do not lower `reserve_percent` below 20 or edit counters.

## Xray Reload Failure

1. Node-agent validates candidates with the pinned binary before replacement.
2. A validation or startup failure leaves or restores `last-known-good.json` and returns a safe `503` without Xray diagnostics.
3. Inspect only classified node-agent errors, agent/Xray version, and config revision. Never attach `config.json`, desired-state files, REALITY private keys, or process output.
4. Correct renderer/configuration code, run unit rollback tests and `make vpn-smoke`, then replay the desired operation.

## Sanitized DLQ Replay

DLQ notices contain source topic, partition, offset, payload SHA-256, and a reason code only. The raw source record remains in Kafka retention.

```powershell
docker compose run --rm provisioning-service replay-dlq access.provision.request.v1 0 42
```

Only `access.provision.request.v1` and `access.revoke.request.v1` are accepted. Replay transitions durable metadata from `available` to `requested`, reads the exact source coordinate, verifies SHA-256, republishes, and marks it `replayed`. A missing offset or hash mismatch must stop the procedure. Never retrieve or print the source value for diagnosis.

## Escalation

- Repeated primary failures, a newer actual revision, identity mismatch, hash mismatch, or failed last-known-good restart require security/operations escalation.
- Preserve IDs, timestamps, bounded reason codes, source coordinates, and image/version digests only. Exclude subscription URLs, VLESS UUIDs, REALITY private keys, raw Kafka records, and Xray configs.
