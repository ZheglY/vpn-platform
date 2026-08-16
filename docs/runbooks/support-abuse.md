# Support and Abuse Runbook

Status: Production launch blocker.

Real support and abuse contacts must be defined before production launch. Local validation uses no external support channel.

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

## Local Defaults

- No real customer support channel.
- No real abuse mailbox.
- The Stage 7 admin CLI requires an operations certificate, explicit permission, human reason, idempotency key, owner execution, and append-only audit for a subscription revoke. It does not initiate a refund or expose VPN credentials.
