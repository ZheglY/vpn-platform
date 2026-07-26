import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const reviewRoot = path.join(root, "docs", "reviews", "stage9");
const requiredDocuments = [
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
const requiredRunbooks = [
  "incident-response.md",
  "game-day.md",
  "production-rollback.md",
];
const requiredGateIDs = new Set([
  "BASE-01", "ARCH-01", "ARCH-02", "SEC-01", "LIC-01", "LIC-02",
  "LEGAL-01", "PAY-01", "PAY-02", "VPS-01", "PKI-01", "SECRET-01",
  "DB-01", "KAFKA-01", "BACKUP-01", "RESTORE-01", "GAME-01",
  "ALERT-01", "EDGE-01", "REGISTRY-01", "PROVENANCE-01", "CAPACITY-01",
  "OPERATIONS-01", "ROLLBACK-01", "CI-01",
]);
let failed = false;

function fail(message) {
  process.stderr.write(`production readiness: ${message}\n`);
  failed = true;
}

for (const file of requiredDocuments) {
  if (!fs.existsSync(path.join(reviewRoot, file))) {
    fail(`missing Stage 9 review document ${file}`);
  }
}
for (const file of requiredRunbooks) {
  if (!fs.existsSync(path.join(root, "docs", "runbooks", file))) {
    fail(`missing Stage 9 runbook ${file}`);
  }
}

const decision = JSON.parse(
  fs.readFileSync(path.join(reviewRoot, "go-no-go.json"), "utf8"),
);
if (decision.format_version !== 1 || !["GO", "CONDITIONAL GO", "NO-GO"].includes(decision.decision)) {
  fail("invalid machine-readable decision");
}
const seen = new Set();
const validStatuses = new Set(["passed", "blocked", "pending"]);
for (const gate of decision.gates ?? []) {
  if (!gate.id || seen.has(gate.id)) {
    fail(`duplicate or empty gate ID ${gate.id ?? ""}`);
    continue;
  }
  seen.add(gate.id);
  if (!gate.requirement || !gate.owner || !gate.evidence || !gate.severity ||
      !gate.review_date || typeof gate.blocking !== "boolean" ||
      !validStatuses.has(gate.status)) {
    fail(`gate ${gate.id} is incomplete`);
  }
}
for (const id of requiredGateIDs) {
  if (!seen.has(id)) {
    fail(`missing required gate ${id}`);
  }
}
const openBlocking = (decision.gates ?? []).filter(
  (gate) => gate.blocking && gate.status !== "passed",
);
if (openBlocking.length > 0 && decision.decision !== "NO-GO") {
  fail("decision must be NO-GO while a blocking gate is not passed");
}
if (decision.decision === "GO" && (decision.gates ?? []).some((gate) => gate.status !== "passed")) {
  fail("GO requires every gate to pass");
}

const markdownDecision = fs.readFileSync(
  path.join(reviewRoot, "go-no-go-checklist.md"), "utf8",
);
if (!markdownDecision.includes(`Decision: **${decision.decision}**`)) {
  fail("Markdown and machine-readable decisions disagree");
}
const markdownGates = new Map();
for (const line of markdownDecision.split(/\r?\n/)) {
  const columns = line.split("|").slice(1, -1).map((value) => value.trim());
  if (columns.length !== 8 || !/^[A-Z]+-\d+$/.test(columns[0])) {
    continue;
  }
  const id = columns[0];
  if (markdownGates.has(id)) {
    fail(`duplicate Markdown gate ${id}`);
    continue;
  }
  if (!["yes", "no"].includes(columns[7])) {
    fail(`Markdown gate ${id} has invalid blocking value`);
  }
  markdownGates.set(id, {
    status: columns[4],
    severity: columns[5],
    blocking: columns[7] === "yes",
  });
}
for (const gate of decision.gates ?? []) {
  const markdownGate = markdownGates.get(gate.id);
  if (!markdownGate) {
    fail(`Markdown checklist is missing gate ${gate.id}`);
    continue;
  }
  if (markdownGate.status !== gate.status ||
      markdownGate.severity !== gate.severity ||
      markdownGate.blocking !== gate.blocking) {
    fail(`Markdown checklist disagrees with gate ${gate.id}`);
  }
}
for (const id of markdownGates.keys()) {
  if (!seen.has(id)) {
    fail(`Markdown checklist has unknown gate ${id}`);
  }
}
const licensePolicy = JSON.parse(
  fs.readFileSync(path.join(root, "deploy", "release", "license-policy.json"), "utf8"),
);
if (licensePolicy.publication_decision !== "GO" && decision.decision !== "NO-GO") {
  fail("platform decision cannot advance while license publication is not GO");
}

if (!failed) {
  process.stdout.write(
    `production readiness review valid: decision ${decision.decision}, ` +
    `${openBlocking.length} open blocking gate(s)\n`,
  );
} else {
  process.exitCode = 1;
}
