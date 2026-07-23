# Support and Abuse Runbook Placeholder

Status: Placeholder for sandbox.

Real support and abuse contacts are not required for the sandbox portfolio environment, but production must define them before launch.

## Required Before Production

- Public support contact.
- Public abuse contact.
- Internal escalation owner.
- Severity levels and response time targets.
- Process for suspected credential sharing.
- Process for payment dispute.
- Process for unlawful or abusive usage report.
- Process for lawful request intake and legal review.
- Audit requirements for every operator action.
- Privacy-safe support view that does not reveal subscription tokens, VLESS UUIDs, REALITY private keys, destination IPs, DNS history, or packet contents.

## Sandbox Defaults

- No real customer support channel.
- No real abuse mailbox.
- The Stage 7 admin CLI requires an operations certificate, explicit permission, human reason, idempotency key, owner execution, and append-only audit for a subscription revoke. It does not initiate a refund or expose VPN credentials.
