# ADR 0034: Stage 8 VPN node host hardening

- Status: Accepted for Stage 8 implementation
- Date: 2026-07-26
- Owners: platform, provisioning, node, and operations
- Security impact: High
- Contract impact: deployment and node-agent runtime configuration only
- Extends: ADR 0012, ADR 0021, ADR 0024, and ADR 0027
- Amended by: ADR 0039

## Context

Local Compose deliberately co-locates node-agent and its Xray child. A production node needs an explicit host boundary, a private management plane, least-privilege credentials, repeatable hardening, and rollback that cannot be bypassed through arbitrary sudo or shell input.

## Decision

1. The supported host baseline is Debian 12. The Ansible role refuses other distributions and configures sysctl, SSH policy, nftables, WireGuard management, users, groups, directories, credentials, and systemd units declaratively.
2. The role owns only the `inet vpn_node` nftables table and never flushes the host ruleset. Its input chain defaults to deny and admits established traffic, loopback, WireGuard, the configured public Xray port, and SSH/node-agent management only from the approved source through the WireGuard interface. Provider and operator tables remain intact.
3. `vpn-node-agent` and `xray` are separate non-root identities. Parent credential paths are traversable only by the narrow `vpn-xray` sharing group; each service keeps its own primary group and receives `vpn-xray` only as a supplementary group. This lets node-agent read its TLS and REALITY inputs and write candidate/current configuration, while Xray reads its REALITY input and generated configuration. Neither identity owns the other's executable or private credentials.
4. Production-mode node-agent renders and validates a candidate, atomically preserves current as last-known-good, and uses the fixed control protocol in ADR 0039. The earlier sudoers design is superseded because `NoNewPrivileges=yes` intentionally makes sudo escalation unavailable.
5. The Xray unit and node-agent unit use systemd sandboxing, explicit writable paths, capability removal, and restart limits. A failed reload restores last-known-good and requires it to become healthy before the operation returns.
6. mTLS deployment supports an old/new trust bundle during rotation. Private keys are owner-readable only; certificates may be group-readable only where the runtime boundary requires it.
7. `make node-hardening-test` applies the role twice to a digest-pinned systemd-enabled Debian container, requires zero changes on the second pass, and checks users, modes, unit restrictions, WireGuard, fixed control units, first boot, reboot, fail-closed validation, last-known-good preservation, and preservation of a pre-existing nftables table. It reads each private key under the actual service identity and starts both node-agent and Xray through systemd. The systemd manager rollback tests also run natively on Linux.

## Consequences

- Compromise of node-agent does not directly grant the Xray service identity or arbitrary root command execution.
- Host policy is reviewable and idempotent without claiming ownership of unrelated host firewall policy, while Compose keeps its process manager for deterministic local development.
- The disposable container proves generated host state but cannot prove a cloud firewall, kernel-specific behavior, provider console recovery, or real WireGuard routing.

## Rejected alternatives

- Run node-agent and Xray as one root service: rejected because management compromise would immediately own VPN process and host.
- Give node-agent unrestricted `systemctl` or a shell wrapper: rejected because request-controlled input could become command execution.
- Treat a Docker healthcheck as host-hardening evidence: rejected because it does not exercise identities, file modes, firewall, or systemd policy.
