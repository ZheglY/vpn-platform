import fs from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath, pathToFileURL } from "node:url";

export const requiredDocuments = [
  "system-inventory.md",
  "architecture-code-review.md",
  "security-privacy-review.md",
  "dependency-license-review.md",
  "legal-payment-provider-checklist.md",
  "production-topology-readiness.md",
  "game-day-report.md",
  "go-no-go-checklist.md",
  "go-no-go.json",
  "evidence-index.md",
];
export const requiredRunbooks = [
  "incident-response.md",
  "game-day.md",
  "production-rollback.md",
];
export const requiredGateIDs = [
  "BASE-01", "ARCH-01", "ARCH-02", "ARCH-03", "SEC-01", "LIC-01", "LIC-02",
  "LEGAL-01", "PAY-01", "PAY-02", "VPS-01", "PKI-01", "SECRET-01", "DB-01",
  "KAFKA-01", "BACKUP-01", "RESTORE-01", "GAME-01", "ALERT-01", "EDGE-01",
  "REGISTRY-01", "PROVENANCE-01", "CAPACITY-01", "OPERATIONS-01", "ROLLBACK-01", "CI-01",
];

const defaultRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const validStatuses = new Set(["passed", "blocked", "pending"]);
const validSeverities = new Set(["P0", "P1", "P2"]);
const validEvidenceTypes = new Set(["immutable", "expiring"]);
const datePattern = /^\d{4}-\d{2}-\d{2}$/;
const commitPattern = /\b[0-9a-f]{40}\b/g;
const repositoryPathPattern = /\b(?:(?:docs|deploy)\/[A-Za-z0-9._/-]+\.(?:md|json)|PLANS\.md)\b/g;

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

function validDate(value) {
  return typeof value === "string" && datePattern.test(value) &&
    new Date(`${value}T00:00:00Z`).toISOString().slice(0, 10) === value;
}

function defaultCommitExists(root, commit) {
  const result = spawnSync("git", ["cat-file", "-e", `${commit}^{commit}`], { cwd: root });
  return !result.error && result.status === 0;
}

function parseMarkdown(markdown) {
  const gates = new Map();
  for (const line of markdown.split(/\r?\n/)) {
    const columns = line.split("|").slice(1, -1).map((value) => value.trim());
    if (columns.length !== 10 || !/^[A-Z]+-\d+$/.test(columns[0])) {
      continue;
    }
    const id = columns[0];
    requireValue(!gates.has(id), `duplicate Markdown gate ${id}`);
    requireValue(["yes", "no"].includes(columns[9]), `Markdown gate ${id} has invalid blocking value`);
    gates.set(id, {
      id,
      requirement: columns[1],
      owner: columns[2],
      evidence: columns[3],
      status: columns[4],
      severity: columns[5],
      evidence_type: columns[6],
      review_date: columns[7],
      valid_until: columns[8],
      blocking: columns[9] === "yes",
    });
  }
  return gates;
}

export function validateProductionReadiness({
  root = defaultRoot,
  now = new Date(),
  commitExists = defaultCommitExists,
} = {}) {
  const reviewRoot = path.join(root, "docs", "reviews", "stage9");
  for (const file of requiredDocuments) {
    requireValue(fs.existsSync(path.join(reviewRoot, file)), `missing Stage 9 review document ${file}`);
  }
  for (const file of requiredRunbooks) {
    requireValue(fs.existsSync(path.join(root, "docs", "runbooks", file)), `missing Stage 9 runbook ${file}`);
  }

  const today = now.toISOString().slice(0, 10);
  const decision = readJSON(path.join(reviewRoot, "go-no-go.json"));
  requireValue(decision.format_version === 2 && ["GO", "NO-GO"].includes(decision.decision),
    "invalid machine-readable decision");
  requireValue(typeof decision.decision_owner === "string" && decision.decision_owner !== "", "decision_owner is required");
  requireValue(validDate(decision.reviewed_at) && decision.reviewed_at <= today, "decision reviewed_at is invalid or in the future");
  requireValue(validDate(decision.valid_until) && decision.valid_until >= today, "decision evidence is stale");

  const expected = new Set(requiredGateIDs);
  const seen = new Set();
  for (const gate of decision.gates ?? []) {
    requireValue(typeof gate.id === "string" && gate.id !== "" && !seen.has(gate.id), `duplicate or empty gate ID ${gate.id ?? ""}`);
    requireValue(expected.has(gate.id), `unknown gate ${gate.id}`);
    seen.add(gate.id);
    requireValue(typeof gate.requirement === "string" && gate.requirement !== "" &&
      typeof gate.owner === "string" && gate.owner !== "" &&
      typeof gate.evidence === "string" && gate.evidence !== "" &&
      typeof gate.blocking === "boolean" && validStatuses.has(gate.status) &&
      validSeverities.has(gate.severity) && validEvidenceTypes.has(gate.evidence_type),
    `gate ${gate.id} is incomplete`);
    requireValue(validDate(gate.review_date) && gate.review_date <= today, `gate ${gate.id} review_date is invalid or in the future`);
    if (gate.evidence_type === "immutable") {
      requireValue(gate.valid_until === undefined, `immutable gate ${gate.id} must not have valid_until`);
    } else {
      requireValue(validDate(gate.valid_until) && gate.valid_until >= today, `gate ${gate.id} evidence is stale`);
      requireValue(gate.valid_until >= decision.valid_until,
        `decision validity outlives gate ${gate.id} evidence`);
    }
    for (const evidencePath of gate.evidence.match(repositoryPathPattern) ?? []) {
      requireValue(fs.existsSync(path.join(root, filepathFromRepository(evidencePath))),
        `gate ${gate.id} references missing file ${evidencePath}`);
    }
    for (const commit of gate.evidence.match(commitPattern) ?? []) {
      requireValue(commitExists(root, commit), `gate ${gate.id} references missing commit ${commit}`);
    }
  }
  for (const id of requiredGateIDs) {
    requireValue(seen.has(id), `missing required gate ${id}`);
  }
  requireValue(seen.size === requiredGateIDs.length, "gate inventory is not exact");

  const openBlocking = decision.gates.filter((gate) => gate.blocking && gate.status !== "passed");
  if (openBlocking.length > 0) {
    requireValue(decision.decision === "NO-GO", "decision must be NO-GO while a blocking gate is not passed");
  }
  if (decision.decision === "GO") {
    requireValue(decision.gates.every((gate) => gate.status === "passed"), "GO requires every gate to pass");
  }

  const markdown = fs.readFileSync(path.join(reviewRoot, "go-no-go-checklist.md"), "utf8");
  requireValue(markdown.includes(`Review date: ${decision.reviewed_at}`), "Markdown and JSON review dates disagree");
  requireValue(markdown.includes(`Valid until: ${decision.valid_until}`), "Markdown and JSON validity dates disagree");
  requireValue(markdown.includes(`Decision owner: ${decision.decision_owner}`), "Markdown and JSON decision owners disagree");
  requireValue(markdown.includes(`Decision: **${decision.decision}**`), "Markdown and machine-readable decisions disagree");
  const markdownGates = parseMarkdown(markdown);
  requireValue(markdownGates.size === requiredGateIDs.length, "Markdown gate inventory is not exact");
  for (const gate of decision.gates) {
    const markdownGate = markdownGates.get(gate.id);
    requireValue(markdownGate !== undefined, `Markdown checklist is missing gate ${gate.id}`);
    const expectedMarkdown = {
      ...gate,
      valid_until: gate.evidence_type === "immutable" ? "-" : gate.valid_until,
    };
    for (const field of ["requirement", "owner", "evidence", "status", "severity", "evidence_type", "review_date", "valid_until", "blocking"]) {
      requireValue(markdownGate[field] === expectedMarkdown[field], `Markdown checklist disagrees with gate ${gate.id} field ${field}`);
    }
  }

  const licensePolicy = readJSON(path.join(root, "deploy", "release", "license-policy.json"));
  if (licensePolicy.publication_decision !== "GO") {
    requireValue(decision.decision === "NO-GO", "platform decision cannot advance while license publication is not GO");
  }
  return { decision: decision.decision, openBlocking: openBlocking.length, gateCount: seen.size };
}

function filepathFromRepository(value) {
  return value.split("/").join(path.sep);
}

function main() {
  try {
    const result = validateProductionReadiness();
    process.stdout.write(
      `production readiness review valid: decision ${result.decision}, ` +
      `${result.openBlocking} open blocking gate(s), ${result.gateCount} total gate(s)\n`,
    );
  } catch (error) {
    process.stderr.write(`production readiness: ${error.message}\n`);
    process.exitCode = 1;
  }
}

if (process.argv[1] && pathToFileURL(path.resolve(process.argv[1])).href === import.meta.url) {
  main();
}
