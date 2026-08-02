# ADR 0039: Xray First Boot And Fixed Systemd Control

- Status: Accepted for Stage 9 remediation
- Date: 2026-08-02
- Owners: platform, provisioning, node, and operations
- Security impact: High
- Contract impact: node host deployment only; no public API or Kafka change
- Amends: ADR 0034

## Context

The Stage 8 role enabled Xray before node-agent had rendered an initial validated
configuration. It also delegated an exact sudo command while the node-agent unit
used `NoNewPrivileges=yes`; that sandbox correctly prevents sudo from acquiring
new privilege. A plain asynchronous file trigger is insufficient because an
already active Xray process could be mistaken for completion of a newer reload.
`Type=simple` also reports node-agent active before its Xray manager has finished
bootstrap.

## Decision

1. `xray.service` is static and has `ConditionFileNotEmpty` for the managed
   current configuration. It is never enabled directly. The role enables the
   fixed reload path first and then starts node-agent, which renders and validates
   the initial candidate before requesting Xray start.
2. Node-agent remains non-root with `NoNewPrivileges=yes`. Its root-owned control
   helper accepts only `reload` and `status`. Reload creates an atomic, unique,
   identifier-only request in `/run/vpn-node-control`; a root systemd path/service
   worker executes the fixed `systemctl restart xray.service` operation.
3. The worker writes an atomic `request_id outcome` acknowledgement readable only
   by root and the node-agent group. Node-agent accepts success only for its exact
   request ID. Timeout removes the caller's request and fails closed. No config,
   credential, arbitrary unit, or caller-supplied command enters this protocol.
4. Xray maintains an identifier-free runtime health marker through
   `ExecStartPost` and `ExecStopPost`. The non-root status helper reads that
   marker; the privileged worker independently confirms systemd active state
   before acknowledging reload success.
5. `node-agent.service` uses `Type=notify`. The Go process sends `READY=1` only
   after state loading and Xray manager bootstrap complete. Xray validation and
   control subprocesses receive no `NOTIFY_SOCKET`, preventing child processes
   from impersonating main-process readiness.
6. Ansible inspects pre-deployment state and performs at most one start or
   restart after an input change. It waits for systemd readiness, Xray health,
   and sustained node-agent activity. Invalid initial configuration leaves Xray
   stopped; an invalid later candidate leaves current and running last-known-good
   unchanged.

## Consequences

- First boot has a deterministic producer-before-consumer ordering for Xray
  configuration without granting node-agent systemd or sudo privilege.
- The local disposable Debian test now exercises the real node-agent binary,
  generated mTLS, failure on the initial candidate, successful clean bootstrap,
  idempotence, last-known-good preservation, and reboot recovery.
- Provider firewall, kernel, WireGuard routing, console recovery, and real VPS
  behavior still require a separately approved production-host exercise.

## Rejected Alternatives

- Enable Xray and seed `{}` before node-agent: rejected because the file is not a
  valid production configuration and bypasses the owner validation path.
- Allow sudo from the sandboxed node-agent: rejected because it conflicts with
  `NoNewPrivileges` and expands the privilege boundary.
- Return success after dropping an asynchronous marker: rejected because stale
  process health is not proof that the requested config was applied.
- Give node-agent direct systemd D-Bus mutation rights: rejected because a
  compromised management process could operate unrelated units.
