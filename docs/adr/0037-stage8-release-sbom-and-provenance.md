# ADR 0037: Stage 8 release SBOM and provenance

- Status: Accepted for Stage 8 implementation
- Date: 2026-07-26
- Owners: platform, security, and release operations
- Security impact: High
- Contract impact: release metadata and CI only
- Extends: ADR 0013, ADR 0027, and ADR 0029

## Context

Passing source tests does not identify which image bytes were reviewed, what packages they contain, or who produced a release artifact. Floating scanner actions are especially unsafe after upstream tag compromise incidents. Production registry and deployment credentials have not been approved.

## Decision

1. `deploy/release/images.json` is the complete reviewed inventory of custom production runtime/tool images. It maps every included Dockerfile, explicitly classifies the two local fake-provider Dockerfiles as excluded, and pins Syft and Trivy by version plus manifest digest. Inventory validation fails when a Dockerfile below `services`, `tools`, or `deploy/observability` is neither included nor explicitly excluded.
2. `make release-bundle` refuses a dirty worktree. It builds every image from one full commit, applies OCI source/revision/version/created labels, tags it with the 12-character commit prefix, records the full Docker content-addressed image ID, and emits one SPDX 2.3 SBOM per image. Syft and Trivy resolve that immutable image ID rather than the mutable local tag.
3. Every image is scanned for HIGH and CRITICAL vulnerabilities with the digest-pinned Trivy image. The existing narrowly reviewed Tempo OpenVEX document is applied only to Tempo and remains visible with `--show-suppressed`. Any other finding fails the release. The JSON scan report is retained and checksum-bound beside the image SBOM.
4. `releasectl` verifies inventory completeness and paths, tool digests, the SPDX container subject against the immutable image ID, SBOM and scan-report structure, manifest/image binding, the exact artifact set, and SHA-256 checksums. Modification of a manifest, substitution of a report from another image, or addition of an unchecksummed file fails verification.
5. `.github/workflows/release-attest.yml` is manual and bound to the `release-signing` environment. It rebuilds and scans from the checked-out commit, creates an archive with canonical ordering, timestamp, owner, and gzip metadata, and uses the commit-pinned `actions/attest` action with GitHub OIDC to create repository-bound keyless provenance. `gh attestation verify` must succeed before the bundle is uploaded. Third-party SBOM/report content can include tool-generated metadata, so byte-for-byte reproducibility across separate runs is not claimed.
6. The workflow has only `contents:read`, `id-token:write`, `attestations:write`, and the current action-required `artifact-metadata:write`. It has no package, cloud, VPS, payment, or deployment permission. Repository administrators must configure required reviewers for the `release-signing` environment before the first approved run.
7. This slice does not publish images to a registry. A future approved publication workflow must push immutable OCI digests, attach the matching SBOM and signature to each digest, verify signer identity and issuer, and deploy by digest only.
8. Local rollback selects the previous reviewed commit and image ID. Production rollback will select a previous registry digest whose source, SBOM, scan, and attestation all verify; rollback never rebuilds an old mutable tag.

## Consequences

- A release candidate has a complete, tamper-evident package inventory and repository-bound build provenance without storing a signing key.
- Keyless CI does not grant deployment authority, and Stage 9 remains the approval gate for registry, production environment, and rollback execution.
- Docker image IDs are local content identities. Registry manifest digests and per-image OCI signatures remain intentionally blocked until publication is approved.

## Rejected alternatives

- Use floating `@vN` scanner or signing actions: rejected because tag movement can replace executable supply-chain code.
- Sign an unscanned mutable tag: rejected because the subject can change independently of review.
- Add a long-lived Cosign private key to repository secrets: rejected because GitHub OIDC can provide short-lived repository/workflow identity without key custody.
