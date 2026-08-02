# ADR 0038: Stage 9 production-readiness evidence and fail-closed gates

- Status: Accepted for Stage 9 review
- Date: 2026-07-26
- Owners: architecture, security, legal, platform, and product
- Security impact: High
- Contract impact: release review metadata only
- Extends: ADR 0013, ADR 0032, ADR 0036, and ADR 0037
- Amended by: ADR 0040 and ADR 0041

## Context

Local tests can prove application behavior but cannot prove legal eligibility,
provider terms, production HA, PKI revocation, alert delivery, registry custody,
or multi-host rollback. A review that reports only green local tests can be
misread as launch approval. SPDX scanners can also emit missing, ambiguous, or
compound license assertions that are not legal conclusions.

## Decision

1. Stage 9 may be technically complete with a production decision of `NO-GO`.
   Review completion never authorizes deployment, provider credentials, real
   data, registry publication, VPS enrollment, DNS, or customer traffic.
2. `docs/reviews/stage9/go-no-go.json` is the machine-readable gate source. ADR
   0041 supersedes the original untyped freshness representation and fixes the
   exact required gate inventory. Any blocking status other than `passed`
   forces `NO-GO`.
3. `make production-readiness` validates the required reports/runbooks, minimum
   gate inventory, decision consistency, and license-policy relationship. It
   succeeds when the review truthfully records `NO-GO`; it is not a deployment
   gate.
4. `deploy/release/license-policy.json` maps all 19 release images. The review
   validator requires `NO-GO` while an image is blocked or requires counsel.
   The separate `make license-publication-gate` requires overall license `GO`,
   every image approved, all final SBOMs present, and exact allowlisted SPDX
   assertions. Missing/unknown/compound/unapproved expressions fail closed.
5. No automated policy decides whether AGPL, MPL, Redis source-available terms,
   or distribution obligations are legally compatible. Those require
   professional review. The strict exact-expression policy deliberately prefers
   a false block over an unreviewed publication.
6. Evidence records a concrete source commit, command, safe aggregate result,
   owner, date, and freshness. Heavy SBOM/scan outputs remain ignored and are
   checksum-bound by the release manifest. Sensitive environment evidence stays
   in an approved restricted store.
7. A local stop/start, generated certificate, inert alert receiver, or local
   image ID cannot be relabeled as production HA, revocation, paging, or registry
   evidence. Unavailable critical drills remain explicit blockers.
8. Runtime/public HTTP, Kafka, payment, retention, and security semantics are
   unchanged by this ADR.

## Consequences

- Review evidence is reproducible and cannot accidentally promote an incomplete
  launch decision.
- The regular verification suite can remain green while the production
  publication gate is intentionally red for documented external decisions.
- Moving to `GO` requires both policy updates and real evidence; changing text
  alone cannot satisfy the machine-readable inventory.

## Rejected alternatives

- Treat every SPDX scanner value as approved: rejected because metadata can be
  absent, ambiguous, or legally incomplete.
- Make the normal CI suite fail forever on known external blockers: rejected
  because it would hide regressions in executable local controls. The strict
  publication gate remains separately mandatory.
- Mark unavailable production drills as passed from local simulations: rejected
  because this creates false operational assurance.
