import { readFile } from "node:fs/promises";
import { Parser, DiagnosticSeverity } from "@asyncapi/parser";

const document = await readFile("contracts/events/asyncapi.yaml", "utf8");
const parser = new Parser();
const result = await parser.parse(document);
const diagnostics = result.diagnostics ?? [];
const errors = diagnostics.filter(
  (diagnostic) =>
    diagnostic.severity === DiagnosticSeverity.Error ||
    diagnostic.severity === 0,
);

for (const diagnostic of diagnostics) {
  const severity = diagnostic.severity ?? "unknown";
  console.log(`${severity}: ${diagnostic.message}`);
}

if (errors.length > 0) {
  console.error(`AsyncAPI validation failed with ${errors.length} error(s).`);
  process.exit(1);
}

console.log(`AsyncAPI validation passed with ${diagnostics.length} diagnostic(s).`);
