// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-235
import test from "node:test";
import assert from "node:assert/strict";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { BATCH_COMPOSE_FILE_ORDER, renderComposeConfiguration, resolveConfiguration, runCheck, validateDescriptor } from "../production-config.mjs";

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

test("TEST-235 repository batch overlay resolves with shared authority, worker command and independent health", t => {
  const directory = mkdtempSync(path.join(tmpdir(), "harden-llm-batch-compose-"));
  t.after(() => rmSync(directory, {recursive: true, force: true}));
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
  const shared = {
    HARDEN_LLM_CONFIG_FILE: path.join(directory, "upstreams.json"),
    HARDEN_LLM_TOKEN: "synthetic-incoming-token", CPA_API_KEY: "synthetic-upstream-token",
    HARDEN_LLM_CONTROL_PLANE_URL: "http://control-plane.test:4310",
    HARDEN_LLM_CONTROL_PLANE_INTERNAL_TOKEN: "synthetic-identity-token", PRLS_PORTAL_URL: "https://portal.test",
  };
  const production = Object.fromEntries(BATCH_COMPOSE_FILE_ORDER.flatMap(file =>
    [...readFileSync(path.join(root, file), "utf8").matchAll(/\$\{([A-Z0-9_]+):\?[^}]*\}/g)]
      .map(match => [match[1], "synthetic-fixture"])).filter(([key]) => !Object.hasOwn(shared, key)));
  Object.assign(production, {
    HARDEN_LLM_RELEASE: "b".repeat(40), HARDEN_LLM_DATABASE_URL: "postgresql://fixture:synthetic@postgres:5432/hllm",
    HARDEN_LLM_ARTIFACT_ENDPOINT: "http://garage:3900", HARDEN_LLM_TEMPORAL_ADDRESS: "temporal:7233",
    HARDEN_LLM_POSTGRES_NETWORK: "fixture-shared", HARDEN_LLM_TEMPORAL_NETWORK: "fixture-shared",
  });
  const envText = values => Object.entries(values).map(([key, value]) => `${key}=${JSON.stringify(value)}`).join("\n") + "\n";
  const identity = {container: "unused-config-only", expectedImage: "sha256:" + "a".repeat(64), manageable: true,
    identityEnvironment: {}, ignoredEnvironmentKeys: [], allowedDifferenceFields: ["environment"], compareMountContents: false};
  const descriptor = validateDescriptor({
    schemaVersion: 1, project: "harden-llm", dockerContext: "default", composeRoot: root, applicationRoot: root,
    composeFiles: [...BATCH_COMPOSE_FILE_ORDER], requiredVariables: [],
    productionEnvFile: privateFile(path.join(directory, "production.env"), envText(production)),
    observabilityEnvFile: privateFile(path.join(directory, "observability.env"), "# all synthetic values in production fixture\n"),
    sharedApplicationEnvFile: privateFile(path.join(directory, "shared.env"), envText(shared)),
    services: {"harden-llm-gateway": identity, "harden-llm-batch-worker": {...identity}},
    serviceImageOverrides: {"harden-llm-gateway": "harden-llm-gateway:fixture", "harden-llm-batch-worker": "harden-llm-gateway:fixture"},
  });
  const {resolved, runtimeVerified} = runCheck(descriptor, {services: ["harden-llm-gateway"], resolveOnly: true});
  assert.equal(runtimeVerified, false);
  const gateway = resolved.model.services["harden-llm-gateway"], worker = resolved.model.services["harden-llm-batch-worker"];
  assert.equal(worker.image, gateway.image);
  assert.equal(resolved.model.services["gateway-schema"].image, gateway.image);
  assert.deepEqual(worker.command, ["worker"]);
  assert.equal(worker.healthcheck.disable, true);
  assert.notEqual(gateway.healthcheck.disable, true);
  assert.equal(worker.stop_grace_period, "2m0s");
  assert.equal(worker.environment.HARDEN_LLM_SERVICE_NAME, "harden-llm-batch-worker");
  assert.equal(worker.environment.CPA_API_KEY, shared.CPA_API_KEY);
  assert.deepEqual(worker.volumes, gateway.volumes);
});
