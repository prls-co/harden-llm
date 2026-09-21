// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-233 TEST-234
import test from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import {
  buildComposeEnvironment,
  compareResolvedConfiguration,
  loadDescriptor,
  readEnvironmentSource,
  resolveConfiguration,
  runApply,
  runCheck,
  summarizeComparison,
  validateDescriptor,
} from "../production-config.mjs";
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";

function fixtureDirectory(t) {
  const directory = mkdtempSync(path.join(tmpdir(), "harden-llm-production-config-test-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  return directory;
}

function privateFile(directory, name, contents) {
  const filePath = path.join(directory, name);
  writeFileSync(filePath, contents, { mode: 0o600 });
  chmodSync(filePath, 0o600);
  return filePath;
}

function descriptorFor(t, { shared = "", observability, production, serviceEnvironmentOverrides = {}, identityEnvironment = {}, allowedDifferenceFields = ["environment"] } = {}) {
  const directory = fixtureDirectory(t);
  const descriptor = {
    schemaVersion: 1,
    project: "harden-llm",
    dockerContext: "default",
    composeRoot: directory,
    applicationRoot: directory,
    composeFiles: ["docker-compose.yml", "deploy/langfuse/docker-compose.upstream.yml", "deploy/langfuse/compose.private.yml", "deploy/frontend/compose.frontend.yml"],
    productionEnvFile: privateFile(directory, "production.env", production ?? "HARDEN_LLM_RELEASE=fixture-release\n"),
    observabilityEnvFile: privateFile(directory, "observability.env", observability ?? "PRLS_ALLURE_HOST=allure.example\n"),
    sharedApplicationEnvFile: privateFile(directory, "shared.env", shared),
    requiredVariables: ["PRLS_ALLURE_HOST", "HARDEN_LLM_RELEASE"],
    services: {
      probe: {
        container: "harden-llm-probe-1",
        expectedImage: "sha256:" + "a".repeat(64),
        manageable: true,
        identityEnvironment,
        ignoredEnvironmentKeys: ["HARDEN_LLM_RELEASE"],
        allowedDifferenceFields,
        compareMountContents: true,
      },
    },
    serviceEnvironmentOverrides,
  };
  return validateDescriptor(descriptor);
}

function composeModel(directory, literalPath) {
  return {
    services: {
      probe: {
        environment: {
          MODE: "production",
          CONFIG_LITERAL: "fixture$literal",
          HARDEN_LLM_RELEASE: "rendered-release",
        },
        image: "fixture/probe:1",
        command: ["probe", "--serve"],
        entrypoint: ["/bin/probe"],
        volumes: [{ type: "bind", source: literalPath, target: "/etc/probe/config", read_only: true }],
        networks: { default: {} },
        ports: [],
        healthcheck: { test: ["CMD", "probe", "health"], interval: "30s", timeout: "5s", retries: 3, start_period: "0s" },
        restart: "unless-stopped",
      },
    },
  };
}

function runtimeContainer(directory, literalPath, { mode = "production", image = "sha256:" + "a".repeat(64), release = "runtime-release" } = {}) {
  return {
    Image: image,
    Config: {
      Labels: { "com.docker.compose.project": "harden-llm", "com.docker.compose.service": "probe" },
      Env: [`MODE=${mode}`, "CONFIG_LITERAL=fixture$literal", `HARDEN_LLM_RELEASE=${release}`],
      Cmd: ["probe", "--serve"],
      Entrypoint: ["/bin/probe"],
      Healthcheck: { Test: ["CMD", "probe", "health"], Interval: 30_000_000_000, Timeout: 5_000_000_000, Retries: 3, StartPeriod: 0 },
    },
    Mounts: [{ Type: "bind", Source: literalPath, Destination: "/etc/probe/config", RW: false }],
    NetworkSettings: { Networks: { "harden-llm_default": {} } },
    HostConfig: { PortBindings: {}, RestartPolicy: { Name: "unless-stopped" } },
  };
}

test("TEST-233 resolves approved ownership and excludes ambient application variables", (t) => {
  const directory = fixtureDirectory(t);
  const shared = "JINA_API_KEY='fixture$literal'\nHARDEN_LLM_PROVIDER_PRIVATE_ALLOWLIST=''\nHARDEN_LLM_STATIC_TOKEN=must-not-escape\n";
  const descriptor = descriptorFor(t, {
    shared,
    observability: "PRLS_ALLURE_HOST=allure.example\nPRLS_TESTS_BASIC_AUTH_HASH='$2a$12$fixture'\n",
    production: "HARDEN_LLM_RELEASE=fixture-release\nGF_SERVER_DOMAIN=grafana.example\n",
  });

  const resolved = resolveConfiguration(descriptor);
  assert.equal(resolved.sharedValues.JINA_API_KEY, "fixture$literal");
  assert.equal(resolved.sharedValues.HARDEN_LLM_PROVIDER_PRIVATE_ALLOWLIST, "");
  assert.equal(resolved.effective.GF_SERVER_DOMAIN, "grafana.example");
  const environment = buildComposeEnvironment(resolved, {
    PATH: "/fixture/bin",
    HOME: "/fixture/home",
    HARDEN_LLM_STATIC_TOKEN: "ambient-secret",
    COMPOSE_FILE: "branch-controlled.yml",
    HARDEN_LLM_RELEASE: "ambient-release",
    DOCKER_HOST: "tcp://ambient.example:2376",
    DOCKER_CONTEXT: "ambient-context",
    DOCKER_CONFIG: "/ambient/docker",
  });
  assert.equal(environment.JINA_API_KEY, "fixture$literal");
  assert.equal(environment.HARDEN_LLM_STATIC_TOKEN, undefined);
  assert.equal(environment.COMPOSE_FILE, undefined);
  assert.equal(environment.HARDEN_LLM_RELEASE, undefined);
  assert.equal(environment.DOCKER_HOST, undefined);
  assert.equal(environment.DOCKER_CONTEXT, undefined);
  assert.equal(environment.DOCKER_CONFIG, undefined);
  assert.equal(environment.PATH, "/fixture/bin");
  assert(!JSON.stringify({ resolved: { ...resolved, sources: resolved.sources.map(({ values, ...source }) => source) }, environment }).includes("ambient-secret"));

  const duplicate = privateFile(directory, "duplicate.env", "A=one\nA=two\n");
  assert.throws(() => readEnvironmentSource(duplicate, "duplicate"), /duplicate assignments/);
  const malformed = privateFile(directory, "malformed.env", "not an assignment\n");
  assert.throws(() => readEnvironmentSource(malformed, "malformed"), /malformed assignment/);
});

test("TEST-233 rejects a conflicting PRLS owner and credential-shaped descriptor data", (t) => {
  const descriptor = descriptorFor(t, {
    observability: "PRLS_ALLURE_HOST=observability.example\n",
    production: "PRLS_ALLURE_HOST=production.example\nHARDEN_LLM_RELEASE=fixture-release\n",
  });
  assert.throws(() => resolveConfiguration(descriptor), /conflict/);
  assert.throws(() => validateDescriptor({ ...descriptor, serviceEnvironmentOverrides: { probe: { PROBE_PASSWORD: "fixture" } } }), /credential-shaped/);
});

test("TEST-234 reports semantic identity and mount equality without exposing values", (t) => {
  const directory = fixtureDirectory(t);
  const mountedFile = privateFile(directory, "probe.conf", "literal fixture configuration\n");
  const descriptor = descriptorFor(t, {
    serviceEnvironmentOverrides: { probe: { HARDEN_LLM_RELEASE: "rendered-release" } },
    identityEnvironment: { HARDEN_LLM_RELEASE: "runtime-release" },
  });
  const model = composeModel(directory, mountedFile);
  const runtime = [{ service: "probe", container: runtimeContainer(directory, mountedFile) }];
  assert.deepEqual(compareResolvedConfiguration(model, runtime, descriptor), []);
  assert.deepEqual(compareResolvedConfiguration(model, runtime, descriptor, ["probe"], { probe: null }), ["probe.image-reference"]);
  assert.deepEqual(summarizeComparison([]), { equivalent: true, differences: [] });

  const changed = runtimeContainer(directory, mountedFile, { mode: "changed" });
  const differences = compareResolvedConfiguration(model, [{ service: "probe", container: changed }], descriptor);
  assert.deepEqual(differences, ["probe.environment.MODE"]);
  assert(!JSON.stringify(summarizeComparison(differences)).includes("fixture$literal"));
});

test("TEST-234 check is read-only and equivalent apply never invokes up", (t) => {
  const directory = fixtureDirectory(t);
  const mountedFile = privateFile(directory, "probe.conf", "fixture configuration\n");
  const descriptor = descriptorFor(t, { serviceEnvironmentOverrides: { probe: { HARDEN_LLM_RELEASE: "rendered-release" } }, identityEnvironment: { HARDEN_LLM_RELEASE: "runtime-release" } });
  const model = composeModel(directory, mountedFile);
  const runtime = runtimeContainer(directory, mountedFile);
  const calls = [];
  const run = (bin, args, options) => {
    calls.push({ bin, args: [...args], environmentKeys: Object.keys(options.env).sort() });
    if (args.includes("config")) return { status: 0, stdout: JSON.stringify(model), stderr: "" };
    if (args.includes("image")) return { status: 0, stdout: "sha256:" + "a".repeat(64), stderr: "" };
    if (args[0] === "--context" && args.includes("inspect")) return { status: 0, stdout: JSON.stringify([runtime]), stderr: "" };
    throw new Error("unexpected mutating command");
  };
  const checked = runCheck(descriptor, { services: ["probe"], run });
  assert.deepEqual(checked.differences, []);
  const applied = runApply(descriptor, ["probe"], run);
  assert.equal(applied.applied, false);
  assert.equal(calls.filter(({ args }) => args.includes("up")).length, 0);
  assert.equal(calls.filter(({ args }) => args.includes("config")).length, 2);
  assert(calls.every(({ environmentKeys }) => !environmentKeys.includes("HARDEN_LLM_STATIC_TOKEN")));
  assert(!JSON.stringify(calls).includes("fixture$literal"));
  assert(!readFileSync(descriptor.sharedApplicationEnvFile, "utf8").includes("ambient"));
});

test("TEST-234 blocks an unapproved runtime difference before application", (t) => {
  const directory = fixtureDirectory(t);
  const mountedFile = privateFile(directory, "probe.conf", "fixture configuration\n");
  const descriptor = descriptorFor(t, { serviceEnvironmentOverrides: { probe: { HARDEN_LLM_RELEASE: "rendered-release" } }, identityEnvironment: { HARDEN_LLM_RELEASE: "runtime-release" }, allowedDifferenceFields: ["image"] });
  const model = composeModel(directory, mountedFile);
  const runtime = runtimeContainer(directory, mountedFile, { mode: "unexpected" });
  const calls = [];
  const run = (bin, args) => {
    calls.push(args);
    if (args.includes("config")) return { status: 0, stdout: JSON.stringify(model), stderr: "" };
    if (args.includes("image")) return { status: 0, stdout: "sha256:" + "a".repeat(64), stderr: "" };
    if (args.includes("inspect")) return { status: 0, stdout: JSON.stringify([runtime]), stderr: "" };
    throw new Error("up must not be called");
  };
  assert.throws(() => runApply(descriptor, ["probe"], run), /unapproved difference/);
  assert.equal(calls.filter((args) => args.includes("up")).length, 0);
});

test("TEST-234 rejects a container from the wrong Compose project and never fills sources from it", (t) => {
  const directory = fixtureDirectory(t);
  const mountedFile = privateFile(directory, "probe.conf", "fixture configuration\n");
  const descriptor = descriptorFor(t, { serviceEnvironmentOverrides: { probe: { HARDEN_LLM_RELEASE: "rendered-release" } }, identityEnvironment: { HARDEN_LLM_RELEASE: "runtime-release" } });
  const model = composeModel(directory, mountedFile);
  const runtime = runtimeContainer(directory, mountedFile);
  runtime.Config.Labels["com.docker.compose.project"] = "other-project";
  const calls = [];
  const run = (bin, args) => {
    calls.push(args);
    if (args.includes("config")) return { status: 0, stdout: JSON.stringify(model), stderr: "" };
    if (args.includes("inspect")) return { status: 0, stdout: JSON.stringify([runtime]), stderr: "" };
    throw new Error("unexpected command");
  };
  assert.throws(() => runCheck(descriptor, { services: ["probe"], run }), /unexpected Compose project/);
  assert.equal(calls.filter((args) => args.includes("up")).length, 0);
});

test("TEST-233 missing approved sources fail before runtime inspection", (t) => {
  const descriptor = descriptorFor(t);
  const calls = [];
  const run = (_bin, args) => {
    calls.push(args);
    throw new Error("runtime inspection must not be used as a source");
  };
  descriptor.productionEnvFile = path.join(fixtureDirectory(t), "missing.env");
  assert.throws(() => runCheck(descriptor, { services: ["probe"], run }), /production environment source is unavailable/);
  assert.equal(calls.length, 0);
});

test("TEST-234 applies only the selected service with the reviewed nonsecret override", (t) => {
  const directory = fixtureDirectory(t);
  const mountedFile = privateFile(directory, "probe.conf", "fixture configuration\n");
  const descriptor = descriptorFor(t, { serviceEnvironmentOverrides: { probe: { HARDEN_LLM_RELEASE: "rendered-release" } }, identityEnvironment: { HARDEN_LLM_RELEASE: "runtime-release" }, allowedDifferenceFields: ["environment"] });
  const model = composeModel(directory, mountedFile);
  const runtime = runtimeContainer(directory, mountedFile, { mode: "old" });
  const calls = [];
  const run = (bin, args) => {
    calls.push([...args]);
    if (args.includes("config")) return { status: 0, stdout: JSON.stringify(model), stderr: "" };
    if (args.includes("image")) return { status: 0, stdout: "sha256:" + "a".repeat(64), stderr: "" };
    if (args.includes("up")) {
      runtime.Config.Env = ["MODE=production", "CONFIG_LITERAL=fixture$literal", "HARDEN_LLM_RELEASE=runtime-release"];
      return { status: 0, stdout: "", stderr: "" };
    }
    if (args.includes("inspect")) return { status: 0, stdout: JSON.stringify([runtime]), stderr: "" };
    throw new Error("unexpected command");
  };
  const result = runApply(descriptor, ["probe"], run);
  assert.equal(result.applied, true);
  const up = calls.find((args) => args.includes("up"));
  assert(up);
  assert(up.includes("--no-build"));
  assert(up.includes("--no-deps"));
  assert(up.includes("--pull") && up.includes("never"));
  assert(up.includes("--wait"));
  assert.equal(up.filter((argument) => argument === "probe").length, 1);
  assert(up.includes("-f"));
  assert(!JSON.stringify(calls).includes("fixture$literal"));
});

test("TEST-234 descriptor loading keeps the descriptor itself nonsecret", (t) => {
  const descriptor = descriptorFor(t);
  const descriptorPath = privateFile(fixtureDirectory(t), "descriptor.json", JSON.stringify(descriptor));
  const loaded = loadDescriptor(descriptorPath);
  assert.equal(loaded.descriptorPath, descriptorPath);
  assert(!JSON.stringify(loaded).includes("API_KEY"));
});

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-260
function candidateDescriptorFor(t, {
  desiredRelease = "old-release",
  desiredImageVersion = desiredRelease,
  runtimeRelease = desiredRelease,
  runtimeImageVersion = runtimeRelease,
  runtimeHealth = "healthy",
} = {}) {
  const directory = fixtureDirectory(t);
  const service = "harden-llm-gateway";
  const image = "sha256:" + "a".repeat(64);
  const descriptor = {
    schemaVersion: 1,
    project: "harden-llm",
    dockerContext: "default",
    composeRoot: directory,
    applicationRoot: directory,
    composeFiles: ["docker-compose.yml", "deploy/langfuse/docker-compose.upstream.yml", "deploy/langfuse/compose.private.yml", "deploy/frontend/compose.frontend.yml"],
    productionEnvFile: privateFile(directory, "production.env", `HARDEN_LLM_RELEASE=${desiredRelease}\n`),
    observabilityEnvFile: privateFile(directory, "observability.env", "PRLS_ALLURE_HOST=allure.example\n"),
    sharedApplicationEnvFile: privateFile(directory, "shared.env", ""),
    requiredVariables: ["PRLS_ALLURE_HOST", "HARDEN_LLM_RELEASE"],
    services: {
      [service]: {
        container: "harden-llm-gateway-1",
        expectedImage: image,
        manageable: true,
        identityEnvironment: { HARDEN_LLM_RELEASE: desiredRelease },
        ignoredEnvironmentKeys: [],
        allowedDifferenceFields: ["environment", "image", "identity"],
        compareMountContents: false,
      },
    },
    serviceEnvironmentOverrides: { [service]: { HARDEN_LLM_RELEASE: desiredRelease } },
    serviceImageOverrides: { [service]: "harden-llm-gateway:fixture" },
    __candidateFixture: { service, image, desiredImageVersion, runtimeRelease, runtimeImageVersion, runtimeHealth },
  };
  return validateDescriptor(descriptor);
}

function candidateComposeModel(directory, desiredRelease) {
  return {
    services: {
      "harden-llm-gateway": {
        environment: { HARDEN_LLM_RELEASE: desiredRelease },
        image: "harden-llm-gateway:fixture",
        command: ["gateway"],
        entrypoint: ["/bin/gateway"],
        volumes: [],
        networks: { default: {} },
        ports: [],
        healthcheck: { test: ["CMD", "gateway", "health"], interval: "30s", timeout: "5s", retries: 3, start_period: "0s" },
        restart: "unless-stopped",
      },
    },
  };
}

function candidateRuntimeContainer({ image, runtimeRelease, runtimeImageVersion, runtimeHealth }) {
  return {
    Image: image,
    Config: {
      Labels: {
        "com.docker.compose.project": "harden-llm",
        "com.docker.compose.service": "harden-llm-gateway",
        "org.opencontainers.image.version": runtimeImageVersion,
      },
      Env: [`HARDEN_LLM_RELEASE=${runtimeRelease}`],
      Cmd: ["gateway"],
      Entrypoint: ["/bin/gateway"],
      Healthcheck: { Test: ["CMD", "gateway", "health"], Interval: 30_000_000_000, Timeout: 5_000_000_000, Retries: 3, StartPeriod: 0 },
    },
    State: { Status: "running", Health: { Status: runtimeHealth } },
    Mounts: [],
    NetworkSettings: { Networks: { "harden-llm_default": {} } },
    HostConfig: { PortBindings: {}, RestartPolicy: { Name: "unless-stopped" } },
  };
}

function candidateRunner(descriptor, { candidateRelease = null } = {}) {
  const fixture = descriptor.__candidateFixture;
  const model = candidateComposeModel(descriptor.composeRoot, descriptor.serviceEnvironmentOverrides[fixture.service].HARDEN_LLM_RELEASE);
  const runtime = candidateRuntimeContainer(fixture);
  return (_bin, args) => {
    if (args.includes("config")) return { status: 0, stdout: JSON.stringify(model), stderr: "" };
    if (args.includes("image")) {
      const metadata = args.some((argument) => argument.includes("org.opencontainers.image.version"));
      return { status: 0, stdout: metadata ? `${fixture.image}|${fixture.desiredImageVersion}\n` : `${fixture.image}\n`, stderr: "" };
    }
    if (args.includes("up")) {
      runtime.Config.Labels["org.opencontainers.image.version"] = candidateRelease;
      runtime.Config.Env = [`HARDEN_LLM_RELEASE=${candidateRelease}`];
      runtime.State = { Status: "running", Health: { Status: "healthy" } };
      return { status: 0, stdout: "", stderr: "" };
    }
    if (args.includes("inspect")) return { status: 0, stdout: JSON.stringify([runtime]), stderr: "" };
    throw new Error(`unexpected fake Docker command: ${args.join(" ")}`);
  };
}

test("TEST-260 candidate release rejects a stale but internally equivalent descriptor", (t) => {
  const descriptor = candidateDescriptorFor(t, { runtimeRelease: "old-release", runtimeImageVersion: "old-release" });
  const checked = runCheck(descriptor, { services: ["harden-llm-gateway"], expectedRelease: "b".repeat(40), run: candidateRunner(descriptor) });
  assert.notDeepEqual(checked.differences, [], "candidate intent must not be ignored");
  assert(checked.differences.some((difference) => difference.includes("candidate")), checked.differences);
});

test("TEST-260 candidate release rejects a wrong image label and unhealthy runtime", (t) => {
  const expectedRelease = "b".repeat(40);
  const wrongLabel = candidateDescriptorFor(t, { desiredRelease: expectedRelease, desiredImageVersion: "old-release", runtimeRelease: expectedRelease, runtimeImageVersion: "old-release" });
  const wrongLabelResult = runCheck(wrongLabel, { services: ["harden-llm-gateway"], expectedRelease, run: candidateRunner(wrongLabel) });
  assert(wrongLabelResult.differences.some((difference) => difference.includes("candidate")), wrongLabelResult.differences);

  const unhealthy = candidateDescriptorFor(t, { desiredRelease: expectedRelease, runtimeRelease: expectedRelease, runtimeHealth: "starting" });
  const unhealthyResult = runCheck(unhealthy, { services: ["harden-llm-gateway"], expectedRelease, run: candidateRunner(unhealthy) });
  assert(unhealthyResult.differences.some((difference) => difference.includes("health")), unhealthyResult.differences);
});

test("TEST-260 candidate-aware apply permits an old runtime and requires convergence", (t) => {
  const expectedRelease = "b".repeat(40);
  const descriptor = candidateDescriptorFor(t, { desiredRelease: expectedRelease, runtimeRelease: "old-release", runtimeImageVersion: "old-release" });
  const calls = [];
  const fakeRunner = candidateRunner(descriptor, { candidateRelease: expectedRelease });
  const result = runApply(descriptor, ["harden-llm-gateway"], (...args) => {
    calls.push(args[1]);
    return fakeRunner(...args);
  }, { expectedRelease });
  assert.equal(result.applied, true);
  assert(calls.some((args) => args.includes("up")));
  assert.equal(result.differences.length, 0);
});

test("TEST-260 candidate options reject invalid intent before Docker", (t) => {
  const descriptor = candidateDescriptorFor(t);
  assert.throws(() => runCheck(descriptor, { services: ["harden-llm-gateway"], expectedRelease: "not-a-sha", run: candidateRunner(descriptor) }), /40-character/);
  assert.throws(() => runCheck(descriptor, { services: ["harden-llm-gateway"], expectedRelease: "b".repeat(40), resolveOnly: true, run: candidateRunner(descriptor) }), /resolve-only/);
  assert.throws(() => runApply(descriptor, ["harden-llm-gateway"], candidateRunner(descriptor), { expectedRelease: "b".repeat(40) }), /desired application images/);

  const cli = spawnSync(process.execPath, ["scripts/production-config.mjs", "check", "--expected-release", "not-a-sha"], { encoding: "utf8" });
  assert.notEqual(cli.status, 0);
  assert.match(`${cli.stdout}\n${cli.stderr}`, /40-character/);
});
