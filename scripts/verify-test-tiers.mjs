#!/usr/bin/env node

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
  ["make", "test-integration"],
  ["make", "test-integration-race"],
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
];

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
  const verifyLine = "verify: format lint build test-static test-unit test-parity test-integration test-integration-race test-api test-observability test-race test-vulnerability";
  if (!makefile.includes(verifyLine)) fail("make verify dependency contract changed");

  for (const [target, selector] of Object.entries(requiredTargets)) {
    const body = targetBody(makefile, target);
    if (!body) fail(`missing Make target ${target}`);
    const expected = `$(NODE) scripts/run-test-tier.mjs --task ${selector}`;
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
  if (fastTasks.length === 0) fail("fast selection is empty");
  for (const task of fastTasks) assertCheapTask(task);

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
