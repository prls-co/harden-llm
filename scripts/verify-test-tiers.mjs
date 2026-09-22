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
  ["make", "test-compose"],
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

  const runner = await fs.readFile(path.join(repositoryRoot, "scripts", "run-test-tier.mjs"), "utf8");
  for (const primitive of ["HARDEN_LLM_TEST_OFFLINE", "HARDEN_LLM_TEST_NETWORK", "SIGTERM", "SIGKILL", "container.id", "truncatedOutputBytes"]) {
    if (!runner.includes(primitive)) fail(`runner is missing ${primitive}`);
  }
  console.log(JSON.stringify({ accepted: true, manifest: path.relative(repositoryRoot, manifestPath), fastTaskCount: fastTasks.length }));
}

main().catch((error) => {
  console.error(error.stack ?? error.message);
  process.exitCode = 1;
});
