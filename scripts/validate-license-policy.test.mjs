import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { validateLicensePolicy } from "./validate-license-policy.mjs";

const commit = "a".repeat(40);

function writeJSON(file, value) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, `${JSON.stringify(value)}\n`);
}

function fixture() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "vpn-license-"));
  const output = path.join(root, "bundle");
  fs.mkdirSync(output);
  writeJSON(path.join(root, "deploy/release/images.json"), {
    images: [{ name: "service", dockerfile: "service/Dockerfile" }],
  });
  writeJSON(path.join(root, "deploy/release/license-policy.json"), {
    format_version: 1,
    reviewed_at: "2026-08-02",
    review_owner: "test",
    publication_decision: "GO",
    allowed_exact_spdx_expressions: ["MIT"],
    images: [{ name: "service", status: "approved", reason: "test fixture" }],
  });
  writeJSON(path.join(output, "release-manifest.json"), {
    source_commit: commit,
    images: [{ name: "service", sbom: "service.spdx.json" }],
  });
  writeJSON(path.join(output, "service.spdx.json"), {
    spdxVersion: "SPDX-2.3",
    packages: [{ name: "service", licenseDeclared: "MIT", licenseConcluded: "MIT" }],
  });
  return { root, output };
}

function validate(item, verifyBundle = () => {}) {
  return validateLicensePolicy({
    repositoryRoot: item.root,
    publication: true,
    output: item.output,
    verifyBundle,
    headCommit: commit,
  });
}

test("publication validates the exact bundle before policy approval", () => {
  const item = fixture();
  let verified = false;
  assert.throws(
    () => validate(item, () => {
      verified = true;
      throw new Error("bundle rejected");
    }),
    /bundle rejected/,
  );
  assert.equal(verified, true);
});

test("publication accepts independently allowlisted declared and concluded licenses", () => {
  const item = fixture();
  assert.deepEqual(validate(item), { imageCount: 1, blockerCount: 0, decision: "GO" });
});

for (const [name, mutation, expected] of [
  ["malformed SPDX object", () => ({}), /SPDX-2.3/],
  ["empty packages", (sbom) => ({ ...sbom, packages: [] }), /packages must be non-empty/],
  ["missing declaration", (sbom) => ({ ...sbom, packages: [{ name: "service", licenseConcluded: "MIT" }] }), /no licenseDeclared/],
  ["NOASSERTION declaration", (sbom) => ({ ...sbom, packages: [{ name: "service", licenseDeclared: "NOASSERTION", licenseConcluded: "MIT" }] }), /unresolved licenseDeclared/],
  ["NONE conclusion", (sbom) => ({ ...sbom, packages: [{ name: "service", licenseDeclared: "MIT", licenseConcluded: "NONE" }] }), /unresolved licenseConcluded/],
  ["LicenseRef conclusion", (sbom) => ({ ...sbom, packages: [{ name: "service", licenseDeclared: "MIT", licenseConcluded: "LicenseRef-local" }] }), /unresolved licenseConcluded/],
  ["unknown conclusion", (sbom) => ({ ...sbom, packages: [{ name: "service", licenseDeclared: "MIT", licenseConcluded: "BSD-4-Clause" }] }), /unapproved licenseConcluded/],
]) {
  test(`publication rejects ${name}`, () => {
    const item = fixture();
    const sbomPath = path.join(item.output, "service.spdx.json");
    const sbom = JSON.parse(fs.readFileSync(sbomPath, "utf8"));
    writeJSON(sbomPath, mutation(sbom));
    assert.throws(() => validate(item), expected);
  });
}

test("publication rejects a bundle for another commit", () => {
  const item = fixture();
  assert.throws(
    () => validateLicensePolicy({
      repositoryRoot: item.root,
      publication: true,
      output: item.output,
      verifyBundle: () => {},
      headCommit: "b".repeat(40),
    }),
    /does not match current HEAD/,
  );
});
