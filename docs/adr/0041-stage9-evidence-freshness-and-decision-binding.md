# ADR 0041: Evidence Freshness And Decision Binding

- Status: Accepted for Stage 9 remediation
- Date: 2026-08-02
- Owners: architecture, security, operations, legal, and product
- Security impact: High
- Contract impact: production-readiness review metadata only
- Amends: ADR 0038

## Context

The first readiness validator required a date string but did not compare it with
UTC time. It accepted `CONDITIONAL GO`, omitted `ARCH-03` from the required set,
and compared only status, severity, and blocking between Markdown and JSON. A
future date, stale evidence, or drift in requirement/owner/evidence could
therefore pass the control.

## Decision

1. `go-no-go.json` format 2 permits only `GO` and `NO-GO`. There is no conditional
   state that bypasses a hard gate.
2. The gate inventory is exact and closed, including `ARCH-03`. Missing,
   duplicate, or unknown IDs fail validation.
3. Every gate declares `evidence_type`. `immutable` evidence has no
   `valid_until`; `expiring` evidence has an actual UTC date. Review dates cannot
   be in the future, expiring evidence cannot be stale, and overall decision
   validity cannot outlive any expiring gate.
4. Severity is restricted to `P0`, `P1`, or `P2`; status is restricted to
   `passed`, `blocked`, or `pending`. `GO` requires every gate, including a
   nonblocking gate, to be `passed`. Any open blocking gate forces `NO-GO`.
5. Markdown and JSON must agree exactly on decision metadata and all gate fields:
   ID, requirement, owner, evidence, status, severity, evidence type, review
   date, validity, and blocking. The immutable validity cell is the literal `-`,
   not a magic future date or string in JSON.
6. Repository file paths named in evidence must exist. Every full 40-character
   commit named in evidence must resolve to a Git commit in the current
   repository. Short display hashes alone are not machine evidence.
7. A truthful fresh `NO-GO` is a valid review-control result. Stale review data
   is invalid even when the decision is `NO-GO`; owners must refresh the record
   rather than allowing an old blocker inventory to appear current.

## Consequences

- `make production-readiness` proves representation and freshness, not launch
  eligibility. The current external legal/provider/PKI/HA/registry gates remain
  blocked and the platform remains `NO-GO`.
- Time-sensitive evidence needs explicit renewal. Accepted milestone evidence
  can be immutable only when its fact cannot be invalidated by environment or
  dependency changes.
- Tests inject UTC time and cover missing gates, stale evidence, illegal magic
  expiry, conditional decisions, open-gate GO, Markdown drift, missing files,
  and missing commits.

## Rejected Alternatives

- Treat any nonempty date as fresh: rejected because future and expired evidence
  are operationally different.
- Keep `CONDITIONAL GO`: rejected because it creates an ambiguous path around
  binary launch blockers.
- Compare only status fields: rejected because changed ownership or substituted
  evidence is decision-significant drift.
