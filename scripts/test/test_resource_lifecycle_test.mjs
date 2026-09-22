// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-271 TEST-272 TEST-273 TEST-280

import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import { once } from "node:events";
import { watch } from "node:fs";
import { promises as fs } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

import { after, test } from "node:test";
import { createResourceReceipt, processStartIdentity, readHostBootID, readResourceReceipt, updateResourceReceipt, validateResourceReceipt } from "../test-resource-lifecycle.mjs";
import { runTasks } from "../run-test-tier.mjs";

const REPOSITORY_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const FLOCK_PATH = spawnSync("which", ["flock"], { encoding: "utf8" }).stdout.trim();
const FIXTURES = new Set();
// Bound IPC readiness within the existing five-second case window; this does
// not change daemon-lock waits, child task deadlines, or test assertions.
const PROCESS_READINESS_TIMEOUT_MS = 5_000;
const HARNESS_BIN = String.raw`#!/usr/bin/env node
import { appendFile, readFile, writeFile } from "node:fs/promises";
import { watch } from "node:fs";
import path from "node:path";

const [, , ...args] = process.argv;
const runId = process.env.HARDEN_LLM_TEST_RUN_ID ?? "unset";
const eventPath = process.env.HARDEN_LLM_FAKE_DOCKER_EVENTS;
const gatePath = process.env.HARDEN_LLM_FAKE_DOCKER_GATE;
const releasePath = process.env.HARDEN_LLM_FAKE_DOCKER_RELEASE;
const flag = (name) => { const index = args.indexOf(name); return index >= 0 ? args[index + 1] : null; };
let project = flag("-p") ?? flag("--project-name");
if (!project && process.env.HARDEN_LLM_TEST_RESOURCE_RECEIPT) {
  try { project = JSON.parse(await readFile(process.env.HARDEN_LLM_TEST_RESOURCE_RECEIPT, "utf8")).project; } catch { /* read errors are visible at the tested operation */ }
}
const event = async (kind, extra = {}) => appendFile(eventPath, JSON.stringify({ kind, runId, project, args, ...extra }) + "\n", { mode: 0o600 });
const waitForFile = async (filePath, timeoutMs = 5_000) => {
  const directory = path.dirname(filePath);
  const target = path.basename(filePath);
  if (await readFile(filePath, "utf8").catch(() => null) !== null) return;
  await new Promise((resolve, reject) => {
    let settled = false;
    const watcher = watch(directory, (eventType, filename) => {
      if (filename?.toString() !== target) return;
      void readFile(filePath, "utf8").then(() => finish()).catch((error) => { if (error.code !== "ENOENT") finish(error); });
    });
    const finish = (error = null) => {
      if (settled) return;
      settled = true;
      clearTimeout(timeout);
      watcher.close();
      if (error) reject(error);
      else resolve();
    };
    const timeout = setTimeout(() => finish(new Error("test gate expired")), timeoutMs);
    void readFile(filePath, "utf8").then(() => finish()).catch((error) => { if (error.code !== "ENOENT") finish(error); });
  });
};

if (args[0] === "context" && args.includes("inspect")) {
  process.stdout.write((process.env.HARDEN_LLM_FAKE_DOCKER_ENDPOINT ?? "unix:///var/run/docker.sock") + "\n");
} else if (args[0] === "info") {
  await event("daemon-info");
  process.stdout.write((process.env.HARDEN_LLM_FAKE_DOCKER_DAEMON_ID ?? "fixture-daemon-identity") + "\n");
} else if (args[0] === "compose" && args.includes("up")) {
  let receipt = null;
  const receiptPath = process.env.HARDEN_LLM_TEST_RESOURCE_RECEIPT;
  if (receiptPath) {
    try { receipt = JSON.parse(await readFile(receiptPath, "utf8")); } catch { /* recorded as missing below */ }
  }
  await event("create", { receipt });
  if (runId === process.env.HARDEN_LLM_FAKE_DOCKER_UP_FAILURE_RUN_ID) {
    process.stderr.write("injected partial startup failure\n");
    process.exitCode = 1;
  }
  if (runId === process.env.HARDEN_LLM_FAKE_DOCKER_BLOCK_RUN_ID) {
    await writeFile(gatePath, runId, { mode: 0o600 });
    await waitForFile(releasePath);
  }
} else if (args[0] === "compose" && args.includes("port")) {
  process.stdout.write("127.0.0.1:54321\n");
} else if (args[0] === "compose" && args.includes("down")) {
  let taskAlive = false;
  const taskPIDFile = process.env.HARDEN_LLM_FAKE_DOCKER_TASK_PID_FILE;
  if (taskPIDFile) {
    try { process.kill(Number(await readFile(taskPIDFile, "utf8")), 0); taskAlive = true; } catch { /* process group is gone */ }
  }
  await event("cleanup", { taskAlive });
  if (runId === process.env.HARDEN_LLM_FAKE_DOCKER_DOWN_FAILURE_RUN_ID) {
    process.stderr.write("injected cleanup failure\n");
    process.exitCode = 1;
  }
} else if (args[0] === "ps" && process.env.HARDEN_LLM_FAKE_DOCKER_HANG_INVENTORY_RUN_ID === runId) {
  await event("inventory-hang");
  await new Promise(() => {});
} else if (["ps", "volume", "network"].includes(args[0]) && (
  process.env.HARDEN_LLM_FAKE_DOCKER_UNKNOWN_RUN_ID === runId
  || (process.env.HARDEN_LLM_FAKE_DOCKER_UNKNOWN_AFTER_DOWN_RUN_ID === runId
    && (await readFile(eventPath, "utf8")).split("\n").filter(Boolean).some((line) => {
      const item = JSON.parse(line);
      return item.runId === runId && item.kind === "cleanup";
    }))
)) {
  await event("unknown-inventory");
  process.stderr.write("injected inventory failure\n");
  process.exitCode = 1;
} else if (args[0] === "ps" && args.some((argument) => argument.startsWith("volume=")) && runId === process.env.HARDEN_LLM_FAKE_DOCKER_FOREIGN_VOLUME_RUN_ID) {
  await event("volume-attachment-inspection");
  process.stdout.write("cafebabecafe\n");
} else if (args[0] === "ps" && args.some((argument) => argument.startsWith("label=")) && process.env.HARDEN_LLM_FAKE_DOCKER_STUBBORN_RUN_ID === runId) {
  await event("stubborn-leftover");
  process.stdout.write("0123456789abcdef\n");
} else if (args[0] === "ps" && process.env.HARDEN_LLM_FAKE_DOCKER_LEFTOVER_RUN_ID === runId) {
  const events = (await readFile(eventPath, "utf8")).split("\n").filter(Boolean).map((line) => JSON.parse(line));
  if (!events.some((item) => item.kind === "container-remove" && item.runId === runId)) {
    await event("leftover");
    process.stdout.write("0123456789abcdef\n");
  }
} else if (args[0] === "inspect") {
  const labels = {
    "com.docker.compose.project": args.at(-1) === "cafebabecafe" && (runId === process.env.HARDEN_LLM_FAKE_DOCKER_FOREIGN_VOLUME_RUN_ID || runId === process.env.HARDEN_LLM_FAKE_DOCKER_FOREIGN_NETWORK_RUN_ID)
      ? "sentinel-project"
      : project,
    "org.opencontainers.image.documentation": "https://example.test/docs",
  };
  process.stdout.write(JSON.stringify(labels) + "\n");
} else if (args[0] === "volume" && args.includes("ls") && runId === process.env.HARDEN_LLM_FAKE_DOCKER_FOREIGN_VOLUME_RUN_ID) {
  const events = (await readFile(eventPath, "utf8")).split("\n").filter(Boolean).map((line) => JSON.parse(line));
  if (!events.some((item) => item.kind === "volume-remove" && item.runId === runId)) process.stdout.write("test-owned-volume\n");
} else if (args[0] === "volume" && args.includes("inspect")) {
  process.stdout.write(JSON.stringify({ "com.docker.compose.project": project }) + "\n");
} else if (args[0] === "network" && args.includes("ls") && runId === process.env.HARDEN_LLM_FAKE_DOCKER_FOREIGN_NETWORK_RUN_ID) {
  const events = (await readFile(eventPath, "utf8")).split("\n").filter(Boolean).map((line) => JSON.parse(line));
  if (!events.some((item) => item.kind === "network-remove" && item.runId === runId)) process.stdout.write("fedcba987654\n");
} else if (args[0] === "network" && args.includes("inspect") && args.some((argument) => argument.includes(".Containers"))) {
  await event("network-attachment-inspection");
  const attached = runId === process.env.HARDEN_LLM_FAKE_DOCKER_FOREIGN_NETWORK_RUN_ID ? { cafebabecafe: { Name: "sentinel" } } : {};
  process.stdout.write(JSON.stringify(attached) + "\n");
} else if (args[0] === "network" && args.includes("inspect")) {
  process.stdout.write(JSON.stringify({ "com.docker.compose.project": project }) + "\n");
} else if (args[0] === "rm" && args.includes("-f")) {
  await event("container-remove", { resourceId: args.at(-1) });
} else if (args[0] === "volume" && args.includes("rm")) {
  await event("volume-remove", { resourceId: args.at(-1) });
} else if (args[0] === "network" && args.includes("rm")) {
  await event("network-remove", { resourceId: args.at(-1) });
} else {
  await event("other");
}
`;

const FLOCK_WRAPPER = String.raw`#!/usr/bin/env node
import { appendFile } from "node:fs/promises";
import { spawn } from "node:child_process";

await appendFile(process.env.HARDEN_LLM_FAKE_DOCKER_EVENTS, JSON.stringify({ kind: "flock-attempt", runId: process.env.HARDEN_LLM_TEST_RUN_ID, argv: process.argv.slice(2) }) + "\n", { mode: 0o600 });
const child = spawn(process.env.HARDEN_LLM_REAL_FLOCK, process.argv.slice(2), { stdio: "inherit", env: process.env });
child.once("error", (error) => { process.stderr.write(error.message); process.exitCode = 127; });
child.once("close", async (code) => {
  await appendFile(process.env.HARDEN_LLM_FAKE_DOCKER_EVENTS, JSON.stringify({ kind: "flock-release", runId: process.env.HARDEN_LLM_TEST_RUN_ID }) + "\n", { mode: 0o600 });
  process.exitCode = code ?? 1;
});
`;

const WORKER = String.raw`import { readFile } from "node:fs/promises";
const { runTasks } = await import(process.argv[2]);
const config = JSON.parse(await readFile(process.argv[3], "utf8"));
const task = {
  id: "receipt-service-" + config.runId,
  testIds: ["TEST-273-" + config.runId],
  tier: "T3",
  resourceClass: "service",
  command: [process.execPath, "-e", "process.exit(0)"],
  dependsOn: [],
  timeoutMs: 10_000,
  cleanupOwner: "runner-test",
  network: "local-only",
  credentialKeys: [],
  requiredFor: ["test"],
  pathSelectors: [],
  servicePool: { composeFile: config.composeFile, services: [{ name: "harden-postgres", port: 5432 }] }
};
const result = await runTasks([task], {
  root: config.root,
  runID: config.runId,
  runDirectory: config.runDirectory,
  environment: config.environment ?? {},
  daemonLockWaitMs: config.daemonLockWaitMs,
  resourceClasses: { service: { slots: 1, exclusive: false } }
});
process.stdout.write(JSON.stringify({ accepted: result.accepted, firstFailure: result.firstFailure, cleanupErrors: result.cleanupErrors, lifecycleTimings: result.lifecycleTimings }));
`;

function restoreEnvironment(previous) {
  for (const [key, value] of Object.entries(previous)) {
    if (value === undefined) delete process.env[key];
    else process.env[key] = value;
  }
}

async function fixture(t) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "harden-llm-resource-test-"));
  FIXTURES.add(root);
  t.after(async () => {
    await fs.rm(root, { recursive: true, force: true });
    FIXTURES.delete(root);
  });
  const bin = path.join(root, "bin");
  await fs.mkdir(bin, { mode: 0o700 });
  await fs.writeFile(path.join(bin, "docker"), HARNESS_BIN, { mode: 0o700 });
  if (FLOCK_PATH) await fs.writeFile(path.join(bin, "flock"), FLOCK_WRAPPER, { mode: 0o700 });
  const eventsPath = path.join(root, "docker-events.jsonl");
  const composeFile = path.join(root, "compose.test.yml");
  await fs.writeFile(eventsPath, "", { mode: 0o600 });
  await fs.writeFile(composeFile, "services: {}\n", { mode: 0o600 });
  return { root, bin, eventsPath, composeFile, gatePath: path.join(root, "first-up.ready"), releasePath: path.join(root, "first-up.release") };
}

function baseEnvironment(data, additions = {}) {
  return {
    PATH: `${data.bin}:${process.env.PATH}`,
    HARDEN_LLM_FAKE_DOCKER_EVENTS: data.eventsPath,
    HARDEN_LLM_FAKE_DOCKER_GATE: data.gatePath,
    HARDEN_LLM_FAKE_DOCKER_RELEASE: data.releasePath,
    HARDEN_LLM_REAL_FLOCK: FLOCK_PATH,
    ...additions,
  };
}

function serviceTask(data, runId, command = [process.execPath, "-e", "process.exit(0)"], timeoutMs = 10_000) {
  return {
    id: `service-${runId}`,
    testIds: [`TEST-271-${runId}`],
    tier: "T3",
    resourceClass: "service",
    command,
    dependsOn: [],
    timeoutMs,
    cleanupOwner: "runner-test",
    network: "local-only",
    credentialKeys: [],
    requiredFor: ["test"],
    pathSelectors: [],
    servicePool: { composeFile: data.composeFile, services: [{ name: "harden-postgres", port: 5432 }] },
  };
}

async function readEvents(data) {
  const contents = await fs.readFile(data.eventsPath, "utf8");
  return contents.trim() ? contents.trim().split("\n").map((line) => JSON.parse(line)) : [];
}

async function waitForEvent(data, predicate, timeoutMs = PROCESS_READINESS_TIMEOUT_MS) {
  return new Promise((resolve, reject) => {
    let settled = false;
    const watcher = watch(data.eventsPath, () => {
      void readEvents(data).then((events) => {
        const found = events.find(predicate);
        if (found && !settled) {
          settled = true;
          clearTimeout(timer);
          watcher.close();
          resolve(found);
        }
      }).catch(fail);
    });
    const fail = (error) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      watcher.close();
      reject(error);
    };
    const timer = setTimeout(() => fail(new Error(`timed out waiting for lifecycle event after ${timeoutMs} ms`)), timeoutMs);
    void readEvents(data).then((events) => {
      const found = events.find(predicate);
      if (found && !settled) {
        settled = true;
        clearTimeout(timer);
        watcher.close();
        resolve(found);
      }
    }).catch(fail);
  });
}

async function waitForFile(filePath, timeoutMs = PROCESS_READINESS_TIMEOUT_MS) {
  const directory = path.dirname(filePath);
  const target = path.basename(filePath);
  const existing = await fs.readFile(filePath, "utf8").catch(() => null);
  if (existing !== null) return existing;
  return new Promise((resolve, reject) => {
    let settled = false;
    const watcher = watch(directory, (eventType, filename) => {
      if (filename?.toString() !== target) return;
      void fs.readFile(filePath, "utf8").then((contents) => finish(null, contents)).catch((error) => {
        if (error.code !== "ENOENT") finish(error);
      });
    });
    const finish = (error, contents) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      watcher.close();
      if (error) reject(error);
      else resolve(contents);
    };
    const timer = setTimeout(() => finish(new Error(`timed out waiting for ${path.basename(filePath)}`)), timeoutMs);
    void fs.readFile(filePath, "utf8").then((contents) => finish(null, contents)).catch((error) => {
      if (error.code !== "ENOENT") finish(error);
    });
  });
}

async function runTask(data, runId, options = {}) {
  const previous = {
    PATH: process.env.PATH,
    HARDEN_LLM_TEST_RUN_ID: process.env.HARDEN_LLM_TEST_RUN_ID,
    HARDEN_LLM_FAKE_DOCKER_EVENTS: process.env.HARDEN_LLM_FAKE_DOCKER_EVENTS,
    HARDEN_LLM_FAKE_DOCKER_GATE: process.env.HARDEN_LLM_FAKE_DOCKER_GATE,
    HARDEN_LLM_FAKE_DOCKER_RELEASE: process.env.HARDEN_LLM_FAKE_DOCKER_RELEASE,
    HARDEN_LLM_FAKE_DOCKER_DOWN_FAILURE_RUN_ID: process.env.HARDEN_LLM_FAKE_DOCKER_DOWN_FAILURE_RUN_ID,
    HARDEN_LLM_FAKE_DOCKER_LEFTOVER_RUN_ID: process.env.HARDEN_LLM_FAKE_DOCKER_LEFTOVER_RUN_ID,
    HARDEN_LLM_FAKE_DOCKER_STUBBORN_RUN_ID: process.env.HARDEN_LLM_FAKE_DOCKER_STUBBORN_RUN_ID,
    HARDEN_LLM_FAKE_DOCKER_UNKNOWN_RUN_ID: process.env.HARDEN_LLM_FAKE_DOCKER_UNKNOWN_RUN_ID,
    HARDEN_LLM_FAKE_DOCKER_UNKNOWN_AFTER_DOWN_RUN_ID: process.env.HARDEN_LLM_FAKE_DOCKER_UNKNOWN_AFTER_DOWN_RUN_ID,
    HARDEN_LLM_FAKE_DOCKER_UP_FAILURE_RUN_ID: process.env.HARDEN_LLM_FAKE_DOCKER_UP_FAILURE_RUN_ID,
    HARDEN_LLM_FAKE_DOCKER_TASK_PID_FILE: process.env.HARDEN_LLM_FAKE_DOCKER_TASK_PID_FILE,
    HARDEN_LLM_FAKE_DOCKER_FOREIGN_VOLUME_RUN_ID: process.env.HARDEN_LLM_FAKE_DOCKER_FOREIGN_VOLUME_RUN_ID,
    HARDEN_LLM_FAKE_DOCKER_FOREIGN_NETWORK_RUN_ID: process.env.HARDEN_LLM_FAKE_DOCKER_FOREIGN_NETWORK_RUN_ID,
    HARDEN_LLM_FAKE_DOCKER_HANG_INVENTORY_RUN_ID: process.env.HARDEN_LLM_FAKE_DOCKER_HANG_INVENTORY_RUN_ID,
    HARDEN_LLM_FAKE_DOCKER_BLOCK_RUN_ID: process.env.HARDEN_LLM_FAKE_DOCKER_BLOCK_RUN_ID,
    HARDEN_LLM_FAKE_DOCKER_DAEMON_ID: process.env.HARDEN_LLM_FAKE_DOCKER_DAEMON_ID,
    HARDEN_LLM_TEST_RESOURCE_DIR: process.env.HARDEN_LLM_TEST_RESOURCE_DIR,
    HARDEN_LLM_TEST_SECRET: process.env.HARDEN_LLM_TEST_SECRET,
    HARDEN_LLM_REAL_FLOCK: process.env.HARDEN_LLM_REAL_FLOCK,
    HARDEN_LLM_FAKE_DOCKER_ENDPOINT: process.env.HARDEN_LLM_FAKE_DOCKER_ENDPOINT,
    DOCKER_HOST: process.env.DOCKER_HOST,
    DOCKER_CONTEXT: process.env.DOCKER_CONTEXT,
  };
  Object.assign(process.env, baseEnvironment(data, {
    HARDEN_LLM_TEST_RUN_ID: runId,
    HARDEN_LLM_TEST_RESOURCE_DIR: path.join(data.root, "receipts"),
    ...options.environment,
  }));
  try {
    let command = options.command;
    if (!command && options.additionalReceiptProjects?.length) {
      const moduleURL = pathToFileURL(path.join(REPOSITORY_ROOT, "scripts/test-resource-lifecycle.mjs")).href;
      const projects = JSON.stringify(options.additionalReceiptProjects);
      const composeFile = JSON.stringify(data.composeFile);
      const code = `const { createResourceReceipt, updateResourceReceipt } = await import(${JSON.stringify(moduleURL)}); for (const project of ${projects}) { const { receiptPath } = await createResourceReceipt({ directory: process.env.HARDEN_LLM_TEST_RESOURCE_DIR, runId: process.env.HARDEN_LLM_TEST_RUN_ID, project, daemonId: process.env.HARDEN_LLM_FAKE_DOCKER_DAEMON_ID ?? "fixture-daemon-identity", composeFiles: [${composeFile}], pid: Number(process.env.HARDEN_LLM_TEST_SUPERVISOR_PID), supervisorStart: process.env.HARDEN_LLM_TEST_SUPERVISOR_START }); await updateResourceReceipt(receiptPath, "creating"); await updateResourceReceipt(receiptPath, "running"); }`;
      command = [process.execPath, "-e", code];
    }
    return await runTasks([serviceTask(data, runId, command, options.timeoutMs)], {
      root: data.root,
      runID: runId,
      sourceSHA: options.sourceSHA,
      cleanupTimeoutMs: options.cleanupTimeoutMs,
      environment: options.environment,
      runDirectory: path.join(data.root, `run-${runId}`),
      resourceClasses: { service: { slots: 1, exclusive: false } },
    });
  } finally {
    restoreEnvironment(previous);
  }
}

function spawnWorker(data, runId, options = {}) {
  const configPath = path.join(data.root, `${runId}.json`);
  const workerPath = path.join(data.root, "resource-worker.mjs");
  const config = { root: data.root, runId, composeFile: data.composeFile, runDirectory: path.join(data.root, `run-${runId}`), environment: options.environment ?? {}, daemonLockWaitMs: options.daemonLockWaitMs };
  return Promise.all([
    fs.writeFile(configPath, JSON.stringify(config), { mode: 0o600 }),
    fs.writeFile(workerPath, WORKER, { mode: 0o600 }),
  ]).then(() => {
    const environment = baseEnvironment(data, {
      HARDEN_LLM_TEST_RUN_ID: runId,
      HARDEN_LLM_TEST_RESOURCE_DIR: path.join(data.root, "receipts"),
      ...(options.environment ?? {}),
    });
    return spawn(process.execPath, [workerPath, pathToFileURL(path.join(REPOSITORY_ROOT, "scripts/run-test-tier.mjs")).href, configPath], { cwd: REPOSITORY_ROOT, env: environment, stdio: ["ignore", "pipe", "pipe"] });
  });
}

function closeWorkers(workers) {
  for (const worker of workers) if (worker && worker.exitCode === null && worker.signalCode === null) worker.kill("SIGKILL");
}

test("TEST-271 registers a private durable receipt before service creation", async (t) => {
  const sharedVector = JSON.parse(await fs.readFile(path.join(REPOSITORY_ROOT, "internal/integrationtest/testdata/resource_receipt_valid.json"), "utf8"));
  assert.equal(validateResourceReceipt(sharedVector), sharedVector, "Node and Go consume the same receipt contract vector");
  const data = await fixture(t);
  const ledger = path.join(data.root, "receipts");
  const result = await runTask(data, "receipt-order", { environment: { HARDEN_LLM_TEST_RESOURCE_DIR: ledger, HARDEN_LLM_TEST_SECRET: "test-only-secret-value" } });
  assert.equal(result.accepted, true, JSON.stringify(result));
  const create = (await readEvents(data)).find((event) => event.kind === "create");
  assert.ok(create, "fake Compose observed the creation boundary");
  assert.ok(create.receipt, "a durable receipt must exist before Compose up");
  assert.equal(create.receipt.runId, "receipt-order");
  assert.equal(create.receipt.project, create.project);
  assert.equal(create.receipt.state, "creating");
  assert.equal(create.receipt.disposable, true);
  assert.equal(create.receipt.resourceIds.containers.length, 0);
  assert.equal(create.receipt.sourceSHA.length, 40);
  assert.ok(create.receipt.daemonId);
  assert.ok(create.receipt.hostBootId);
  assert.ok(create.receipt.supervisorStart);
  const runLedger = path.join(ledger, "receipt-order");
  const receiptFiles = await fs.readdir(runLedger);
  assert.equal(receiptFiles.length, 1);
  const receiptPath = path.join(runLedger, receiptFiles[0]);
  const stat = await fs.stat(receiptPath);
  assert.equal(stat.mode & 0o077, 0, "receipt permissions must be private");
  assert.equal(path.dirname(receiptPath) === result.runDirectory, false, "receipt must outlive disposable runner scratch");
  const persisted = await readResourceReceipt(receiptPath);
  assert.equal(persisted.state, "cleaned", "successful teardown advances the durable receipt");
  assert.deepEqual({ ...persisted, state: create.receipt.state }, create.receipt);
  assert.equal(await fs.stat(result.runDirectory).then(() => true, () => false), false, "runner scratch was removed");
  assert.equal((await fs.readFile(receiptPath, "utf8")).includes("test-only-secret-value"), false);
});

test("TEST-271 receipt validation or atomic-write failure prevents Docker mutation", async (t) => {
  const data = await fixture(t);
  const invalid = await runTask(data, "invalid-receipt", { sourceSHA: "not-a-commit" });
  assert.equal(invalid.accepted, false);
  assert.equal((await readEvents(data)).some((event) => event.kind === "create" && event.runId === "invalid-receipt"), false);

  const ledgerFile = path.join(data.root, "not-a-directory");
  await fs.writeFile(ledgerFile, "sentinel", { mode: 0o600 });
  const unwritable = await runTask(data, "unwritable-ledger", { environment: { HARDEN_LLM_TEST_RESOURCE_DIR: ledgerFile } });
  assert.equal(unwritable.accepted, false);
  assert.equal((await readEvents(data)).some((event) => event.kind === "create" && event.runId === "unwritable-ledger"), false);
});

test("TEST-272 cleanup failures fail acceptance", async (t) => {
  const data = await fixture(t);
  const recovered = await runTask(data, "cleanup-fallback", { environment: {
    HARDEN_LLM_FAKE_DOCKER_DOWN_FAILURE_RUN_ID: "cleanup-fallback",
    HARDEN_LLM_FAKE_DOCKER_LEFTOVER_RUN_ID: "cleanup-fallback",
  } });
  assert.equal(recovered.accepted, true, "exact fallback cleanup with an empty final inventory is accepted");
  assert.equal(recovered.results[0].cleanupError, null);
  assert.ok(recovered.cleanupWarnings.some((warning) => /Compose down failed.*exact project cleanup.*empty final inventory/i.test(warning)));
  assert.deepEqual(recovered.cleanupErrors, []);
  assert.equal(JSON.stringify(recovered).includes("https://example.test/docs"), false, "raw Docker label metadata must not leak into reports");

  const leftover = await runTask(data, "cleanup-leftover", { environment: {
    HARDEN_LLM_FAKE_DOCKER_DOWN_FAILURE_RUN_ID: "cleanup-leftover",
    HARDEN_LLM_FAKE_DOCKER_STUBBORN_RUN_ID: "cleanup-leftover",
  } });
  assert.equal(leftover.accepted, false, "resources that remain after exact-ID fallback cleanup are not accepted");
  assert.ok(leftover.results[0].cleanupError);
  assert.ok(leftover.cleanupErrors.some((error) => /cleanup-leftover|service pool/i.test(error)));
  assert.ok(leftover.cleanupWarnings.some((warning) => /Compose down failed/i.test(warning)));
});

test("TEST-272 cleanup reporting preserves the first task failure and Compose warning", async (t) => {
  const data = await fixture(t);
  const originalFailure = await runTask(data, "original-failure", {
    command: [process.execPath, "-e", "process.exit(17)"],
    environment: {
      HARDEN_LLM_FAKE_DOCKER_DOWN_FAILURE_RUN_ID: "original-failure",
      HARDEN_LLM_FAKE_DOCKER_LEFTOVER_RUN_ID: "original-failure",
      HARDEN_LLM_FAKE_DOCKER_UNKNOWN_AFTER_DOWN_RUN_ID: "original-failure",
    },
  });
  assert.equal(originalFailure.accepted, false);
  assert.equal(originalFailure.firstFailure.status, 17);
  assert.ok(originalFailure.cleanupErrors.length > 0, "cleanup uncertainty is reported separately");
  assert.ok(originalFailure.cleanupWarnings.some((warning) => /Compose down failed.*could not be verified/i.test(warning)), "a graceful-down error remains visible when fallback verification is inconclusive");
});

test("TEST-272 parent waits for the timed-out process group before cleanup", async (t) => {
  const data = await fixture(t);
  const pidFile = path.join(data.root, "timed-out-child.pid");
  const command = [process.execPath, "-e", `const fs = require("node:fs"); fs.writeFileSync(${JSON.stringify(pidFile)}, String(process.pid)); process.on("SIGTERM", () => {}); setInterval(() => {}, 1000);`];
  const result = await runTask(data, "timeout-child", {
    command,
    timeoutMs: 100,
    environment: {
      HARDEN_LLM_FAKE_DOCKER_TASK_PID_FILE: pidFile,
      HARDEN_LLM_FAKE_DOCKER_LEFTOVER_RUN_ID: "timeout-child",
    },
  });
  assert.equal(result.accepted, false);
  assert.equal(result.results[0].timedOut, true);
  const cleanup = (await readEvents(data)).find((event) => event.kind === "cleanup");
  assert.ok(cleanup);
  assert.equal(cleanup.taskAlive, false, "cleanup starts only after the owned child process has exited");
});

test("TEST-272 preserves volumes and networks attached to foreign containers", async (t) => {
  const data = await fixture(t);
  const runId = "foreign-attachments";
  const result = await runTask(data, runId, {
    environment: {
      HARDEN_LLM_FAKE_DOCKER_FOREIGN_VOLUME_RUN_ID: runId,
      HARDEN_LLM_FAKE_DOCKER_FOREIGN_NETWORK_RUN_ID: runId,
    },
  });
  assert.equal(result.accepted, false);
  const events = await readEvents(data);
  assert.equal(events.some((event) => event.kind === "volume-remove"), false, "foreign-attached volume must not be removed");
  assert.equal(events.some((event) => event.kind === "network-remove"), false, "foreign-attached network must not be removed");
  const cleanupIndex = events.findIndex((event) => event.kind === "cleanup");
  assert.ok(events.findIndex((event) => event.kind === "volume-attachment-inspection") < cleanupIndex, "volume attachments are inspected before teardown");
  assert.ok(events.findIndex((event) => event.kind === "network-attachment-inspection") < cleanupIndex, "network attachments are inspected before teardown");
  const receiptDirectory = path.join(data.root, "receipts", runId);
  const [receiptName] = await fs.readdir(receiptDirectory);
  assert.equal((await readResourceReceipt(path.join(receiptDirectory, receiptName))).state, "cleanup-pending");
});

test("TEST-272 bounds hung inventory and retains a cleanup-pending receipt", async (t) => {
  const data = await fixture(t);
  const result = await runTask(data, "hung-inventory", {
    cleanupTimeoutMs: 3_000,
    environment: { HARDEN_LLM_FAKE_DOCKER_HANG_INVENTORY_RUN_ID: "hung-inventory" },
  });
  assert.equal(result.accepted, false);
  assert.match(result.results[0].cleanupError, /inventory|budget|timed out/i);
  const runLedger = path.join(data.root, "receipts", "hung-inventory");
  const [receiptName] = await fs.readdir(runLedger);
  assert.equal((await readResourceReceipt(path.join(runLedger, receiptName))).state, "cleanup-pending");
});

test("TEST-272 parent reconciles Go fixture receipts in the same run ledger", async (t) => {
  const data = await fixture(t);
  const project = "harden-llm-smoke-nested-test";
  const result = await runTask(data, "nested-receipt-run", { additionalReceiptProjects: [project] });
  assert.equal(result.accepted, true, JSON.stringify(result));
  const nestedReceiptPath = path.join(data.root, "receipts", "nested-receipt-run", `resource-${project}.json`);
  assert.equal((await readResourceReceipt(nestedReceiptPath)).state, "cleaned");
});

test("TEST-273 same-daemon runners serialize and leave exact crash receipts recoverable", async (t) => {
  if (process.platform !== "linux" || !FLOCK_PATH) return t.skip("Linux util-linux flock is required for this repository host");
  const data = await fixture(t);
  const workers = [];
  t.after(() => closeWorkers(workers));
  const first = await spawnWorker(data, "first", { environment: { HARDEN_LLM_FAKE_DOCKER_BLOCK_RUN_ID: "first" } });
  workers.push(first);
  const firstExit = once(first, "exit");
  const firstOutput = [];
  first.stdout.on("data", (chunk) => firstOutput.push(chunk));
  first.stderr.on("data", (chunk) => firstOutput.push(chunk));
  const firstUp = await waitForEvent(data, (event) => event.kind === "create" && event.runId === "first");
  assert.ok(firstUp.project);
  await waitForFile(data.gatePath);

  const second = await spawnWorker(data, "second");
  workers.push(second);
  const secondExit = once(second, "exit");
  const secondOutput = [];
  second.stdout.on("data", (chunk) => secondOutput.push(chunk));
  second.stderr.on("data", (chunk) => secondOutput.push(chunk));
  await waitForEvent(data, (event) => event.kind === "flock-attempt" && event.runId === "second");
  assert.equal((await readEvents(data)).some((event) => event.kind === "create" && event.runId === "second"), false, "second runner must wait while the first owns the daemon lock");

  await fs.writeFile(data.releasePath, "release first", { mode: 0o600 });
  await Promise.all([firstExit, secondExit]);
  const events = await readEvents(data);
  assert.ok(events.findIndex((event) => event.kind === "flock-release" && event.runId === "first") < events.findIndex((event) => event.kind === "create" && event.runId === "second"));
  assert.ok(Buffer.concat(firstOutput).includes(Buffer.from('"accepted":true')), Buffer.concat(firstOutput).toString());
  assert.ok(Buffer.concat(secondOutput).includes(Buffer.from('"accepted":true')), Buffer.concat(secondOutput).toString());
  assert.equal(Number.isFinite(JSON.parse(Buffer.concat(secondOutput).toString()).lifecycleTimings.daemonLockWaitMs), true, "runner report retains measured daemon-lock wait separately from child timeout");
});

test("TEST-273 different daemon identities use separate locks and do not block", async (t) => {
  if (process.platform !== "linux" || !FLOCK_PATH) return t.skip("Linux util-linux flock is required for this repository host");
  const data = await fixture(t);
  const workers = [];
  t.after(() => closeWorkers(workers));
  const first = await spawnWorker(data, "daemon-a", { environment: {
    HARDEN_LLM_FAKE_DOCKER_DAEMON_ID: "fixture-daemon-a",
    HARDEN_LLM_FAKE_DOCKER_BLOCK_RUN_ID: "daemon-a",
  } });
  workers.push(first);
  const firstExit = once(first, "exit");
  await waitForFile(data.gatePath);
  const second = await spawnWorker(data, "daemon-b", { environment: { HARDEN_LLM_FAKE_DOCKER_DAEMON_ID: "fixture-daemon-b" } });
  workers.push(second);
  const secondExit = once(second, "exit");
  // This readiness bound includes cold Node/`flock` startup while the fast
  // selector may be running other independent tasks. The ordering assertion
  // below remains the oracle: daemon-b must create before daemon-a is released.
  await waitForEvent(data, (event) => event.kind === "create" && event.runId === "daemon-b", 5_000);
  await fs.writeFile(data.releasePath, "release daemon-a", { mode: 0o600 });
  await Promise.all([firstExit, secondExit]);
  const events = await readEvents(data);
  const firstLock = events.find((event) => event.kind === "flock-attempt" && event.runId === "daemon-a");
  const secondLock = events.find((event) => event.kind === "flock-attempt" && event.runId === "daemon-b");
  assert.ok(firstLock && secondLock, "both Docker selections must acquire a daemon guard");
  const firstLockPath = firstLock.argv.find((argument) => argument.endsWith(".lock"));
  const secondLockPath = secondLock.argv.find((argument) => argument.endsWith(".lock"));
  assert.ok(firstLockPath && secondLockPath, "flock must receive the daemon lock files");
  assert.notEqual(firstLockPath, secondLockPath, "different daemon IDs map to different lock files");
  assert.ok(events.findIndex((event) => event.kind === "create" && event.runId === "daemon-b") < events.findIndex((event) => event.kind === "flock-release" && event.runId === "daemon-a"), JSON.stringify(events));
});

test("TEST-273 lock wait is bounded and timeout prevents Docker creation", async (t) => {
  if (process.platform !== "linux" || !FLOCK_PATH) return t.skip("Linux util-linux flock is required for this repository host");
  const data = await fixture(t);
  const workers = [];
  t.after(() => closeWorkers(workers));
  const first = await spawnWorker(data, "lock-holder", { environment: { HARDEN_LLM_FAKE_DOCKER_BLOCK_RUN_ID: "lock-holder" } });
  workers.push(first);
  const firstExit = once(first, "exit");
  await waitForFile(data.gatePath);
  const second = await spawnWorker(data, "lock-timeout", { daemonLockWaitMs: 1_000 });
  workers.push(second);
  const secondExit = once(second, "exit");
  const secondOutput = [];
  second.stdout.on("data", (chunk) => secondOutput.push(chunk));
  second.stderr.on("data", (chunk) => secondOutput.push(chunk));
  await secondExit;
  assert.match(Buffer.concat(secondOutput).toString(), /"accepted":false/);
  assert.match(Buffer.concat(secondOutput).toString(), /lock|wait|timeout/i);
  assert.equal((await readEvents(data)).some((event) => event.kind === "create" && event.runId === "lock-timeout"), false);
  await fs.writeFile(data.releasePath, "release holder", { mode: 0o600 });
  await firstExit;
});

async function seedReceipt(data, { runId, project, pid, supervisorStart, hostBootId }) {
  const { receiptPath } = await createResourceReceipt({
    directory: path.join(data.root, "receipts"),
    runId,
    project,
    daemonId: "fixture-daemon-identity",
    composeFiles: [data.composeFile],
    pid,
    supervisorStart,
    hostBootId,
  });
  await updateResourceReceipt(receiptPath, "creating");
  await updateResourceReceipt(receiptPath, "running");
  return receiptPath;
}

test("TEST-273 active receipt owner blocks recovery without mutating its record", async (t) => {
  const data = await fixture(t);
  const receiptPath = await seedReceipt(data, {
    runId: "active-owner",
    project: "harden-llm-active-owner",
    pid: process.pid,
    supervisorStart: await processStartIdentity(process.pid),
    hostBootId: await readHostBootID(),
  });
  const result = await runTask(data, "blocked-by-active-owner");
  assert.equal(result.accepted, false);
  assert.match(result.preflightFailure, /active|owned|receipt/i);
  assert.equal((await readResourceReceipt(receiptPath)).state, "running");
  assert.equal((await readEvents(data)).some((event) => event.kind === "create" && event.runId === "blocked-by-active-owner"), false);
});

test("TEST-273 dead, reused-PID, and prior-boot owners are reconciled only with identity proof", async (t) => {
  const data = await fixture(t);
  const bootID = await readHostBootID();
  const currentStart = await processStartIdentity(process.pid);
  const receiptPaths = await Promise.all([
    seedReceipt(data, {
      runId: "dead-owner",
      project: "harden-llm-dead-owner",
      pid: 2_000_000_000,
      supervisorStart: "123456789",
      hostBootId: bootID,
    }),
    seedReceipt(data, {
      runId: "reused-owner",
      project: "harden-llm-reused-owner",
      pid: process.pid,
      supervisorStart: "1",
      hostBootId: bootID,
    }),
    seedReceipt(data, {
      runId: "prior-boot-owner",
      project: "harden-llm-prior-boot-owner",
      pid: process.pid,
      supervisorStart: currentStart,
      hostBootId: "prior-host-boot",
    }),
  ]);
  const result = await runTask(data, "recover-stale-identities");
  assert.equal(result.accepted, true, JSON.stringify(result));
  for (const receiptPath of receiptPaths) assert.equal((await readResourceReceipt(receiptPath)).state, "cleaned");
});

test("TEST-273 corrupt receipt is preserved and blocks ambiguous Docker recovery", async (t) => {
  const data = await fixture(t);
  const receiptDirectory = path.join(data.root, "receipts", "corrupt-owner");
  await fs.mkdir(receiptDirectory, { recursive: true, mode: 0o700 });
  const receiptPath = path.join(receiptDirectory, "resource-harden-llm-corrupt-owner.json");
  const contents = "{not valid json\n";
  await fs.writeFile(receiptPath, contents, { mode: 0o600 });
  const result = await runTask(data, "blocked-by-corrupt-receipt");
  assert.equal(result.accepted, false);
  assert.match(result.preflightFailure, /corrupt|validate|receipt/i);
  assert.equal(await fs.readFile(receiptPath, "utf8"), contents);
  assert.equal((await readEvents(data)).some((event) => event.kind === "create" && event.runId === "blocked-by-corrupt-receipt"), false);
});

test("TEST-273 remote Docker endpoint is rejected before lock or mutation", async (t) => {
  const data = await fixture(t);
  const result = await runTask(data, "remote-endpoint", { environment: { DOCKER_HOST: "tcp://remote.example:2376" } });
  assert.equal(result.accepted, false);
  assert.match(result.preflightFailure, /local Unix socket|remote/i);
  const events = await readEvents(data);
  assert.equal(events.some((event) => event.kind === "flock-attempt"), false);
  assert.equal(events.some((event) => event.kind === "create"), false);
});

test("TEST-273 pure task selection does not contact Docker or acquire the lock", async (t) => {
  const data = await fixture(t);
  const result = await runTasks([{
    id: "pure-node",
    testIds: ["TEST-273-pure"],
    tier: "T1",
    resourceClass: "cpu",
    command: [process.execPath, "-e", "process.exit(0)"],
    dependsOn: [],
    timeoutMs: 5_000,
    cleanupOwner: "runner",
    network: "forbidden",
    credentialKeys: [],
    requiredFor: ["test"],
    pathSelectors: [],
  }], {
    root: data.root,
    runID: "pure-selection",
    runDirectory: path.join(data.root, "pure-run"),
    resourceClasses: { cpu: { slots: 1, exclusive: false } },
  });
  assert.equal(result.accepted, true, JSON.stringify(result));
  assert.deepEqual(result.lifecycleTimings, { dockerIdentityMs: null, daemonLockWaitMs: null, staleReceiptRecoveryMs: null });
  assert.equal((await readEvents(data)).some((event) => event.kind === "flock-attempt" || event.kind === "daemon-info"), false);
});

test("TEST-273 nested managed runner reuses its parent's daemon lease", async (t) => {
  if (process.platform !== "linux" || !FLOCK_PATH) return t.skip("Linux util-linux flock is required for this repository host");
  const data = await fixture(t);
  const code = `const { runTasks } = await import(${JSON.stringify(pathToFileURL(path.join(REPOSITORY_ROOT, "scripts/run-test-tier.mjs")).href)}); const task = { id: "nested-service", testIds: ["TEST-273-nested"], tier: "T3", resourceClass: "service", command: [process.execPath, "-e", "process.exit(0)"], dependsOn: [], timeoutMs: 10000, cleanupOwner: "test", network: "local-only", credentialKeys: [], requiredFor: ["test"], pathSelectors: [], servicePool: { composeFile: ${JSON.stringify(data.composeFile)}, services: [{name:"harden-postgres",port:5432}] } }; const result = await runTasks([task], { root: ${JSON.stringify(data.root)}, runID: "nested-child", runDirectory: ${JSON.stringify(path.join(data.root, "nested-child-run"))}, resourceClasses: { service: { slots: 1, exclusive: false } } }); if (!result.accepted) process.exitCode = 1;`;
  const result = await runTask(data, "nested-lease", { command: [process.execPath, "-e", code] });
  assert.equal(result.accepted, true, JSON.stringify(result));
  const events = await readEvents(data);
  assert.equal(events.filter((event) => event.kind === "flock-attempt" && event.runId === "nested-lease").length, 1, "the nested run must reuse the parent's lease");
  assert.equal(events.filter((event) => event.kind === "create").length, 2, "outer and nested pools both start under one daemon guard");
});

after(async () => {
  for (const root of FIXTURES) await fs.rm(root, { recursive: true, force: true });
});
