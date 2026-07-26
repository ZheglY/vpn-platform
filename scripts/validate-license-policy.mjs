import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const policyPath = path.join(repositoryRoot, "deploy", "release", "license-policy.json");
const inventoryPath = path.join(repositoryRoot, "deploy", "release", "images.json");
const enforcePublication = process.argv.includes("--publication");
const outputArgument = process.argv.find((value) => value.startsWith("--output="));

function fail(message) {
  process.stderr.write(`license policy: ${message}\n`);
  process.exitCode = 1;
}

function readJSON(file) {
  return JSON.parse(fs.readFileSync(file, "utf8"));
}

const policy = readJSON(policyPath);
const inventory = readJSON(inventoryPath);
const validStatuses = new Set(["approved", "blocked", "requires_counsel"]);

if (policy.format_version !== 1) {
  fail("unsupported format_version");
}
if (!["GO", "NO-GO"].includes(policy.publication_decision)) {
  fail("publication_decision must be GO or NO-GO");
}
if (!Array.isArray(policy.allowed_exact_spdx_expressions) ||
    policy.allowed_exact_spdx_expressions.length === 0) {
  fail("allowed_exact_spdx_expressions must be a non-empty array");
}

const policyByImage = new Map();
for (const item of policy.images ?? []) {
  if (!item.name || policyByImage.has(item.name)) {
    fail(`duplicate or empty image policy name: ${item.name ?? ""}`);
    continue;
  }
  if (!validStatuses.has(item.status) || !item.reason) {
    fail(`image ${item.name} has an invalid status or empty reason`);
  }
  policyByImage.set(item.name, item);
}

const inventoryNames = new Set(inventory.images.map((item) => item.name));
for (const name of inventoryNames) {
  if (!policyByImage.has(name)) {
    fail(`release image ${name} has no license policy`);
  }
}
for (const name of policyByImage.keys()) {
  if (!inventoryNames.has(name)) {
    fail(`license policy references non-release image ${name}`);
  }
}

const blockers = [...policyByImage.values()].filter((item) => item.status !== "approved");
if (blockers.length > 0 && policy.publication_decision !== "NO-GO") {
  fail("publication_decision must be NO-GO while an image is not approved");
}

if (enforcePublication) {
  if (policy.publication_decision !== "GO" || blockers.length > 0) {
    fail(`publication blocked by ${blockers.length} unapproved image review(s)`);
  }
  if (!outputArgument) {
    fail("--output=<release bundle directory> is required for publication");
  } else {
    const output = path.resolve(outputArgument.slice("--output=".length));
    const allowlist = new Set(policy.allowed_exact_spdx_expressions);
    for (const item of inventory.images) {
      const sbomPath = path.join(output, `${item.name}.spdx.json`);
      if (!fs.existsSync(sbomPath)) {
        fail(`missing SBOM for ${item.name}`);
        continue;
      }
      const sbom = readJSON(sbomPath);
      for (const pkg of sbom.packages ?? []) {
        const expressions = [pkg.licenseDeclared, pkg.licenseConcluded]
          .filter((value) => value && value !== "NOASSERTION" && value !== "NONE");
        if (expressions.length === 0) {
          fail(`${item.name} package ${pkg.name ?? "<unnamed>"} has no asserted license`);
          continue;
        }
        for (const expression of expressions) {
          if (!allowlist.has(expression)) {
            fail(`${item.name} package ${pkg.name ?? "<unnamed>"} has unapproved exact license expression ${expression}`);
          }
        }
      }
    }
  }
}

if (!process.exitCode) {
  process.stdout.write(
    `license policy valid: ${policyByImage.size} release images, ` +
    `${blockers.length} publication blocker(s), decision ${policy.publication_decision}\n`,
  );
}
