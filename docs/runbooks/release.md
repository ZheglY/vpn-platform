# Release, SBOM, and Provenance

This runbook creates a Stage 8 release candidate. It does not publish or deploy production images.

## Local Candidate

Ordinary local image gates use the same inventory as the release candidate:

```powershell
make docker-build
make image-scan
```

Both commands cover all 19 classified images, including `migrate`; adding an
unclassified production Dockerfile fails before a build or scan starts.

The worktree must be clean:

```powershell
make release-bundle
```

The command:

1. reads the fixed inventory in `deploy/release/images.json`;
2. builds every image from the exact Git commit with OCI source/revision/version/date labels;
3. records the full content-addressed Docker image ID;
4. generates one SPDX 2.3 SBOM per image using digest-pinned Syft;
5. runs digest-pinned Trivy with a zero HIGH/CRITICAL gate and retains its JSON report;
6. verifies inventory completeness, each SPDX container subject against the immutable image ID, report structure, the exact artifact set, and all SHA-256 bindings.

Output is ignored under `tmp/release-<commit>/`. It contains metadata and SBOMs, not credentials or production configuration. Treat a failed scan, missing package inventory, changed checksum, or dirty tree as a release failure.

## License Publication Gate

After legal/product approval changes the reviewed policy to `GO`, publication
still requires the exact bundle for current `HEAD`:

```powershell
make license-publication-gate RELEASE_OUTPUT_DIR=<bundle>
```

The command first runs the complete `releasectl verify` bundle check. It then
requires manifest `source_commit` to equal current `HEAD`, all 19 image policies
to be approved, non-empty SPDX 2.3 packages, and independently allowlisted exact
`licenseDeclared` and `licenseConcluded` values for every package.
`NOASSERTION`, `NONE`, `LicenseRef-*`, unknown expressions, missing artifacts,
wrong subjects, checksum changes, and extra files fail closed. The command is
expected to remain red while `license-policy.json` truthfully says `NO-GO`.

## Keyless CI Attestation

1. Protect the GitHub `release-signing` environment with required reviewers.
2. Run the manual `release-attest` workflow for a reviewed immutable commit and version.
3. The workflow rebuilds and rescans, creates a canonically ordered release-metadata archive, signs repository-bound provenance through GitHub OIDC, and runs `gh attestation verify`. Tool-generated report metadata means separate runs are not asserted to be byte-for-byte reproducible.
4. Download the bundle and verify it again:

```bash
gh attestation verify vpn-platform-<version>-<commit>.tar.gz --repo ZheglY/vpn-platform
```

The workflow has no registry or deployment permission. The uploaded bundle contains the manifest, checksums, SPDX documents, and retained Trivy JSON reports; release images remain local build outputs.

## Future Registry Publication

Production publication requires a separate approved workflow that:

- authenticates to an approved private registry with short-lived identity;
- pushes each image once and records its OCI manifest digest;
- attaches matching SBOM and keyless signature/attestation to that digest;
- verifies expected workflow identity and OIDC issuer;
- scans the registry-resolved digest again;
- deploys by digest, never by mutable tag.

## Rollback

Select the previous accepted release manifest. Verify its source commit, SBOM checksum, vulnerability decision, attestation identity, and immutable image/registry digest. Rollback must reuse those bytes; rebuilding an old tag creates a new candidate and requires the full gate again.

Do not promote a release when GitHub attestation services, scanner databases, or provenance verification are unavailable. Record the block; do not bypass it with a long-lived signing key.
