# ADR 0030: Stage 8 SLI, SLO, and alert routing

- Status: Accepted for Stage 8 implementation
- Date: 2026-07-25
- Owners: platform, access, and operations
- Security impact: Medium
- Contract impact: operational metrics and alerts only
- Extends: ADR 0013, ADR 0027, ADR 0028, and ADR 0029
- Superseded in part by: ADR 0033

> Historical note: decision 3 and the asynchronous wording in decisions 4-6 describe the first slice. ADR 0033 replaces them with the durable successful-payment denominator and pending-over-60 behavior.

## Context

The specification defines monthly availability and latency objectives, but raw HTTP and workflow metrics do not define which requests are eligible, what is considered bad, or how an operator is warned before a monthly error budget is exhausted. The payment-to-provisioning objective also crosses asynchronous service boundaries and cannot be inferred reliably from unrelated scrape timestamps.

Alertmanager configuration is needed for validation and routing semantics, but no production receiver, contact, or credential has been approved.

## Decision

1. The control API availability SLI covers registered non-health routes from Identity, Catalog, Billing, Subscription, Access, Provisioning, Notification, and Admin. Every eligible request is in the denominator and a `5xx` response is bad. Client errors do not consume the availability budget.
2. The public subscription SLI covers only `GET /s/{token}` in Access. Raw bearer paths never enter a metric. Every response is in the denominator and `5xx` is bad; the uniform `404` security behavior is not an availability failure.
3. Access records `vpn_access_payment_to_provisioning_seconds` only after the transaction for a new `subscription.activated.v1` credential and its provisioning outbox command commits. The source timestamp is the immutable initial period start. Exact event replay, extension, revoke, and a failed transaction do not create another observation. Values are bounded at 30 days and have no labels.
4. The availability objectives are 99.9% monthly for the control API and 99.95% monthly for the subscription endpoint. The initial asynchronous objective is 99% of initial activations creating the durable provisioning command within 60 seconds.
5. Recording rules calculate request, bad-event, and error-ratio rates over 5-minute, 30-minute, 1-hour, and 6-hour windows. Fast alerts require both 1-hour and 5-minute burn above 14.4 times budget. Slow alerts require both 6-hour and 30-minute burn above 6 times budget. Empty traffic does not alert.
6. Latency is tracked separately from availability budget: control API p95 over 5 minutes must remain below 300 ms and warm subscription rendering below 200 ms. A 15-minute breach is a warning.
7. SLI rules use only bounded labels already approved by ADR 0028. They do not add identifiers, raw paths, error text, provider data, VPN material, or traffic metadata.
8. Alertmanager `v0.33.1` is an integrity-pinned validation baseline with grouped critical and default routes, inhibition, and privacy-safe templates. Both receivers are deliberately inert and contain no URL, token, email, or production contact. This configuration proves routing syntax only and must never be described as production delivery.
9. `promtool` tests cover healthy and burning SLO states. `amtool check-config` is a mandatory validation gate. A production receiver, escalation owner, paging test, and credential injection require separate approval.

## Consequences

- Availability and asynchronous delay now have explicit numerators, denominators, budgets, and actionable multi-window alerts.
- The payment-to-provisioning metric measures the locally observable durable command boundary. It does not claim that Xray has applied the credential or that a notification was delivered.
- An inert receiver prevents accidental notification delivery from a portfolio environment while keeping the production routing shape reviewable.
- Monthly reporting and production paging remain deployment responsibilities; the short windows are budget-burn detectors, not a substitute for a monthly report.

## Rejected alternatives

- Derive payment-to-provisioning delay from two independently scraped counters: rejected because scrape timing and restart loss make individual durations ambiguous.
- Include raw route paths or subscription tokens: rejected for secrecy and cardinality.
- Treat every `4xx` as downtime: rejected because authentication, validation, and uniform token `404` responses are intentional client-visible outcomes.
- Commit a placeholder webhook or email receiver: rejected because even fake destinations encourage secret handling and can be mistaken for working escalation.
