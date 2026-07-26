# VPN Node Hardening

This runbook covers the Stage 8 Debian 12 Ansible baseline and local disposable validation. It does not authorize enrollment of a production VPS.

## Local Validation

```powershell
make node-hardening-test
```

The test builds a digest-pinned Debian 12 target, applies `deploy/ansible/playbooks/vpn-node.yml` twice, requires zero changes on the second pass, and checks:

- separate `vpn-node-agent` and `xray` non-root users;
- private-key, certificate, WireGuard, and Xray config ownership/modes plus successful reads under the actual service identities;
- a role-owned default-deny `inet vpn_node` table with explicit management and Xray rules, applied twice without removing a pre-existing host table;
- SSH and sysctl hardening;
- hardened systemd units that start both node-agent and Xray as separate non-root users;
- an exact `reload`/`status` helper and narrow sudoers entry.

The Linux Go test for `systemdManager` must also prove a failed candidate reload restores last-known-good and returns only after health recovers.

## Host Enrollment

1. Confirm Debian 12, console recovery, provider firewall ownership, approved SSH management CIDR, public Xray port, and WireGuard addresses.
2. Create the production inventory outside the repository. Store no private key or host credential in Git.
3. Review the role-owned `inet vpn_node` table against provider firewall rules and all existing host tables. Node-agent management must be reachable only over WireGuard; do not replace or flush operator/provider firewall state.
4. Stage mTLS and REALITY material through the approved secret channel with the owner/group/modes in ADR 0034.
5. Run Ansible in check mode, review every change, then apply with an incident owner present.
6. Verify WireGuard, node-agent mTLS authorization, Xray health, capacity visibility, and a canary credential before admitting the node to placement.
7. Run Ansible again and require idempotence. Archive only identifier-free change and validation evidence.

## Rollback

1. Stop new placement to the node.
2. Drain or rebind assigned credentials through Provisioning. Do not edit allocation tables.
3. Restore the previously reviewed unit/config package and last-known-good Xray configuration.
4. Reload through the fixed helper and require Xray health plus canary traffic.
5. If management access is lost, use the provider console and the reviewed previous nftables configuration. Do not open management publicly as a shortcut.

## Production Blockers

- Approved VPS/provider, inventory custody, SSH and WireGuard key custody.
- Provider firewall review and console recovery exercise.
- Production CA issuance/revocation and secret distribution.
- Kernel/version qualification and a real systemd host test.
- Maintenance window, node drain policy, on-call ownership, and rollback approval.
