// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-235
import test from "node:test";
import assert from "node:assert/strict";
import { chmodSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { renderComposeConfiguration, resolveConfiguration, validateDescriptor } from "../production-config.mjs";

function privateFile(filePath, contents) {
  writeFileSync(filePath, contents, { mode: 0o600 });
  chmodSync(filePath, 0o600);
  return filePath;
}

test("TEST-235 native Compose resolution preserves quoting, empties, precedence, and escaped dollars", (t) => {
  const directory = mkdtempSync(path.join(tmpdir(), "harden-llm-compose-conformance-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  for (const relative of ["deploy/frontend"]) {
    const nested = path.join(directory, relative);
    // The fixture is intentionally small; the production module still passes
    // the repository's fixed two-file order to the native Compose CLI.
    mkdirSync(nested, { recursive: true });
  }
  writeFileSync(path.join(directory, "docker-compose.yml"), `services:
  probe:
    image: alpine:3.20
    environment:
      HASH: \${HASH:?set HASH}
      JSON_VALUE: \${JSON_VALUE:?set JSON_VALUE}
      EMPTY_VALUE: \${EMPTY_VALUE-default}
      PRECEDENCE: \${PRECEDENCE:?set PRECEDENCE}
      DEFAULT_VALUE: \${UNSET_VALUE:-default-value}
`);
  writeFileSync(path.join(directory, "deploy/frontend/compose.frontend.yml"), "services: {}\n");

  const descriptor = validateDescriptor({
    schemaVersion: 1,
    project: "harden-llm",
    dockerContext: "default",
    composeRoot: directory,
    applicationRoot: directory,
    composeFiles: ["docker-compose.yml", "deploy/frontend/compose.frontend.yml"],
    productionEnvFile: privateFile(path.join(directory, "production.env"), "PRECEDENCE=production\n"),
    observabilityEnvFile: privateFile(path.join(directory, "observability.env"), [
      "HASH='$2a$12$literal-bcrypt-hash'",
      "JSON_VALUE='{\"primary\":\"$literal\",\"number\":7}'",
      "EMPTY_VALUE=''",
      "PRECEDENCE=observability",
    ].join("\n") + "\n"),
    sharedApplicationEnvFile: privateFile(path.join(directory, "shared.env"), [
      `HARDEN_LLM_CONFIG_FILE=${path.join(directory, "upstreams.json")}`,
      "HARDEN_LLM_TOKEN=synthetic-incoming-token",
      "CPA_API_KEY=synthetic-upstream-token",
      "HARDEN_LLM_CONTROL_PLANE_URL=http://control-plane.test:4310",
      "HARDEN_LLM_CONTROL_PLANE_INTERNAL_TOKEN=synthetic-identity-token",
      "PRLS_PORTAL_URL=https://portal.test",
      "JINA_API_KEY='fixture$shared'",
    ].join("\n") + "\n"),
    requiredVariables: ["HASH", "JSON_VALUE", "PRECEDENCE"],
    services: {
      probe: {
        container: "unused-in-config-only-test",
        expectedImage: "sha256:" + "b".repeat(64),
        manageable: false,
        identityEnvironment: {},
        ignoredEnvironmentKeys: [],
        allowedDifferenceFields: ["environment"],
        compareMountContents: false,
      },
    },
    serviceEnvironmentOverrides: {},
  });

  const model = renderComposeConfiguration(resolveConfiguration(descriptor), undefined, {
    PATH: process.env.PATH,
    HOME: process.env.HOME,
    PRECEDENCE: "ambient-must-not-win",
    HASH: "ambient-must-not-win",
  });
  const environment = model.services.probe.environment;
  assert.equal(environment.HASH, "$2a$12$literal-bcrypt-hash");
  assert.deepEqual(JSON.parse(environment.JSON_VALUE), { primary: "$literal", number: 7 });
  assert.equal(environment.EMPTY_VALUE, "");
  assert.equal(environment.PRECEDENCE, "production");
  assert.equal(environment.DEFAULT_VALUE, "default-value");
  assert.equal(model.services.probe.image, "alpine:3.20");
});
