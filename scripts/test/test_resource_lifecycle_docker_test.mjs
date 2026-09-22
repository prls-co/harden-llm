// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-274
// This integration boundary must only run through the managed tier runner.

import assert from "node:assert/strict";
import { fork, spawn } from "node:child_process";
import { randomBytes } from "node:crypto";
import { promises as fs } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { performance } from "node:perf_hooks";

import { createResourceReceipt, defaultResourceDirectory, inheritedDaemonLockLease, readResourceReceipt, updateResourceReceipt } from "../test-resource-lifecycle.mjs";
import { runTasks } from "../run-test-tier.mjs";

const REPOSITORY_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const FIXTURE_IMAGE = "alpine@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc";
const MAX_OUTPUT_BYTES = 32 * 1024;
const DOCKER_COMMAND_TIMEOUT_MS = 20_000;
const SUPERVISOR_READY_TIMEOUT_MS = 35_000;

function capture(stream, state) {
  stream.on("data", (chunk) => {
    state.bytes += chunk.length;
    if (state.value.length < MAX_OUTPUT_BYTES) {
      state.value += chunk.toString("utf8").slice(0, MAX_OUTPUT_BYTES - state.value.length);
    }
  });
}

function runProcess(executable, args, timeoutMs = DOCKER_COMMAND_TIMEOUT_MS) {
  return new Promise((resolve) => {
    const child = spawn(executable, args, {
      cwd: REPOSITORY_ROOT,
      env: process.env,
      stdio: ["ignore", "pipe", "pipe"],
    });
    const stdout = { value: "", bytes: 0 };
    const stderr = { value: "", bytes: 0 };
    capture(child.stdout, stdout);
    capture(child.stderr, stderr);
    let timedOut = false;
    let killTimer = null;
    const timer = setTimeout(() => {
      timedOut = true;
      child.kill("SIGTERM");
      killTimer = setTimeout(() => child.kill("SIGKILL"), 1_500);
      killTimer.unref();
    }, timeoutMs);
    let spawnError = null;
    child.once("error", (error) => { spawnError = error; });
    child.once("close", (code, signal) => {
      clearTimeout(timer);
      if (killTimer) clearTimeout(killTimer);
      resolve({ code, signal, timedOut, spawnError, stdout: stdout.value, stderr: stderr.value, truncated: stdout.bytes + stderr.bytes > MAX_OUTPUT_BYTES });
    });
  });
}

function commandFailure(label, result) {
  const detail = `${result.stderr}\n${result.stdout}`.trim().slice(-1_200);
  return new Error(`${label} failed${result.timedOut ? " after its command deadline" : ""}${result.signal ? ` (signal ${result.signal})` : ` (exit ${result.code})`}${detail ? `: ${detail}` : ""}`);
}

async function docker(args, timeoutMs = DOCKER_COMMAND_TIMEOUT_MS) {
  const result = await runProcess("docker", args, timeoutMs);
  if (result.spawnError || result.code !== 0 || result.timedOut || result.truncated) {
    throw commandFailure(`docker ${args[0]}`, result);
  }
  return result.stdout.trim();
}

async function compose(project, composeFile, args) {
  return docker(["compose", "--project-name", project, "-f", composeFile, ...args]);
}

function parseLines(value) {
  return value.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
}

async function projectInventory(project) {
  const filter = `label=com.docker.compose.project=${project}`;
  const [containers, volumes, networks] = await Promise.all([
    docker(["ps", "-aq", "--filter", filter]),
    docker(["volume", "ls", "-q", "--filter", filter]),
    docker(["network", "ls", "-q", "--filter", filter]),
  ]);
  return { containers: parseLines(containers), volumes: parseLines(volumes), networks: parseLines(networks) };
}

function resourceCount(inventory) {
  return inventory.containers.length + inventory.volumes.length + inventory.networks.length;
}

function assertProjectHasResources(inventory, label) {
  assert.ok(inventory.containers.length > 0, `${label}: expected an owned container`);
  assert.ok(inventory.volumes.length > 0, `${label}: expected an owned named volume`);
  assert.ok(inventory.networks.length > 0, `${label}: expected an owned Compose network`);
}

async function assertProjectEmpty(project, label) {
  const inventory = await projectInventory(project);
  assert.equal(resourceCount(inventory), 0, `${label}: project ${project} still has resources: ${JSON.stringify(inventory)}`);
}

async function createComposeFile(directory) {
  const composeFile = path.join(directory, "resource-lifecycle.compose.yml");
  const contents = [
    "services:",
    "  sleeper:",
    `    image: ${FIXTURE_IMAGE}`,
    "    pull_policy: never",
    "    command: [\"sh\", \"-c\", \"trap 'exit 0' TERM; sleep 600 & wait\"]",
    "    volumes:",
    "      - owned-data:/owned",
    "volumes:",
    "  owned-data: {}",
    "",
  ].join("\n");
  await fs.writeFile(composeFile, contents, { encoding: "utf8", mode: 0o600, flag: "wx" });
  return composeFile;
}

async function registerProject({ resourceDirectory, runId, project, daemonId, composeFile }) {
  const { receiptPath } = await createResourceReceipt({
    directory: resourceDirectory,
    runId,
    project,
    daemonId,
    composeFiles: [composeFile],
  });
  await updateResourceReceipt(receiptPath, "creating");
  return receiptPath;
}

async function sendIPC(message) {
  await new Promise((resolve, reject) => {
    if (!process.connected) return reject(new Error("supervisor IPC channel is closed"));
    process.send(message, (error) => error ? reject(error) : resolve());
  });
}

async function waitForIPC() {
  return new Promise((resolve) => process.once("message", resolve));
}

async function runSupervisor() {
  const [, , , scenario, project, composeFile] = process.argv;
  const runId = process.env.HARDEN_LLM_TEST_RUN_ID;
  const resourceDirectory = process.env.HARDEN_LLM_TEST_RESOURCE_DIR;
  const daemonId = process.env.HARDEN_LLM_TEST_DAEMON_LOCK_DAEMON_ID;
  assert.ok(runId && resourceDirectory && daemonId, "managed runner identity is required before fixture creation");
  const receiptPath = await registerProject({ resourceDirectory, runId, project, daemonId, composeFile });
  if (scenario === "partial-create") {
    await compose(project, composeFile, ["create", "--pull", "never"]);
    await sendIPC({ type: "created", receiptPath });
    process.disconnect();
    process.exitCode = 23;
    return;
  }
  await compose(project, composeFile, ["up", "-d", "--pull", "never"]);
  await updateResourceReceipt(receiptPath, "running");
  await sendIPC({ type: "ready", receiptPath });
  const message = await waitForIPC();
  if (scenario === "success") {
    if (message?.type !== "cleanup") throw new Error("successful supervisor expected the explicit cleanup IPC");
    await updateResourceReceipt(receiptPath, "cleaning");
    await compose(project, composeFile, ["down", "--remove-orphans", "--volumes", "--timeout", "5"]);
    await updateResourceReceipt(receiptPath, "cleaned");
    await sendIPC({ type: "cleaned", receiptPath });
    process.disconnect();
    return;
  }
  // TERM and SIGKILL scenarios intentionally leave a live project and receipt.
  // The controller terminates this process only after observing `ready`.
  throw new Error(`unexpected control message for ${scenario}`);
}

function startSupervisor(scenario, project, composeFile) {
  const child = fork(fileURLToPath(import.meta.url), ["--supervisor", scenario, project, composeFile], {
    cwd: REPOSITORY_ROOT,
    env: process.env,
    stdio: ["ignore", "pipe", "pipe", "ipc"],
  });
  const stdout = { value: "", bytes: 0 };
  const stderr = { value: "", bytes: 0 };
  capture(child.stdout, stdout);
  capture(child.stderr, stderr);
  const closed = new Promise((resolve) => child.once("close", (code, signal) => resolve({ code, signal })));
  const message = new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`supervisor ${project} did not report readiness within ${SUPERVISOR_READY_TIMEOUT_MS} ms`)), SUPERVISOR_READY_TIMEOUT_MS);
    child.on("message", (value) => {
      if (value?.type === "ready" || value?.type === "created" || value?.type === "cleaned") {
        clearTimeout(timer);
        resolve(value);
      } else if (value?.type === "error") {
        clearTimeout(timer);
        reject(new Error(`supervisor ${project} failed: ${value.message}`));
      }
    });
    child.once("close", (code, signal) => {
      clearTimeout(timer);
      reject(new Error(`supervisor ${project} exited before readiness (exit ${code}, signal ${signal}): ${stderr.value.slice(-1_200)}`));
    });
  });
  return { child, closed, message, stdout, stderr, project };
}

async function waitForClose(supervisor, timeoutMs = 20_000) {
  let timer;
  try {
    return await Promise.race([
      supervisor.closed,
      new Promise((_, reject) => { timer = setTimeout(() => reject(new Error(`supervisor ${supervisor.project} did not exit within ${timeoutMs} ms`)), timeoutMs); }),
    ]);
  } finally {
    clearTimeout(timer);
  }
}

async function stopSupervisor(supervisor) {
  if (supervisor.child.exitCode !== null || supervisor.child.signalCode !== null) return;
  supervisor.child.kill("SIGTERM");
  try { await waitForClose(supervisor, 2_000); }
  catch {
    supervisor.child.kill("SIGKILL");
    await waitForClose(supervisor, 2_000).catch(() => {});
  }
}

async function assertSentinelAlive(sentinel) {
  const receipt = await readResourceReceipt(sentinel.receiptPath);
  assert.equal(receipt.state, "running", "sentinel receipt must remain live through target recovery");
  assertProjectHasResources(await projectInventory(sentinel.project), "sentinel preservation");
}

function syntheticRecoveryTask() {
  return {
    id: "test-274-next-owner-recovery",
    testIds: ["TEST-274"],
    tier: "T3",
    resourceClass: "docker-exclusive",
    command: [process.execPath, "-e", "process.exit(0)"],
    dependsOn: [],
    timeoutMs: 10_000,
    cleanupOwner: "runner",
    network: "local-only",
    credentialKeys: [],
    requiredFor: [],
    pathSelectors: [],
    requiresDocker: true,
  };
}

async function reconcileWithNextManagedOwner({ root, resourceDirectory, runId, temporaryDirectory }) {
  const report = await runTasks([syntheticRecoveryTask()], {
    root,
    resourceDirectory,
    runID: runId,
    runDirectory: path.join(temporaryDirectory, `reconcile-${randomBytes(6).toString("hex")}`),
    environment: { ...process.env },
    resourceClasses: { "docker-exclusive": { slots: 1, exclusive: true } },
  });
  assert.equal(report.accepted, true, `next managed owner failed to recover receipts: ${JSON.stringify({ firstFailure: report.firstFailure, cleanupErrors: report.cleanupErrors })}`);
  assert.deepEqual(report.cleanupErrors, []);
  return report;
}

async function runFixtureSupervisorCase({ scenario, resourceDirectory, runId, composeFile, temporaryDirectory, sentinel, trackedProjects }) {
  const project = `harden-llm-test-${randomBytes(6).toString("hex")}`;
  const receiptPath = path.join(resourceDirectory, runId, `resource-${project}.json`);
  const entry = { project, runId, receiptPath, scenario };
  trackedProjects.push(entry);
  const startedAt = performance.now();
  const supervisor = startSupervisor(scenario, project, composeFile);
  entry.supervisor = supervisor;
  const ready = await supervisor.message;
  assert.equal(ready.receiptPath, receiptPath, `${scenario}: supervisor receipt path must bind to this exact run/project`);
  const before = await projectInventory(project);
  assertProjectHasResources(before, `${scenario} before recovery`);
  const configuredCommand = await docker(["inspect", "--format", "{{json .Config.Cmd}}", before.containers[0]]);
  assert.deepEqual(JSON.parse(configuredCommand), ["sh", "-c", "trap 'exit 0' TERM; sleep 600 & wait"], `${scenario}: fixture PID 1 must explicitly handle TERM before recovery`);
  await assertSentinelAlive(sentinel);

  if (scenario === "success") {
    supervisor.child.send({ type: "cleanup" });
    const closed = await waitForClose(supervisor);
    assert.equal(closed.code, 0, `successful fixture supervisor failed: ${supervisor.stderr.value}`);
    const receipt = await readResourceReceipt(receiptPath);
    assert.equal(receipt.state, "cleaned");
    await assertProjectEmpty(project, "successful child cleanup");
  } else {
    if (scenario === "term") supervisor.child.kill("SIGTERM");
    else if (scenario === "supervisor-sigkill") supervisor.child.kill("SIGKILL");
    const closed = await waitForClose(supervisor);
    if (scenario === "partial-create") assert.equal(closed.code, 23, "partial create must preserve the fixture's intentional failure status");
    if (scenario === "term") assert.equal(closed.signal, "SIGTERM");
    if (scenario === "supervisor-sigkill") assert.equal(closed.signal, "SIGKILL");
    const beforeRecovery = await readResourceReceipt(receiptPath);
    assert.equal(beforeRecovery.state, scenario === "partial-create" ? "creating" : "running");
    entry.recoveryAttempted = true;
    let recoveryReport;
    try {
      recoveryReport = await reconcileWithNextManagedOwner({ root: REPOSITORY_ROOT, resourceDirectory, runId, temporaryDirectory });
    } catch (error) {
      entry.recoveryFailed = true;
      throw error;
    }
    entry.cleanupWarnings = recoveryReport.cleanupWarnings;
    const afterRecovery = await readResourceReceipt(receiptPath);
    assert.equal(afterRecovery.state, "cleaned", `${scenario}: next managed owner must complete receipt reconciliation`);
    await assertProjectEmpty(project, `${scenario} next-owner recovery`);
    await assertSentinelAlive(sentinel);
  }
  entry.elapsedMs = Math.round(performance.now() - startedAt);
  return {
    scenario,
    project,
    elapsedMs: entry.elapsedMs,
    finalState: (await readResourceReceipt(receiptPath)).state,
    cleanupWarnings: entry.cleanupWarnings ?? [],
  };
}

function createSentinel({ resourceDirectory, runId, daemonId, composeFile }) {
  const nonce = randomBytes(8).toString("hex");
  return {
    project: `harden-llm-test-${nonce}`,
    runId: `${runId}-sentinel-${nonce}`,
    resourceDirectory,
    daemonId,
    composeFile,
  };
}

async function startSentinel(sentinel) {
  sentinel.receiptPath = await registerProject({
    resourceDirectory: sentinel.resourceDirectory,
    runId: sentinel.runId,
    project: sentinel.project,
    daemonId: sentinel.daemonId,
    composeFile: sentinel.composeFile,
  });
  await compose(sentinel.project, sentinel.composeFile, ["up", "-d", "--pull", "never"]);
  await updateResourceReceipt(sentinel.receiptPath, "running");
  return sentinel;
}

async function cleanSentinel(sentinel) {
  if (!sentinel?.receiptPath) return;
  let receipt;
  try { receipt = await readResourceReceipt(sentinel.receiptPath); }
  catch (error) {
    if (error.code === "ENOENT") return;
    throw error;
  }
  if (receipt.state === "cleaned" && resourceCount(await projectInventory(sentinel.project)) === 0) return;
  if (receipt.state !== "cleaning") await updateResourceReceipt(sentinel.receiptPath, "cleaning");
  try {
    await compose(sentinel.project, receipt.composeFiles[0], ["down", "--remove-orphans", "--volumes", "--timeout", "5"]);
    await updateResourceReceipt(sentinel.receiptPath, "cleaned");
  } catch (error) {
    await updateResourceReceipt(sentinel.receiptPath, "cleanup-pending").catch(() => {});
    throw error;
  }
  await assertProjectEmpty(sentinel.project, "sentinel cleanup");
}

async function runIntegration() {
  const runId = process.env.HARDEN_LLM_TEST_RUN_ID;
  const resourceDirectory = process.env.HARDEN_LLM_TEST_RESOURCE_DIR ?? defaultResourceDirectory();
  if (!runId || !process.env.HARDEN_LLM_TEST_DAEMON_LOCK_TOKEN) {
    throw new Error("TEST-274 requires scripts/run-test-tier.mjs to supply a managed run ID and inherited Docker lease");
  }
  const daemonId = await docker(["info", "--format", "{{.ID}}"]);
  const lease = await inheritedDaemonLockLease({ daemonId, resourceDirectory, environment: process.env });
  assert.ok(lease?.delegated, "TEST-274 must verify and reuse the managed runner's local daemon lock lease");
  assert.equal(lease.daemonId, process.env.HARDEN_LLM_TEST_DAEMON_LOCK_DAEMON_ID);
  await docker(["compose", "version", "--short"]);
  await docker(["image", "inspect", FIXTURE_IMAGE, "--format", "{{.Id}}"]);

  const temporaryDirectory = await fs.mkdtemp(path.join(os.tmpdir(), "harden-llm-test-274-"));
  let composeFile;
  const trackedProjects = [];
  const activeSupervisors = [];
  let sentinel = null;
  let recoveryFailure = false;
  let primaryFailure = null;
  const scenarioResults = [];
  try {
    composeFile = await createComposeFile(temporaryDirectory);
    sentinel = createSentinel({ resourceDirectory, runId, daemonId, composeFile });
    await startSentinel(sentinel);
    for (const scenario of ["success", "partial-create", "term", "supervisor-sigkill"]) {
      const entryCount = trackedProjects.length;
      try {
        const result = await runFixtureSupervisorCase({ scenario, resourceDirectory, runId, composeFile, temporaryDirectory, sentinel, trackedProjects });
        scenarioResults.push(result);
        if (trackedProjects[entryCount]?.supervisor) activeSupervisors.push(trackedProjects[entryCount].supervisor);
      } catch (error) {
        const entry = trackedProjects[entryCount];
        if (entry?.supervisor && !activeSupervisors.includes(entry.supervisor)) activeSupervisors.push(entry.supervisor);
        if (entry?.recoveryFailed) recoveryFailure = true;
        throw error;
      }
    }
  } catch (error) {
    primaryFailure = error;
  } finally {
    for (const supervisor of activeSupervisors) await stopSupervisor(supervisor);
    if (sentinel) {
      try { await cleanSentinel(sentinel); }
      catch (error) { primaryFailure = new AggregateError([primaryFailure, error].filter(Boolean), "TEST-274 failed and its independently receipted sentinel could not be cleaned"); }
    }
    const unreconciled = [];
    for (const project of trackedProjects) {
      if (project.recoveryAttempted || project.recoveryFailed) continue;
      try {
        if ((await readResourceReceipt(project.receiptPath)).state !== "cleaned") unreconciled.push(project);
      } catch (error) {
        if (error.code !== "ENOENT") unreconciled.push(project);
      }
    }
    if (!recoveryFailure && unreconciled.length > 0) {
      unreconciled.forEach((project) => { project.recoveryAttempted = true; });
      try {
        await reconcileWithNextManagedOwner({ root: REPOSITORY_ROOT, resourceDirectory, runId, temporaryDirectory });
      } catch (error) {
        recoveryFailure = true;
        primaryFailure = new AggregateError([primaryFailure, error].filter(Boolean), "TEST-274 failed and final next-owner recovery was unsuccessful");
      }
    }
    const incomplete = [];
    for (const project of trackedProjects) {
      try {
        const receipt = await readResourceReceipt(project.receiptPath);
        const inventory = await projectInventory(project.project);
        if (receipt.state !== "cleaned" || resourceCount(inventory) !== 0) incomplete.push(project.project);
      } catch (error) {
        if (error.code !== "ENOENT") incomplete.push(project.project);
      }
    }
    if (sentinel) {
      if (sentinel.receiptPath) {
        try {
          const receipt = await readResourceReceipt(sentinel.receiptPath);
          const inventory = await projectInventory(sentinel.project);
          if (receipt.state !== "cleaned" || resourceCount(inventory) !== 0) incomplete.push(sentinel.project);
        } catch (error) { if (error.code !== "ENOENT") incomplete.push(sentinel.project); }
      }
    }
    if (incomplete.length === 0) await fs.rm(temporaryDirectory, { recursive: true, force: true });
    else console.error(`TEST-274 preserved Compose fixture files for pending receipts: ${temporaryDirectory} (${incomplete.join(", ")})`);
  }
  if (primaryFailure) throw primaryFailure;
  console.log(JSON.stringify({ accepted: true, testId: "TEST-274", daemonId, fixtureImage: FIXTURE_IMAGE, scenarios: scenarioResults, sentinelPreserved: true }));
}

async function main() {
  if (process.argv[2] === "--supervisor") await runSupervisor();
  else await runIntegration();
}

main().catch((error) => {
  if (process.send && process.connected) {
    process.send({ type: "error", message: error.message }, () => process.disconnect());
  }
  console.error(error.stack ?? error.message);
  process.exitCode = 1;
});
