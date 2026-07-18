import { readFile } from "node:fs/promises";
import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";

const contracts = [
  "billing.payment.succeeded.v1",
  "billing.payment.canceled.v1",
  "billing.refund.succeeded.v1",
  "subscription.activated.v1",
  "subscription.extended.v1",
  "subscription.expired.v1",
  "subscription.revoked.v1",
];

const ajv = new Ajv2020({ allErrors: true, strict: true });
addFormats(ajv);

for (const name of contracts) {
  const schema = JSON.parse(
    await readFile(`contracts/events/schemas/${name}.schema.json`, "utf8"),
  );
  const example = JSON.parse(
    await readFile(`contracts/events/examples/${name}.json`, "utf8"),
  );
  const validate = ajv.compile(schema);
  if (!validate(example)) {
    console.error(`${name} contract validation failed`, validate.errors);
    process.exit(1);
  }
}

console.log(`Validated ${contracts.length} event schema example(s).`);
