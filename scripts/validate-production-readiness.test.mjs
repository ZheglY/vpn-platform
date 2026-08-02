import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
  requiredDocuments,
  requiredGateIDs,
  requiredRunbooks,
  validateProductionReadiness,
} from "./validate-production-readiness.mjs";

const now = new Date("2026-08-02T12:00:00Z");

function writeJSON(file, value) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, `${JSON.stringify(value, null, 2)}\n`);
}

function renderMarkdown(decision) {
  const lines = [
    "# Test decision",
    "",
    `Review date: ${decision.reviewed_at}`,
    `Valid until: ${decision.valid_until}`,
    `Decision owner: ${decision.decision_owner}`,
    `Decision: **${decision.decision}**`,
    "",
    "| Gate ID | Requirement | Owner | Evidence | Status | Severity | Evidence type | Review date | Valid until | Blocking |",
    "|---|---|---|---|---|---|---|---|---|---|",
  ];
  for (const gate of decision.gates) {
    lines.push(`| ${gate.id} | ${gate.requirement} | ${gate.owner} | ${gate.evidence} | ${gate.status} | ${gate.severity} | ${gate.evidence_type} | ${gate.review_date} | ${gate.evidence_type === "immutable" ? "-" : gate.valid_until} | ${gate.blocking ? "yes" : "no"} |`);
  }
  return `${lines.join("\n")}\n`;
}

function fixture() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "vpn-readiness-"));
  const reviewRoot = path.join(root, "docs/reviews/stage9");
  for (const file of requiredDocuments) {
    fs.mkdirSync(path.dirname(path.join(reviewRoot, file)), { recursive: true });
    fs.writeFileSync(path.join(reviewRoot, file), "fixture\n");
  }
  for (const file of requiredRunbooks) {
    const target = path.join(root, "docs/runbooks", file);
    fs.mkdirSync(path.dirname(target), { recursive: true });
    fs.writeFileSync(target, "fixture\n");
  }
  const decision = {
    format_version: 2,
    reviewed_at: "2026-08-02",
    valid_until: "2026-08-30",
    decision: "GO",
    decision_owner: "Test owner",
    gates: requiredGateIDs.map((id) => ({
      id,
      requirement: `requirement ${id}`,
      owner: "Test owner",
      evidence: `evidence ${id}`,
      status: "passed",
      severity: "P1",
      evidence_type: "expiring",
      review_date: "2026-08-02",
      valid_until: "2026-08-30",
      blocking: true,
    })),
  };
  writeJSON(path.join(reviewRoot, "go-no-go.json"), decision);
  fs.writeFileSync(path.join(reviewRoot, "go-no-go-checklist.md"), renderMarkdown(decision));
  writeJSON(path.join(root, "deploy/release/license-policy.json"), { publication_decision: "GO" });
  return { root, decision, reviewRoot };
}

function save(item) {
  writeJSON(path.join(item.reviewRoot, "go-no-go.json"), item.decision);
  fs.writeFileSync(path.join(item.reviewRoot, "go-no-go-checklist.md"), renderMarkdown(item.decision));
}

function validate(item, commitExists = () => true) {
  return validateProductionReadiness({ root: item.root, now, commitExists });
}

test("complete fresh GO decision passes", () => {
  const item = fixture();
  assert.deepEqual(validate(item), { decision: "GO", openBlocking: 0, gateCount: requiredGateIDs.length });
});

test("missing ARCH-03 fails", () => {
  const item = fixture();
  item.decision.gates = item.decision.gates.filter((gate) => gate.id !== "ARCH-03");
  save(item);
  assert.throws(() => validate(item), /missing required gate ARCH-03/);
});

test("stale expiring evidence fails", () => {
  const item = fixture();
  item.decision.gates[0].valid_until = "2026-08-01";
  save(item);
  assert.throws(() => validate(item), /evidence is stale/);
});

test("decision validity cannot outlive a gate", () => {
  const item = fixture();
  item.decision.gates[0].valid_until = "2026-08-10";
  item.decision.valid_until = "2026-08-11";
  save(item);
  assert.throws(() => validate(item), /validity outlives gate/);
});

test("immutable evidence rejects a magic expiry", () => {
  const item = fixture();
  item.decision.gates[0].evidence_type = "immutable";
  save(item);
  assert.throws(() => validate(item), /must not have valid_until/);
});

test("conditional GO is not an allowed bypass", () => {
  const item = fixture();
  item.decision.decision = "CONDITIONAL GO";
  save(item);
  assert.throws(() => validate(item), /invalid machine-readable decision/);
});

test("GO rejects pending or blocked gates", () => {
  for (const status of ["pending", "blocked"]) {
    const item = fixture();
    item.decision.gates[0].status = status;
    save(item);
    assert.throws(() => validate(item), /decision must be NO-GO/);
  }
});

test("Markdown field drift fails", () => {
  const item = fixture();
  const file = path.join(item.reviewRoot, "go-no-go-checklist.md");
  fs.writeFileSync(file, fs.readFileSync(file, "utf8").replace("| Test owner | evidence BASE-01", "| Another owner | evidence BASE-01"));
  assert.throws(() => validate(item), /field owner/);
});

test("missing referenced files and commits fail", () => {
  const missingFile = fixture();
  missingFile.decision.gates[0].evidence = "docs/missing.md";
  save(missingFile);
  assert.throws(() => validate(missingFile), /references missing file/);

  const missingCommit = fixture();
  missingCommit.decision.gates[0].evidence = `commit ${"a".repeat(40)}`;
  save(missingCommit);
  assert.throws(() => validate(missingCommit, () => false), /references missing commit/);
});

test("truthful fresh NO-GO with a blocked gate passes", () => {
  const item = fixture();
  item.decision.decision = "NO-GO";
  item.decision.gates[0].status = "blocked";
  writeJSON(path.join(item.root, "deploy/release/license-policy.json"), { publication_decision: "NO-GO" });
  save(item);
  assert.equal(validate(item).openBlocking, 1);
});
