import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath, pathToFileURL } from "node:url";

const defaultRepositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const validStatuses = new Set(["approved", "blocked", "requires_counsel"]);
const commitPattern = /^[0-9a-f]{40}$/;

function readJSON(file) {
  try {
    return JSON.parse(fs.readFileSync(file, "utf8"));
  } catch (error) {
    throw new Error(`cannot read valid JSON ${path.basename(file)}: ${error.message}`);
  }
}

function requireValue(condition, message) {
  if (!condition) {
    throw new Error(message);
  }
}

function defaultVerifyBundle(repositoryRoot, inventoryPath, output) {
  const result = spawnSync(
    "go",
    ["run", "-mod=readonly", "./tools/releasectl", "verify", "--inventory", inventoryPath, "--output", output],
    { cwd: repositoryRoot, stdio: "inherit" },
  );
  if (result.error) {
    throw new Error(`release bundle verifier failed to start: ${result.error.message}`);
  }
  if (result.status !== 0) {
    throw new Error("release bundle verification failed");
  }
}

function defaultHeadCommit(repositoryRoot) {
  const result = spawnSync("git", ["rev-parse", "HEAD"], {
    cwd: repositoryRoot,
    encoding: "utf8",
  });
  if (result.error || result.status !== 0) {
    throw new Error("cannot resolve current Git commit");
  }
  return result.stdout.trim();
}

function validatePackageLicenses(imageName, sbom, allowlist) {
  requireValue(sbom.spdxVersion === "SPDX-2.3", `${imageName} SBOM must be SPDX-2.3`);
  requireValue(Array.isArray(sbom.packages) && sbom.packages.length > 0, `${imageName} SBOM packages must be non-empty`);
  for (const pkg of sbom.packages) {
    const packageName = typeof pkg.name === "string" && pkg.name !== "" ? pkg.name : "<unnamed>";
    for (const field of ["licenseDeclared", "licenseConcluded"]) {
      const expression = pkg[field];
      requireValue(typeof expression === "string" && expression !== "", `${imageName} package ${packageName} has no ${field}`);
      requireValue(expression !== "NOASSERTION" && expression !== "NONE", `${imageName} package ${packageName} has unresolved ${field} ${expression}`);
      requireValue(!expression.startsWith("LicenseRef-"), `${imageName} package ${packageName} has unresolved ${field} ${expression}`);
      requireValue(allowlist.has(expression), `${imageName} package ${packageName} has unapproved ${field} ${expression}`);
    }
  }
}

export function validateLicensePolicy({
  repositoryRoot = defaultRepositoryRoot,
  publication = false,
  output,
  verifyBundle = defaultVerifyBundle,
  headCommit,
} = {}) {
  const policyPath = path.join(repositoryRoot, "deploy", "release", "license-policy.json");
  const inventoryPath = path.join(repositoryRoot, "deploy", "release", "images.json");

  if (publication) {
    requireValue(typeof output === "string" && output !== "", "--output=<release bundle directory> is required for publication");
    output = path.resolve(output);
    verifyBundle(repositoryRoot, inventoryPath, output);
  }

  const policy = readJSON(policyPath);
  const inventory = readJSON(inventoryPath);
  requireValue(policy.format_version === 1, "unsupported policy format_version");
  requireValue(["GO", "NO-GO"].includes(policy.publication_decision), "publication_decision must be GO or NO-GO");
  requireValue(/^\d{4}-\d{2}-\d{2}$/.test(policy.reviewed_at ?? ""), "reviewed_at must be a UTC date");
  requireValue(Array.isArray(policy.allowed_exact_spdx_expressions) && policy.allowed_exact_spdx_expressions.length > 0,
    "allowed_exact_spdx_expressions must be a non-empty array");
  requireValue(Array.isArray(inventory.images) && inventory.images.length > 0, "release inventory images must be non-empty");

  const policyByImage = new Map();
  for (const item of policy.images ?? []) {
    requireValue(typeof item.name === "string" && item.name !== "" && !policyByImage.has(item.name),
      `duplicate or empty image policy name: ${item.name ?? ""}`);
    requireValue(validStatuses.has(item.status) && typeof item.reason === "string" && item.reason !== "",
      `image ${item.name} has an invalid status or empty reason`);
    policyByImage.set(item.name, item);
  }

  const inventoryNames = new Set(inventory.images.map((item) => item.name));
  for (const name of inventoryNames) {
    requireValue(policyByImage.has(name), `release image ${name} has no license policy`);
  }
  for (const name of policyByImage.keys()) {
    requireValue(inventoryNames.has(name), `license policy references non-release image ${name}`);
  }

  const blockers = [...policyByImage.values()].filter((item) => item.status !== "approved");
  if (blockers.length > 0) {
    requireValue(policy.publication_decision === "NO-GO", "publication_decision must be NO-GO while an image is not approved");
  }

  if (publication) {
    requireValue(policy.publication_decision === "GO" && blockers.length === 0,
      `publication blocked by ${blockers.length} unapproved image review(s)`);
    const manifest = readJSON(path.join(output, "release-manifest.json"));
    const currentCommit = headCommit ?? defaultHeadCommit(repositoryRoot);
    requireValue(commitPattern.test(currentCommit), "current Git commit is invalid");
    requireValue(manifest.source_commit === currentCommit, "release manifest source_commit does not match current HEAD");
    requireValue(Array.isArray(manifest.images) && manifest.images.length === inventory.images.length,
      "release manifest image set is incomplete");
    const manifestByImage = new Map(manifest.images.map((item) => [item.name, item]));
    const allowlist = new Set(policy.allowed_exact_spdx_expressions);
    for (const item of inventory.images) {
      const releaseImage = manifestByImage.get(item.name);
      requireValue(releaseImage && typeof releaseImage.sbom === "string", `release manifest is missing ${item.name}`);
      const sbomPath = path.resolve(output, releaseImage.sbom);
      requireValue(path.dirname(sbomPath) === output, `${item.name} SBOM path escapes release output`);
      validatePackageLicenses(item.name, readJSON(sbomPath), allowlist);
    }
  }

  return { imageCount: policyByImage.size, blockerCount: blockers.length, decision: policy.publication_decision };
}

function main() {
  const publication = process.argv.includes("--publication");
  const outputArgument = process.argv.find((value) => value.startsWith("--output="));
  try {
    const result = validateLicensePolicy({
      publication,
      output: outputArgument?.slice("--output=".length),
    });
    process.stdout.write(
      `license policy valid: ${result.imageCount} release images, ` +
      `${result.blockerCount} publication blocker(s), decision ${result.decision}\n`,
    );
  } catch (error) {
    process.stderr.write(`license policy: ${error.message}\n`);
    process.exitCode = 1;
  }
}

if (process.argv[1] && pathToFileURL(path.resolve(process.argv[1])).href === import.meta.url) {
  main();
}
