# ADR 0040: Release Inventory And License Publication Gate

- Status: Accepted for Stage 9 remediation
- Date: 2026-08-02
- Owners: platform, security, legal, and release operations
- Security impact: High
- Contract impact: build and publication gates only
- Amends: ADR 0037 and ADR 0038

## Context

The release bundle used the complete 19-image inventory, but ordinary
`docker-build` and `image-scan` duplicated only 18 entries and omitted `migrate`.
The strict license command inspected loose SBOM filenames without first proving
that the directory was the exact checksummed release bundle for the current
commit. It also allowed one of `licenseDeclared` or `licenseConcluded` to be
missing or unresolved when the other field was allowlisted.

## Decision

1. `deploy/release/images.json` is the single image source for local build,
   local scan, release build, release verification, and license policy mapping.
   `releasectl local-build` and `local-scan` iterate all 19 images in inventory
   order. Tempo alone receives its reviewed VEX. Any production Dockerfile that
   is neither included nor explicitly excluded fails inventory loading.
2. The license publication command first invokes `releasectl verify` against the
   supplied directory. Exact manifest, image order, Dockerfiles, immutable image
   subjects, SPDX 2.3 structure, Trivy artifacts, checksums, and artifact set
   must pass before license approval is considered.
3. The verified manifest `source_commit` must equal current repository `HEAD`.
   A valid bundle for another commit cannot be published from the current
   approval context.
4. Every SPDX document must contain a non-empty package list. Every package must
   independently provide allowlisted exact `licenseDeclared` and
   `licenseConcluded` values. Empty values, `NOASSERTION`, `NONE`, `LicenseRef-*`,
   and unknown or unapproved expressions fail closed.
5. The technical review check and publication authorization remain separate.
   The review check is green only when the inventory and truthful policy agree.
   Publication additionally requires overall `GO`, every image `approved`, the
   exact current-commit bundle, and all package assertions accepted.
6. The current policy remains `NO-GO`. This ADR strengthens enforcement and does
   not choose a repository license, interpret AGPL/MPL/Redis obligations, or
   authorize image publication.

## Consequences

- `migrate` and every future classified production image receive the same build,
  vulnerability, SBOM, and license gates without editing Make targets.
- A copied, partial, stale, malformed, or checksum-divergent artifact directory
  cannot satisfy the publication command.
- Legal approval remains an explicit external owner decision; engineering cannot
  turn missing legal evidence into a technical default.

## Rejected Alternatives

- Keep three image lists synchronized manually: rejected because the omission
  already demonstrated silent drift.
- Scan any `*.spdx.json` found in a directory: rejected because filenames do not
  bind evidence to image bytes, inventory, or commit.
- Accept a package when either SPDX license field is known: rejected because the
  unresolved field can represent a different scanner or legal conclusion.
