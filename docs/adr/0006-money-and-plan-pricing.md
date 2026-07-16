# ADR 0006: Money Representation and Plan Pricing

Status: Accepted

Date: 2026-07-12

## Context

Payment correctness is central to the product. Floating point money can introduce rounding errors. The MVP plan exists for sandbox testing, but price and currency must not leak into subscription domain rules.

## Decision

Represent money as integer minor units plus ISO 4217 currency code.

Examples:

- `amount_minor bigint`
- `currency char(3)`

Do not use `float32`, `float64`, JSON floating point values, or binary floating point arithmetic for money.

The first test plan is seeded by configuration:

- duration: 30 days;
- no hard traffic cap;
- one selected region;
- one primary node and one failover;
- grace period: 24 hours.

Price and currency are seed/configuration data owned by catalog/billing, not hardcoded in subscription domain logic.

## Consequences

- Orders must snapshot plan and price at creation time.
- Published plan history must not be rewritten in a way that changes old orders.
- Payment verification must compare expected amount and currency exactly.
