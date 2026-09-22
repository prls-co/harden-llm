#!/usr/bin/env node

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-279 PLAN-HLLM-SCALE-001
// PLAN-HLLM-WIDGET-PARITY-001 TEST-117 TEST-268

import { promises as fs } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { loadManifest, selectTasks } from "./run-test-tier.mjs";

const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const manifestPath = path.join(repositoryRoot, "test", "test-tiers.json");
const makefilePath = path.join(repositoryRoot, "Makefile");
const workflowPath = path.join(repositoryRoot, ".github", "workflows", "test-hierarchy.yml");

const requiredTargets = {
  "test-fast": "fast",
  "test-browser": "browser",
  "test-release": "release",
  "test-live": "live",
};

const requiredCommands = [
  ["make", "format"],
  ["make", "lint"],
  ["make", "build"],
  ["make", "test-static"],
  ["make", "test-unit"],
  ["make", "test-parity"],
  ["go", "test", "-p=${HARDEN_LLM_TEST_PACKAGE_SLOTS}", "./...", "-tags=integration", "-count=1"],
  ["go", "test", "-race", "-p=${HARDEN_LLM_TEST_PACKAGE_SLOTS}", "./...", "-tags=integration", "-count=1"],
  ["make", "test-api"],
  ["make", "test-observability"],
  ["go", "test", "./internal/smoke/...", "-tags=compose", "-run", "TestComposeSmoke", "-count=1"],
  ["make", "test-race"],
  ["make", "test-vulnerability"],
  ["make", "live-structured-call"],
  ["mix", "format", "--check-formatted"],
  ["mix", "compile", "--warnings-as-errors"],
  ["mix", "test"],
  ["mix", "test", "--only", "browser", "--max-cases", "1"],
  ["mix", "test", "--only", "compose", "--max-cases", "1"],
  ["mix", "deps.audit"],
  ["mix", "hex.audit"],
  ["mix", "assets.deploy"],
  ["mix", "release"],
  ["make", "verify"],
  ["node", "scripts/run-deployed-browser-test.mjs", "--allow-browser"],
];

const closeoutRegistrations = Object.freeze({
  "TEST-260": "go-static",
  "TEST-261": "go-api",
  "TEST-262": "go-api",
  "TEST-263": "go-api",
  "TEST-264": "frontend-deterministic",
  "TEST-265": "frontend-deterministic",
  "TEST-266": "frontend-deterministic",
  "TEST-267": "go-integration",
  "TEST-268": "go-static",
});

const closeoutOperationalIds = Object.freeze(["TEST-269", "TEST-270"]);
const closeoutBackendIds = Object.freeze(["TEST-260", "TEST-261", "TEST-262", "TEST-263", "TEST-267", "TEST-268"]);
const closeoutFrontendIds = Object.freeze(["WEB-TEST-100", "WEB-TEST-101", "WEB-TEST-102"]);

function fail(message) {
  throw new Error(message);
}

function targetBody(makefile, target) {
  const lines = makefile.split("\n");
  const start = lines.findIndex((line) => line === `${target}:`);
  if (start < 0) return null;
  const body = [];
  for (let index = start + 1; index < lines.length; index += 1) {
    if (lines[index] !== "" && !lines[index].startsWith("\t")) break;
    if (lines[index].startsWith("\t")) body.push(lines[index].slice(1));
  }
  return body;
}

function assertCheapTask(task) {
  if (!["T0", "T1", "T2"].includes(task.tier)) fail(`fast task ${task.id} has tier ${task.tier}`);
  if (task.network !== "forbidden") fail(`fast task ${task.id} permits ${task.network} network`);
  if ((task.credentialKeys ?? []).length !== 0) fail(`fast task ${task.id} declares credentials`);
  if (task.container) fail(`fast task ${task.id} starts a container`);
  for (const key of Object.keys(task.environment ?? {})) {
    if (/(password|secret|token|api[_-]?key|access[_-]?key)/i.test(key)) fail(`fast task ${task.id} has credential-shaped environment key ${key}`);
  }
  const command = task.command.join(" ").toLowerCase();
  for (const forbidden of ["integration", "compose", "browser", "live-structured-call", "-tags=", "govulncheck"]) {
    if (command.includes(forbidden)) fail(`fast task ${task.id} contains ${forbidden}`);
  }
}

async function main() {
  const manifest = await loadManifest(manifestPath);
  const makefile = await fs.readFile(makefilePath, "utf8");
  const workflow = await fs.readFile(workflowPath, "utf8");
  const runner = await fs.readFile(path.join(repositoryRoot, "scripts", "run-test-tier.mjs"), "utf8");
  const specificationDirectory = path.join(repositoryRoot, "plans", "from_utility-llm");
  const specificationNames = await fs.readdir(specificationDirectory);
  const backendSpec = await fs.readFile(path.join(specificationDirectory, "harden-llm-self-hosted-test-spec.md"), "utf8");
  const frontendSpecName = specificationNames.find((name) => name.endsWith("-frontend-spec.md"));
  if (!frontendSpecName) fail("frontend test specification is missing");
  const frontendSpec = await fs.readFile(path.join(specificationDirectory, frontendSpecName), "utf8");
  const verifyLine = "verify: format lint build test-static test-unit test-parity test-integration test-integration-race test-api test-observability test-race test-vulnerability";
  if (!makefile.includes(verifyLine)) fail("make verify dependency contract changed");

  for (const [target, selector] of Object.entries(requiredTargets)) {
    const body = targetBody(makefile, target);
    if (!body) fail(`missing Make target ${target}`);
    const expected = `$(NODE) scripts/run-test-tier.mjs --task ${selector}${selector === "browser" ? " --allow-browser" : ""}`;
    if (!body.includes(expected)) fail(`${target} must delegate to ${expected}`);
    if (body.some((line) => /\b(go|mix|node)\s+(test|format|compile|build)\b/.test(line))) fail(`${target} composes a task command outside the manifest`);
  }
  const benchmarkBody = targetBody(makefile, "benchmark-test-feedback");
  if (!benchmarkBody?.some((line) => line.includes("scripts/benchmark-test-feedback.mjs"))) fail("benchmark-test-feedback must delegate to the benchmark harness");

  const composeBody = targetBody(makefile, "test-compose");
  if (!composeBody?.includes("$(NODE) scripts/run-test-tier.mjs --task go-compose")) {
    fail("test-compose must delegate to the managed go-compose task");
  }
  const composeTask = manifest.tasks.find((task) => task.id === "go-compose");
  if (!composeTask || composeTask.command.join(" ") !== "go test ./internal/smoke/... -tags=compose -run TestComposeSmoke -count=1") {
    fail("go-compose must execute the original raw Compose smoke command without Make recursion");
  }
  if (composeTask.tier !== "T4" || composeTask.network !== "local-only" || composeTask.requiresBrowser) {
    fail("go-compose must remain an explicit browser-free local service boundary");
  }
  if (composeTask.requiresDocker !== true) fail("go-compose must acquire the local Docker lifecycle guard");

  const dockerLifecycle = manifest.tasks.find((task) => task.id === "test-resource-lifecycle-docker");
  if (!dockerLifecycle) fail("manifest is missing the opt-in test-resource-lifecycle-docker task");
  if (dockerLifecycle.command.join(" ") !== "node scripts/test/test_resource_lifecycle_docker_test.mjs"
      || dockerLifecycle.tier !== "T3"
      || dockerLifecycle.resourceClass !== "release"
      || dockerLifecycle.network !== "local-only"
      || dockerLifecycle.requiresDocker !== true
      || dockerLifecycle.timeoutMs !== 300000
      || (dockerLifecycle.credentialKeys ?? []).length !== 0
      || (dockerLifecycle.requiredFor ?? []).length !== 0) {
    fail("the real Docker lifecycle boundary must remain a credential-free, explicit-only T3 task with its recorded deadline");
  }
  try { await fs.access(path.join(repositoryRoot, "scripts", "test", "test_resource_lifecycle_docker_test.mjs")); }
  catch { fail("the registered Docker lifecycle boundary test file is missing"); }
  const dockerLifecycleOccurrences = manifest.tasks.flatMap((task) => (task.testIds ?? []).filter((id) => id === "TEST-274").map(() => task.id));
  if (dockerLifecycleOccurrences.length !== 1 || dockerLifecycleOccurrences[0] !== "test-resource-lifecycle-docker") {
    fail("TEST-274 must be registered exactly once in the explicit real-Docker lifecycle task");
  }
  for (const selector of ["baseline", "fast", "release"]) {
    if (selectTasks(manifest, selector).some((task) => task.id === "test-resource-lifecycle-docker")) {
      fail(`the actual Docker lifecycle task must not run in ${selector}`);
    }
  }
  if (!workflow.includes("options: [fast, integration, lifecycle, capacity, release, browser, full-with-browser]")
      || !workflow.includes("lifecycle: ${{ steps.mode.outputs.lifecycle }}")
      || !workflow.includes("if: needs.mode.outputs.lifecycle == 'true'")
      || !workflow.includes("docker pull alpine@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc")
      || !workflow.includes("node scripts/run-test-tier.mjs --task test-resource-lifecycle-docker")) {
    fail("the real Docker lifecycle boundary must be reachable only through its explicit browser-free manual workflow selector");
  }

  const capacityTask = manifest.tasks.find((task) => task.id === "capacity-baseline");
  if (!capacityTask || capacityTask.command.join(" ") !== "go test ./cmd/harden-llm-gateway -tags=integration,capacity -run ^TestGatewayCapacityBaseline$ -count=1"
      || capacityTask.tier !== "T3"
      || capacityTask.resourceClass !== "service"
      || capacityTask.network !== "local-only"
      || capacityTask.requiresBrowser
      || capacityTask.requiresDocker
      || capacityTask.capacityReport !== true
      || capacityTask.sampleDockerResources !== true
      || capacityTask.timeoutMs !== 2_400_000
      || capacityTask.servicePool?.composeFile !== "deploy/test/compose.integration.yml"
      || JSON.stringify((capacityTask.servicePool?.services ?? []).map(({ name }) => name).sort()) !== JSON.stringify(["garage", "harden-postgres"])
      || (capacityTask.requiredFor ?? []).length !== 0) {
    fail("the real-gateway capacity suite must be an explicit-only, credential-free pooled T3 task with bounded report and resource sampling");
  }
  const capacityTestOccurrences = manifest.tasks.flatMap((task) => (task.testIds ?? []).filter((id) => id === "TEST-277").map(() => task.id));
  if (capacityTestOccurrences.length !== 1 || capacityTestOccurrences[0] !== "capacity-baseline") {
    fail("TEST-277 must be registered exactly once in the explicit capacity task");
  }
  for (const selector of ["baseline", "fast", "integration", "release"]) {
    if (selectTasks(manifest, selector).some((task) => task.id === "capacity-baseline")) {
      fail(`the capacity suite must not run in routine selector ${selector}`);
    }
  }
  if (!workflow.includes("options: [fast, integration, lifecycle, capacity, release, browser, full-with-browser]")
      || !workflow.includes("capacity: ${{ steps.mode.outputs.capacity }}")
      || !workflow.includes("capacityCaseSet:")
      || !workflow.includes("options: [correctness, exploration, holdout]")
      || !workflow.includes("HARDEN_LLM_CAPACITY_CASE_SET: ${{ inputs.capacityCaseSet }}")
      || !workflow.includes("node scripts/run-test-tier.mjs --task capacity-baseline")
      || !workflow.includes("actions/setup-node@v4")
      || !workflow.includes("timeout-minutes: 60")) {
    fail("capacity runs must be reachable only through the explicit manual service-suite workflow with a selected scenario set");
  }

  const runnerContracts = manifest.tasks.find((task) => task.id === "runner-contracts");
  if (!runnerContracts) fail("manifest is missing the runner-contracts task");
  if (!runnerContracts.command.includes("scripts/test/run_test_tier_test.mjs") || !runnerContracts.command.includes("scripts/test/test_resource_lifecycle_test.mjs") || !runnerContracts.command.includes("scripts/test/test_resource_measurement_test.mjs") || !runnerContracts.command.includes("scripts/test/gateway_image_publication_test.mjs")) {
    fail("runner-contracts must execute the runner, lifecycle, measurement, and private image-publication regressions");
  }
  if (!["T0", "T1", "T2"].includes(runnerContracts.tier) || runnerContracts.network !== "forbidden" || runnerContracts.servicePool || runnerContracts.container) {
    fail("runner-contracts must remain cheap, offline, and container-free");
  }
  for (const selector of ["baseline", "fast", "release"]) {
    if (!(runnerContracts.requiredFor ?? []).includes(selector)) fail(`runner-contracts must be registered for ${selector}`);
  }
  const expectedRunnerTestIDs = ["TEST-049", "TEST-271", "TEST-272", "TEST-273", "TEST-275", "TEST-283"];
  for (const testId of expectedRunnerTestIDs) {
    const occurrences = manifest.tasks.flatMap((task) => (task.testIds ?? []).filter((id) => id === testId).map(() => task.id));
    if (occurrences.length !== 1 || occurrences[0] !== "runner-contracts") fail(`${testId} must be registered exactly once in runner-contracts`);
  }
  const goStatic = manifest.tasks.find((task) => task.id === "go-static");
  const receiptTestOccurrences = manifest.tasks.flatMap((task) => (task.testIds ?? []).filter((id) => id === "TEST-280").map(() => task.id));
  if (receiptTestOccurrences.length !== 1 || receiptTestOccurrences[0] !== "go-static" || !goStatic) {
    fail("TEST-280 must be registered exactly once in the deterministic go-static task");
  }
  if (!makefile.includes("$(GO) test ./internal/integrationtest -run '^TestResourceReceipt' -count=1")) {
    fail("test-static must run the pure shared receipt contract test");
  }
  if (!runner.includes("MAX_RUN_REPORT_BYTES") || !runner.includes("writeRunReport") || !runner.includes("reportPath") || !runner.includes("cleanupWarnings")) {
    fail("runner must atomically persist bounded private per-invocation reports with cleanup diagnostics by default");
  }
  const reportUploadCount = workflow.match(/path: tmp\/test-feedback\/runner-\*\.json/g)?.length ?? 0;
  if (!workflow.includes("actions/upload-artifact@v4") || !workflow.includes("if: always()") || !workflow.includes("retention-days: 14") || reportUploadCount < 3) {
    fail("fast, integration, and release jobs must always upload bounded runner reports");
  }

  const knownCommands = new Set(manifest.tasks.map((task) => task.command.join(" ")));
  for (const command of requiredCommands) if (!knownCommands.has(command.join(" "))) fail(`manifest is missing command ${command.join(" ")}`);

  const laneOrder = manifest.benchmarkLaneOrder ?? [];
  if (laneOrder.length === 0 || new Set(laneOrder).size !== laneOrder.length) fail("benchmarkLaneOrder must be a non-empty unique list");
  const knownLanes = new Set(laneOrder);
  const laneCounts = new Map(laneOrder.map((lane) => [lane, 0]));
  for (const task of manifest.tasks) {
    for (const lane of task.benchmarkLanes ?? []) {
      if (!knownLanes.has(lane)) fail(`task ${task.id} references unknown benchmark lane ${lane}`);
      laneCounts.set(lane, laneCounts.get(lane) + 1);
    }
  }
  for (const [lane, count] of laneCounts) if (count === 0) fail(`benchmark lane ${lane} has no manifest tasks`);

  const fastTasks = selectTasks(manifest, "fast");
  for (const selector of ["fast", "baseline", "release"]) {
    if (selectTasks(manifest, selector).some(task => task.requiresBrowser)) fail(`${selector} must be browser-free`);
  }
  for (const id of ["frontend-browser", "frontend-compose", "frontend-deployed"]) {
    if (!manifest.tasks.find(task => task.id === id)?.requiresBrowser) fail(`${id} must declare explicit browser authorization`);
  }
  if (fastTasks.length === 0) fail("fast selection is empty");
  for (const task of fastTasks) assertCheapTask(task);

  const fastTestIds = new Set(fastTasks.flatMap((task) => task.testIds ?? []));
  for (const testId of [
    "TEST-101",
    "TEST-102",
    "TEST-103",
    "TEST-104",
    "TEST-105",
    "TEST-106",
    "TEST-107",
    "TEST-109",
    "TEST-110",
    "TEST-111",
    "TEST-112",
    "TEST-113",
    "TEST-115",
    "TEST-116",
  ]) {
    if (!fastTestIds.has(testId)) fail(`cheap selection is missing widget test ${testId}`);
  }

  const taskById = new Map(manifest.tasks.map((task) => [task.id, task]));
  const closeoutOccurrences = new Map();
  for (const task of manifest.tasks) {
    for (const testId of task.testIds ?? []) {
      if (Object.hasOwn(closeoutRegistrations, testId)) {
        closeoutOccurrences.set(testId, [...(closeoutOccurrences.get(testId) ?? []), task.id]);
      }
    }
  }
  for (const [testId, taskId] of Object.entries(closeoutRegistrations)) {
    if (!taskById.has(taskId)) fail(`closeout registration names missing task ${taskId}`);
    const occurrences = closeoutOccurrences.get(testId) ?? [];
    if (occurrences.length !== 1 || occurrences[0] !== taskId) {
      fail(`${testId} must be registered exactly once in ${taskId}`);
    }
    if (closeoutBackendIds.includes(testId) && !backendSpec.includes(testId)) fail(`backend specification is missing ${testId}`);
  }
  for (const testId of closeoutOperationalIds) {
    if (!backendSpec.includes(testId)) fail(`backend specification is missing ${testId}`);
    if (closeoutOccurrences.has(testId)) fail(`${testId} is an operational exception and must not be a manifest task`);
  }
  for (const testId of closeoutFrontendIds) {
    if (!frontendSpec.includes(testId)) fail(`frontend specification is missing ${testId}`);
    const occurrences = manifest.tasks.flatMap((task) => (task.testIds ?? []).filter((id) => id === testId).map(() => task.id));
    if (occurrences.length !== 1 || occurrences[0] !== "frontend-deterministic") {
      fail(`${testId} must be registered exactly once in frontend-deterministic`);
    }
  }

  const deployed = manifest.tasks.find((task) => task.id === "frontend-deployed");
  if (!deployed || deployed.tier !== "T5" || deployed.resourceClass !== "live" || deployed.network !== "public") {
    fail("frontend-deployed must be an explicit T5 live/public task");
  }

  const frontendFormat = manifest.tasks.find((task) => task.id === "frontend-format");
  if (!frontendFormat?.dependsOn?.includes("frontend-compile")) {
    fail("frontend-format must follow frontend-compile so clean runners do not compile the same dev dependencies concurrently");
  }

  for (const primitive of ["HARDEN_LLM_TEST_OFFLINE", "HARDEN_LLM_TEST_NETWORK", "SIGTERM", "SIGKILL", "container.id", "truncatedOutputBytes"]) {
    if (!runner.includes(primitive)) fail(`runner is missing ${primitive}`);
  }
  console.log(JSON.stringify({ accepted: true, manifest: path.relative(repositoryRoot, manifestPath), fastTaskCount: fastTasks.length }));
}

main().catch((error) => {
  console.error(error.stack ?? error.message);
  process.exitCode = 1;
});
