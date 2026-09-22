#!/usr/bin/env node

import { spawn, spawnSync } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { constants as fsConstants, promises as fs } from "node:fs";
import os from "node:os";
import path from "node:path";
import { performance } from "node:perf_hooks";
import { fileURLToPath } from "node:url";

import { capacitySafetyFailure, collectDockerResourceSample, hashImageIDs, summarizeResourceSamples } from "./measure-test-resources.mjs";
import { acquireDaemonLock, classifyReceiptOwner, createResourceReceipt, daemonLockEnvironment, defaultResourceDirectory, inheritedDaemonLockLease, processStartIdentity, readHostBootID, readResourceReceipt, releaseDaemonLock, updateResourceReceipt } from "./test-resource-lifecycle.mjs";

const REPOSITORY_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const DEFAULT_RUN_ROOT = path.join(REPOSITORY_ROOT, "tmp", "test-feedback");
const DEFAULT_SEED = 104729;
const MAX_CAPTURE_BYTES = 8192;
const MAX_FAILURE_DETAIL_BYTES = 4096;
const TASK_TIMEOUT_GRACE_MS = 2_000;
const DEFAULT_RESOURCE_CLEANUP_MS = 120_000;
const RESOURCE_INVENTORY_MS = 15_000;
const MAX_PROJECT_RESOURCES = 1024;
const MAX_RUN_REPORT_BYTES = 2 * 1024 * 1024;

export async function loadManifest(manifestPath) {
  const manifest = JSON.parse(await fs.readFile(manifestPath, "utf8"));
  validateManifest(manifest);
  return manifest;
}

export function validateManifest(manifest) {
  if (!manifest || manifest.schemaVersion !== 1) throw new Error("manifest schemaVersion must be 1");
  if (!manifest.documentId) throw new Error("manifest documentId is required");
  if (!manifest.resourceClasses || typeof manifest.resourceClasses !== "object") throw new Error("manifest resourceClasses are required");
  for (const [name, definition] of Object.entries(manifest.resourceClasses)) {
    if (!Number.isInteger(definition.slots) || definition.slots <= 0) throw new Error(`resource ${name} has invalid slots`);
    if (definition.exclusive !== undefined && typeof definition.exclusive !== "boolean") throw new Error(`resource ${name} has invalid exclusive flag`);
  }
  if (!Array.isArray(manifest.tasks) || manifest.tasks.length === 0) throw new Error("manifest tasks are required");
  validateTaskGraph(manifest.tasks, manifest.resourceClasses);
  const seenTestIDs = new Map();
  for (const task of manifest.tasks) {
    if (!Array.isArray(task.testIds) || task.testIds.length === 0) throw new Error(`task ${task.id} has no testIds`);
    for (const testID of task.testIds) {
      if (seenTestIDs.has(testID)) throw new Error(`test ID ${testID} is assigned to ${seenTestIDs.get(testID)} and ${task.id}`);
      seenTestIDs.set(testID, task.id);
    }
    if (!Array.isArray(task.requiredFor)) throw new Error(`task ${task.id} has no requiredFor list`);
    if (!Array.isArray(task.pathSelectors)) throw new Error(`task ${task.id} has no pathSelectors list`);
    if ((task.tier === "T0" || task.tier === "T1" || task.tier === "T2") && (task.network !== "forbidden" || (task.credentialKeys ?? []).length !== 0)) {
      throw new Error(`cheap task ${task.id} is not offline and credential-free`);
    }
    if (task.environment && typeof task.environment !== "object") throw new Error(`task ${task.id} environment must be an object`);
    if (Object.keys(task.environment ?? {}).some((key) => /(password|secret|token|api[_-]?key|access[_-]?key)/i.test(key))) {
      throw new Error(`task ${task.id} environment contains a credential-shaped key`);
    }
  }
  return manifest;
}

function validateTaskGraph(tasks, resourceClasses) {
  const byID = new Map();
  for (const task of tasks) {
    if (!task.id || byID.has(task.id)) throw new Error(`task ID is missing or duplicated: ${task.id ?? ""}`);
    if (!["T0", "T1", "T2", "T3", "T4", "T5"].includes(task.tier)) throw new Error(`task ${task.id} has invalid tier`);
    if (!Array.isArray(task.command) || task.command.length === 0) throw new Error(`task ${task.id} has no command`);
    if (!resourceClasses[task.resourceClass]) throw new Error(`task ${task.id} references unknown resource ${task.resourceClass}`);
    if (!Number.isInteger(task.timeoutMs) || task.timeoutMs <= 0) throw new Error(`task ${task.id} has invalid timeoutMs`);
    if (!task.cleanupOwner) throw new Error(`task ${task.id} has no cleanupOwner`);
    if (!task.network) throw new Error(`task ${task.id} has no network policy`);
    if (!Array.isArray(task.dependsOn)) throw new Error(`task ${task.id} dependsOn must be an array`);
    byID.set(task.id, task);
  }
  for (const task of tasks) {
    for (const dependency of task.dependsOn) if (!byID.has(dependency)) throw new Error(`task ${task.id} depends on unknown task ${dependency}`);
  }
  const visiting = new Set();
  const visited = new Set();
  const visit = (task) => {
    if (visiting.has(task.id)) throw new Error(`task dependency cycle includes ${task.id}`);
    if (visited.has(task.id)) return;
    visiting.add(task.id);
    for (const dependency of task.dependsOn) visit(byID.get(dependency));
    visiting.delete(task.id);
    visited.add(task.id);
  };
  for (const task of tasks) visit(task);
  return tasks;
}

export function selectTasks(manifest, selector) {
  validateManifest(manifest);
  const byID = new Map(manifest.tasks.map((task) => [task.id, task]));
  const selected = new Set();
  const selectedTask = byID.get(selector) ? byID.get(selector) : null;
  const requested = selectedTask ? [selectedTask] : manifest.tasks.filter((task) => (task.requiredFor ?? []).includes(selector));
  if (requested.length === 0) throw new Error(`no tasks selected for ${selector}`);
  const add = (task) => {
    if (selected.has(task.id)) return;
    for (const dependencyID of task.dependsOn ?? []) add(byID.get(dependencyID));
    selected.add(task.id);
  };
  for (const task of requested) add(task);
  return manifest.tasks.filter((task) => selected.has(task.id));
}

function boundedCapture() {
  let bytes = 0;
  let first = "";
  let tail = "";
  return {
    append(chunk) {
      const text = String(chunk);
      bytes += Buffer.byteLength(text);
      const remaining = Math.max(0, MAX_CAPTURE_BYTES - Buffer.byteLength(first));
      if (remaining > 0) first += Buffer.from(text).subarray(0, remaining).toString("utf8");
      tail = (tail + text).slice(-MAX_CAPTURE_BYTES);
    },
    get value() {
      const truncatedBytes = Math.max(0, bytes - MAX_CAPTURE_BYTES);
      // Structured Docker metadata must be parsed before redaction: replacing
      // label URLs with [url] would corrupt JSON. Keep the bounded raw prefix
      // internal; reports and diagnostics use only the redacted fields.
      return { bytes, preview: scrub(first), rawPreview: first, tailPreview: scrub(tail), truncatedBytes };
    },
  };
}

function scrub(value) {
  return String(value)
    .replace(/Bearer\s+[A-Za-z0-9._-]+/gi, "Bearer [redacted]")
    .replace(/sk-[A-Za-z0-9]+/g, "sk-[redacted]")
    .replace(/([?&](?:key|token|secret|password|authorization)=[^&\s]+)/gi, "$1=[redacted]")
    .replace(/(password|secret|api[_-]?key|access[_-]?key)(\s*[:=]\s*)[^\s,;]+/gi, "$1$2[redacted]")
    .replace(/https?:\/\/[^\s]+/g, "[url]");
}

function shellQuote(value) {
  return `'${String(value).replaceAll("'", "'\\''")}'`;
}

function summarizeFailure(value) {
  const lines = String(value)
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean);
  const useful = lines.filter((line) => !/^(?:failed?:?\s+\d+\s+features?|finished in |randomized with seed |\d+ tests?,?\s+\d+ failures?)/i.test(line));
  return scrub((useful.length > 0 ? useful : lines).slice(-3).join(" | ")).slice(0, 240);
}

function failureDetail(task, stdout, stderr) {
  if (task.tier === "T5" || task.network === "public") return "[suppressed]";
  const detail = `${stderr.tailPreview}\n${stdout.tailPreview}`.trim();
  return detail ? scrub(detail).slice(-MAX_FAILURE_DETAIL_BYTES) : null;
}

function commandOutput(command, args, timeout = 3_000) {
  try {
    return spawnSync(command, args, { encoding: "utf8", timeout, stdio: ["ignore", "pipe", "ignore"] }).stdout.trim();
  } catch {
    return "unavailable";
  }
}

async function exists(filePath) {
  try {
    await fs.access(filePath);
    return true;
  } catch {
    return false;
  }
}

async function sha256File(filePath) {
  return createHash("sha256").update(await fs.readFile(filePath)).digest("hex");
}

function servicePoolProjectName() {
  return `harden-llm-test-${randomBytes(6).toString("hex")}`;
}

function composeBaseArguments(composeFile, project) {
  return ["compose", "-f", composeFile, "-p", project];
}

function normalizePublishedEndpoint(value) {
  const line = String(value).trim().split(/\r?\n/).filter(Boolean).at(-1) ?? "";
  const separator = line.lastIndexOf(":");
  if (separator <= 0 || separator === line.length - 1) throw new Error(`invalid published service endpoint ${line}`);
  const host = line.slice(0, separator);
  const port = line.slice(separator + 1);
  if (!/^\d+$/.test(port)) throw new Error(`invalid published service port ${line}`);
  return `${host === "0.0.0.0" || host === "::" ? "127.0.0.1" : host}:${port}`;
}

async function runExternal(executable, args, options = {}) {
  const stdout = boundedCapture();
  const stderr = boundedCapture();
  const child = spawn(executable, args, {
    cwd: options.cwd ?? REPOSITORY_ROOT,
    env: options.environment ?? process.env,
    stdio: ["ignore", "pipe", "pipe"],
    detached: process.platform !== "win32",
  });
  child.stdout.on("data", (chunk) => stdout.append(chunk));
  child.stderr.on("data", (chunk) => stderr.append(chunk));
  let timedOut = false;
  let killTimer = null;
  const terminate = () => terminateProcessGroup(child, "SIGTERM");
  const abortHandler = () => terminate();
  options.signal?.addEventListener("abort", abortHandler, { once: true });
  const timeoutTimer = options.timeoutMs ? setTimeout(() => {
    timedOut = true;
    terminate();
    killTimer = setTimeout(() => terminateProcessGroup(child, "SIGKILL"), TASK_TIMEOUT_GRACE_MS);
    killTimer.unref();
  }, options.timeoutMs) : null;
  const outcome = await new Promise((resolve) => {
    child.once("error", (error) => resolve({ error }));
    child.once("close", (exitCode, signal) => resolve({ exitCode, signal }));
  });
  if (timeoutTimer) clearTimeout(timeoutTimer);
  if (killTimer) clearTimeout(killTimer);
  options.signal?.removeEventListener("abort", abortHandler);
  return {
    status: outcome.error ? 1 : (outcome.exitCode ?? 1),
    signal: outcome.signal ?? null,
    timedOut,
    stdout: stdout.value,
    stderr: stderr.value,
  };
}

async function hostResourceSample(root = REPOSITORY_ROOT, dockerDataRoot = root) {
  const host = {};
  host.totalMemoryBytes = os.totalmem();
  try {
    const memory = await fs.readFile("/proc/meminfo", "utf8");
    const available = memory.match(/^MemAvailable:\s+(\d+)\s+kB$/m);
    if (available) host.availableMemoryBytes = Number(available[1]) * 1024;
  } catch {
    // The report records unavailable host metrics as null with provenance.
  }
  try {
    const filesystem = await fs.statfs(dockerDataRoot, { bigint: true });
    const freeBytes = filesystem.bavail * filesystem.bsize;
    if (freeBytes >= 0n && freeBytes <= BigInt(Number.MAX_SAFE_INTEGER)) host.availableDiskBytes = Number(freeBytes);
  } catch {
    // Capacity runs fail closed if filesystem headroom cannot be measured.
  }
  const pressure = {};
  for (const resource of ["cpu", "memory", "io"]) {
    try {
      const content = await fs.readFile(`/proc/pressure/${resource}`, "utf8");
      for (const scope of ["some", "full"]) {
        const line = content.split(/\r?\n/).find((entry) => entry.startsWith(`${scope} `));
        const match = line?.match(/\bavg10=([0-9]+(?:\.[0-9]+)?)/);
        if (match) pressure[`${resource}${scope[0].toUpperCase()}${scope.slice(1)}Avg10`] = Number(match[1]);
      }
    } catch {
      // Per-resource PSI availability is independent.
    }
  }
  host.pressure = pressure;
  return host;
}

function startDockerResourceSampler(project, environment, cwd, dockerDataRoot = cwd) {
  const samples = [];
  let latestSample = null;
  const expectedIntervalMs = 5_000;
  let inFlight = null;
  let stopped = false;
  let timer = null;

  const takeSample = async () => {
    if (stopped || inFlight || samples.length >= 1_000) return latestSample;
    inFlight = (async () => {
      const host = await hostResourceSample(cwd, dockerDataRoot);
      const sample = await collectDockerResourceSample(project, async (args) => {
        const result = await runExternal("docker", args, { cwd, timeoutMs: 2_000, environment });
        return {
          status: result.status,
          stdout: result.stdout.rawPreview,
          redactedStderr: result.stderr.tailPreview,
          truncatedBytes: result.stdout.truncatedBytes,
        };
      }, { timestamp: new Date().toISOString(), host });
      latestSample = sample;
      samples.push(sample);
    })().catch(() => {
      latestSample = {
        timestamp: new Date().toISOString(),
        host: {},
        collectionNullReasons: {
          containers: "resource sample failed unexpectedly",
          volumes: "resource sample failed unexpectedly",
        },
      };
      samples.push(latestSample);
    }).finally(() => { inFlight = null; });
    await inFlight;
    return latestSample;
  };

  timer = setInterval(() => { void takeSample(); }, expectedIntervalMs);
  timer.unref();
  return {
    sample: takeSample,
    safetyFailure() {
      return capacitySafetyFailure(latestSample?.host);
    },
    async stop() {
      if (stopped) return summarizeResourceSamples(project, samples, { expectedIntervalMs });
      stopped = true;
      clearInterval(timer);
      if (inFlight) await inFlight;
      if (samples.length < 1_000) {
        stopped = false;
        await takeSample();
        stopped = true;
      }
      return summarizeResourceSamples(project, samples, { expectedIntervalMs });
    },
  };
}

function poolFailure(pool, message) {
  return new Error(`${pool?.project ?? "integration service pool"}: ${message}`);
}

export function resourceCleanupOptions(options, now = performance.now()) {
  const budget = options.cleanupTimeoutMs ?? DEFAULT_RESOURCE_CLEANUP_MS;
  const taskState = options.taskCleanupState;
  const taskDeadline = taskState
    ? (taskState.cleanupDeadline ??= options.cleanupDeadline ?? now + budget)
    : (options.cleanupDeadline ?? now + budget);
  const cancellationDeadline = options.lifecycleState?.cleanupDeadline;
  return {
    ...options,
    cleanupDeadline: Number.isFinite(cancellationDeadline)
      ? Math.min(taskDeadline, cancellationDeadline)
      : taskDeadline,
  };
}

async function startServicePool(task, options) {
  const definition = task.servicePool;
  if (!definition || typeof definition !== "object") throw new Error(`task ${task.id} has no servicePool definition`);
  if (!Array.isArray(definition.services) || definition.services.length === 0) throw new Error(`task ${task.id} servicePool has no services`);
  const project = servicePoolProjectName();
  if (!/^harden-llm-test-[0-9a-f]{12}$/.test(project)) throw new Error(`invalid service pool project ${project}`);
  const composeFile = path.resolve(options.root, definition.composeFile ?? "");
  if (!(await exists(composeFile))) throw new Error(`service pool Compose file is missing: ${composeFile}`);
  const base = composeBaseArguments(composeFile, project);
  const resourceDirectory = options.resourceDirectory ?? defaultResourceDirectory();
  const dockerEnvironment = { ...process.env, ...(options.environment ?? {}), ...daemonLockEnvironment(options.daemonLockLease) };
  const identity = await runExternal("docker", ["info", "--format", "{{.ID}}"], { cwd: options.root, signal: options.signal, timeoutMs: 10_000, environment: dockerEnvironment });
  const daemonId = identity.stdout.preview.trim();
  if (identity.status !== 0 || !daemonId) throw poolFailure({ project }, "cannot establish Docker daemon identity before resource creation");
  if (options.daemonLockLease && daemonId !== options.daemonLockLease.daemonId) throw poolFailure({ project }, "Docker daemon identity changed after acquiring the daemon guard");
  const { receiptPath } = await createResourceReceipt({
    directory: resourceDirectory,
    runId: options.runID ?? path.basename(options.runDirectory),
    project,
    daemonId,
    composeFiles: [composeFile],
    sourceSHA: options.sourceSHA,
  });
  const childEnvironment = {
    ...process.env,
    ...(options.environment ?? {}),
    ...daemonLockEnvironment(options.daemonLockLease),
    HARDEN_LLM_TEST_RESOURCE_DIR: resourceDirectory,
    HARDEN_LLM_TEST_RESOURCE_RECEIPT: receiptPath,
    HARDEN_LLM_TEST_RUN_ID: options.runID ?? path.basename(options.runDirectory),
  };
  const pool = { project, composeFile, base, environment: {}, cleaned: false, receiptPath };
  try {
    await updateResourceReceipt(receiptPath, "creating");
    const up = await runExternal("docker", [...base, "up", "-d", "--wait", "--pull", "missing", ...definition.services.map((service) => service.name)], { cwd: options.root, signal: options.signal, timeoutMs: 120_000, environment: childEnvironment });
    if (up.status !== 0) throw poolFailure(pool, `service startup failed: ${scrub(up.stderr.tailPreview || up.stdout.tailPreview)}`);
    await updateResourceReceipt(receiptPath, "running");
    for (const service of definition.services) {
      if (!service.name || !Number.isInteger(service.port) || service.port <= 0) throw poolFailure(pool, "service definition is invalid");
      const resolved = await runExternal("docker", [...base, "port", service.name, String(service.port)], { cwd: options.root, signal: options.signal, timeoutMs: 10_000, environment: childEnvironment });
      if (resolved.status !== 0) throw poolFailure(pool, `cannot resolve ${service.name} port: ${scrub(resolved.stderr.tailPreview)}`);
      const endpoint = normalizePublishedEndpoint(resolved.stdout.preview);
      if (service.name === "harden-postgres") pool.environment.HARDEN_LLM_TEST_POSTGRES_ENDPOINT = endpoint;
      if (service.name === "garage") pool.environment.HARDEN_LLM_TEST_GARAGE_ENDPOINT = endpoint;
    }
  } catch (error) {
    error.resourceReceiptPath = receiptPath;
    const cleanupOptions = resourceCleanupOptions(options);
    error.resourceCleanupErrors = await cleanupServicePool(pool, cleanupOptions);
    throw error;
  }
  pool.environment.HARDEN_LLM_TEST_POOL = "1";
  pool.environment.HARDEN_LLM_TEST_SERVICE_PROJECT = project;
  return pool;
}

async function cleanupServicePool(pool, options) {
  if (!pool || pool.cleaned) return [];
  pool.cleaned = true;
  return cleanupResourceReceipt(pool.receiptPath, options);
}

function parseResourceIDs(output, kind) {
  if (output.truncatedBytes > 0) throw new Error(`${kind} inventory was truncated`);
  const ids = output.preview.split(/\r?\n/).map((value) => value.trim()).filter(Boolean);
  if (ids.length > MAX_PROJECT_RESOURCES) throw new Error(`${kind} inventory exceeded ${MAX_PROJECT_RESOURCES} resources`);
  const pattern = kind === "volumes" ? /^[A-Za-z0-9][A-Za-z0-9_.-]{0,254}$/ : /^[a-f0-9]{12,64}$/i;
  if (ids.some((id) => !pattern.test(id)) || new Set(ids).size !== ids.length) throw new Error(`${kind} inventory contains invalid or duplicate IDs`);
  return ids.sort();
}

function receiptResourceIDs(receipt) {
  return {
    containers: [...(receipt.resourceIds?.containers ?? [])],
    volumes: [...(receipt.resourceIds?.volumes ?? [])],
    networks: [...(receipt.resourceIds?.networks ?? [])],
  };
}

function mergeResourceIDs(...groups) {
  return Object.fromEntries(["containers", "volumes", "networks"].map((kind) => [kind, [...new Set(groups.flatMap((group) => group[kind] ?? []))].sort()]));
}

async function cleanupResourceReceipt(receiptPath, options) {
  let receipt;
  try {
    receipt = await readResourceReceipt(receiptPath);
  } catch (error) {
    return [`cannot validate resource receipt: ${scrub(error.message)}`];
  }
  const errors = [];
  const composeDownWarnings = [];
  const project = receipt.project;
  const resourceDirectory = path.dirname(path.dirname(receiptPath));
  const environment = {
    ...process.env,
    ...(options.environment ?? {}),
    ...daemonLockEnvironment(options.daemonLockLease),
    HARDEN_LLM_TEST_RUN_ID: receipt.runId,
    HARDEN_LLM_TEST_RESOURCE_DIR: resourceDirectory,
    HARDEN_LLM_TEST_RESOURCE_RECEIPT: receiptPath,
  };
  const deadline = options.cleanupDeadline ?? performance.now() + (options.cleanupTimeoutMs ?? DEFAULT_RESOURCE_CLEANUP_MS);
  const runDocker = async (label, args, operationLimitMs = RESOURCE_INVENTORY_MS, failureSink = errors) => {
    const remaining = deadline - performance.now();
    if (remaining <= TASK_TIMEOUT_GRACE_MS + 10) {
      failureSink.push(`${label}: total cleanup budget exhausted`);
      return null;
    }
    const result = await runExternal("docker", args, {
      cwd: options.root,
      timeoutMs: Math.max(1, Math.min(operationLimitMs, remaining - TASK_TIMEOUT_GRACE_MS)),
      environment,
    });
    if (result.status !== 0 || result.timedOut) {
      failureSink.push(`${label}: ${scrub(result.stderr.tailPreview || result.stdout.tailPreview || `docker exited ${result.status}`)}`);
      return null;
    }
    return result.stdout;
  };
  const inventory = async () => {
    const filter = `label=com.docker.compose.project=${project}`;
    const containersOutput = await runDocker("container inventory", ["ps", "-aq", "--filter", filter]);
    if (!containersOutput) return null;
    const volumesOutput = await runDocker("volume inventory", ["volume", "ls", "-q", "--filter", filter]);
    if (!volumesOutput) return null;
    const networksOutput = await runDocker("network inventory", ["network", "ls", "-q", "--filter", filter]);
    if (!networksOutput) return null;
    try {
      return {
        containers: parseResourceIDs(containersOutput, "containers"),
        volumes: parseResourceIDs(volumesOutput, "volumes"),
        networks: parseResourceIDs(networksOutput, "networks"),
      };
    } catch (error) {
      errors.push(scrub(error.message));
      return null;
    }
  };
  const markPending = async (resourceIDs) => {
    try {
      receipt = await updateResourceReceipt(receiptPath, "cleanup-pending", { resourceIds: resourceIDs });
    } catch (error) {
      errors.push(`cannot persist cleanup-pending receipt: ${scrub(error.message)}`);
    }
  };
  const daemonOutput = await runDocker("Docker daemon identity", ["info", "--format", "{{.ID}}"]);
  if (!daemonOutput || daemonOutput.preview.trim() !== receipt.daemonId) {
    errors.push(`resource ${project} is not being cleaned on its recorded Docker daemon`);
    await markPending(receiptResourceIDs(receipt));
    return errors;
  }
  let currentStart;
  let currentBoot;
  try {
    currentStart = await processStartIdentity(process.pid);
    currentBoot = await readHostBootID();
  } catch (error) {
    errors.push(`cannot establish cleanup owner identity: ${scrub(error.message)}`);
    await markPending(receiptResourceIDs(receipt));
    return errors;
  }
  const currentSupervisorOwnsReceipt = receipt.hostBootId === currentBoot
    && receipt.supervisorPid === process.pid
    && receipt.supervisorStart === currentStart;
  if (!currentSupervisorOwnsReceipt) {
    let owner = { status: "active", reason: "receipt belongs to another supervisor" };
    if (options.recovery) {
      try { owner = await classifyReceiptOwner(receipt); }
      catch (error) { owner = { status: "ambiguous", reason: error.message }; }
    }
    if (!options.recovery || owner.status !== "dead") {
      errors.push(`resource ${project} supervisor is not proven dead; recovery refused (${scrub(owner.reason)})`);
      await markPending(receiptResourceIDs(receipt));
      return errors;
    }
  }

  const before = await inventory();
  if (!before) {
    await markPending(receiptResourceIDs(receipt));
    return errors;
  }
  if (receipt.state === "cleaned" && before.containers.length + before.volumes.length + before.networks.length === 0) return errors;

  for (const id of before.containers) {
    if (!(await inspectProjectLabels("container", id, project, runDocker, errors))) {
      await markPending(mergeResourceIDs(receiptResourceIDs(receipt), before));
      return errors;
    }
  }
  for (const id of before.volumes) {
    if (!(await inspectProjectLabels("volume", id, project, runDocker, errors))) {
      await markPending(mergeResourceIDs(receiptResourceIDs(receipt), before));
      return errors;
    }
  }
  for (const id of before.networks) {
    if (!(await inspectProjectLabels("network", id, project, runDocker, errors))) {
      await markPending(mergeResourceIDs(receiptResourceIDs(receipt), before));
      return errors;
    }
  }

  const foreignAttachedVolumes = new Set();
  for (const id of before.volumes) {
    const attachedOutput = await runDocker(`pre-cleanup volume attachment inventory ${id}`, ["ps", "-aq", "--filter", `volume=${id}`]);
    if (!attachedOutput) {
      await markPending(mergeResourceIDs(receiptResourceIDs(receipt), before));
      return errors;
    }
    let attachedContainers;
    try { attachedContainers = parseResourceIDs(attachedOutput, "containers"); }
    catch (error) {
      errors.push(scrub(error.message));
      await markPending(mergeResourceIDs(receiptResourceIDs(receipt), before));
      return errors;
    }
    for (const containerID of attachedContainers) {
      if (!(await inspectProjectLabels("container", containerID, project, runDocker, errors))) foreignAttachedVolumes.add(id);
    }
  }
  const foreignAttachedNetworks = new Set();
  for (const id of before.networks) {
    const attachmentOutput = await runDocker(`pre-cleanup network attachment inventory ${id}`, ["network", "inspect", "--format", "{{json .Containers}}", id]);
    if (!attachmentOutput) {
      await markPending(mergeResourceIDs(receiptResourceIDs(receipt), before));
      return errors;
    }
    let attachmentIDs;
    try {
      if (attachmentOutput.truncatedBytes > 0) throw new Error("network attachment inventory was truncated");
      const decoded = JSON.parse(attachmentOutput.rawPreview || "null");
      attachmentIDs = decoded && typeof decoded === "object" ? Object.keys(decoded) : [];
      if (attachmentIDs.some((containerID) => !/^[a-f0-9]{12,64}$/i.test(containerID))) throw new Error("network attachment inventory contains invalid IDs");
    } catch (error) {
      errors.push(`${scrub(error.message)} for network ${id}`);
      await markPending(mergeResourceIDs(receiptResourceIDs(receipt), before));
      return errors;
    }
    for (const containerID of attachmentIDs) {
      if (!(await inspectProjectLabels("container", containerID, project, runDocker, errors))) foreignAttachedNetworks.add(id);
    }
  }

  const currentIDs = mergeResourceIDs(receiptResourceIDs(receipt), before);
  try {
    if (receipt.state === "cleaned") receipt = await updateResourceReceipt(receiptPath, "cleanup-pending", { resourceIds: currentIDs });
    receipt = await updateResourceReceipt(receiptPath, "cleaning", { resourceIds: currentIDs });
  } catch (error) {
    errors.push(`cannot record cleanup ownership before Docker removal: ${scrub(error.message)}`);
    await markPending(currentIDs);
    return errors;
  }
  if (before.containers.length + before.volumes.length + before.networks.length === 0) {
    try { await updateResourceReceipt(receiptPath, "cleaned", { resourceIds: currentIDs }); }
    catch (error) { errors.push(`cannot record empty resource inventory: ${scrub(error.message)}`); }
    return errors;
  }

  const composeArgs = ["compose", "--project-name", project, ...receipt.composeFiles.flatMap((file) => ["-f", file]), "down", "--remove-orphans", "--timeout", "30"];
  await runDocker("Compose down", composeArgs, 30_000, composeDownWarnings);
  const reportComposeDownWarnings = (outcome) => {
    if (composeDownWarnings.length === 0) return;
    const diagnostics = composeDownWarnings.map((warning) => `resource ${project}: ${outcome}: ${warning}`);
    if (Array.isArray(options.cleanupWarnings)) options.cleanupWarnings.push(...diagnostics);
    else errors.push(...diagnostics);
  };

  const afterDown = await inventory();
  if (!afterDown) {
    reportComposeDownWarnings("Compose down failed; exact fallback cleanup could not be verified");
    await markPending(currentIDs);
    return errors;
  }
  for (const id of afterDown.containers) {
    if (!(await inspectProjectLabels("container", id, project, runDocker, errors))) continue;
    await runDocker(`remove owned container ${id}`, ["rm", "-f", id], 15_000);
  }

  const afterContainers = await inventory();
  if (!afterContainers) {
    reportComposeDownWarnings("Compose down failed; container fallback cleanup could not be verified");
    await markPending(currentIDs);
    return errors;
  }
  for (const id of afterContainers.volumes) {
    if (!(await inspectProjectLabels("volume", id, project, runDocker, errors))) continue;
    if (foreignAttachedVolumes.has(id)) {
      errors.push(`volume ${id} had a foreign attachment before teardown; it was not removed`);
      continue;
    }
    const attachedOutput = await runDocker(`volume attachment inventory ${id}`, ["ps", "-aq", "--filter", `volume=${id}`]);
    if (!attachedOutput) continue;
    let attachments;
    try { attachments = parseResourceIDs(attachedOutput, "containers"); }
    catch (error) { errors.push(scrub(error.message)); continue; }
    let foreignAttachment = false;
    for (const containerID of attachments) {
      const owned = await inspectProjectLabels("container", containerID, project, runDocker, errors);
      if (!owned) foreignAttachment = true;
    }
    if (foreignAttachment || attachments.length > 0) {
      errors.push(`volume ${id} is still attached; it was not removed`);
      continue;
    }
    await runDocker(`remove owned volume ${id}`, ["volume", "rm", id], 15_000);
  }

  const afterVolumes = await inventory();
  if (!afterVolumes) {
    reportComposeDownWarnings("Compose down failed; volume fallback cleanup could not be verified");
    await markPending(currentIDs);
    return errors;
  }
  for (const id of afterVolumes.networks) {
    if (!(await inspectProjectLabels("network", id, project, runDocker, errors))) continue;
    if (foreignAttachedNetworks.has(id)) {
      errors.push(`network ${id} had a foreign attachment before teardown; it was not removed`);
      continue;
    }
    const attachmentOutput = await runDocker(`network attachment inventory ${id}`, ["network", "inspect", "--format", "{{json .Containers}}", id]);
    if (!attachmentOutput) continue;
    let attachments;
    try {
      if (attachmentOutput.truncatedBytes > 0) throw new Error("network attachment inventory was truncated");
      const decoded = JSON.parse(attachmentOutput.rawPreview || "null");
      attachments = decoded && typeof decoded === "object" ? Object.keys(decoded) : [];
    }
    catch { errors.push(`network ${id} returned invalid attachment inventory`); continue; }
    if (attachments.length > 0) {
      for (const containerID of attachments) await inspectProjectLabels("container", containerID, project, runDocker, errors);
      errors.push(`network ${id} remains attached; it was not removed`);
      continue;
    }
    await runDocker(`remove owned network ${id}`, ["network", "rm", id], 15_000);
  }

  const afterCleanup = await inventory();
  if (!afterCleanup) {
    reportComposeDownWarnings("Compose down failed; final fallback inventory could not be verified");
    await markPending(currentIDs);
    return errors;
  }
  const remaining = afterCleanup.containers.length + afterCleanup.volumes.length + afterCleanup.networks.length;
  const finalIDs = mergeResourceIDs(currentIDs, afterCleanup);
  if (remaining > 0) errors.push(`resource ${project} retains ${afterCleanup.containers.length} containers, ${afterCleanup.volumes.length} volumes, and ${afterCleanup.networks.length} networks`);
  try {
    if (remaining > 0) await updateResourceReceipt(receiptPath, "cleanup-pending", { resourceIds: finalIDs });
    else await updateResourceReceipt(receiptPath, "cleaned", { resourceIds: finalIDs });
  } catch (error) {
    errors.push(`cannot persist final resource inventory: ${scrub(error.message)}`);
  }
  const outcome = remaining === 0
    ? "Compose down failed; exact project cleanup and empty final inventory succeeded"
    : "Compose down failed and final project inventory is not empty";
  reportComposeDownWarnings(outcome);
  if (remaining > 0 && Array.isArray(options.cleanupWarnings)) {
    errors.push(...composeDownWarnings.map((warning) => `resource ${project}: ${outcome}: ${warning}`));
  }
  return errors;
}

async function inspectProjectLabels(kind, id, project, runDocker, errors) {
  const argumentsByKind = {
    container: ["inspect", "--format", "{{json .Config.Labels}}", id],
    volume: ["volume", "inspect", "--format", "{{json .Labels}}", id],
    network: ["network", "inspect", "--format", "{{json .Labels}}", id],
  };
  const output = await runDocker(`${kind} label inspection ${id}`, argumentsByKind[kind]);
  if (!output) return false;
  if (output.truncatedBytes > 0) {
    errors.push(`${kind} ${id} returned truncated label inventory`);
    return false;
  }
  let labels;
  try { labels = JSON.parse(output.rawPreview); }
  catch { errors.push(`${kind} ${id} returned invalid label inventory`); return false; }
  if (!labels || labels["com.docker.compose.project"] !== project) {
    errors.push(`${kind} ${id} is not labeled for recorded project ${project}; it was not removed`);
    return false;
  }
  return true;
}

async function cleanupRunResourceReceipts(options, excludedReceiptPath = null) {
  const resourceDirectory = options.resourceDirectory ?? defaultResourceDirectory();
  const runDirectory = path.join(resourceDirectory, options.runID ?? path.basename(options.runDirectory));
  let names;
  try { names = await fs.readdir(runDirectory); }
  catch (error) { return error.code === "ENOENT" ? [] : [`cannot inventory run resource receipts: ${scrub(error.message)}`]; }
  const errors = [];
  for (const name of names.sort()) {
    if (!/^resource-[A-Za-z0-9_.:-]{1,200}\.json$/.test(name)) {
      errors.push(`run resource ledger contains an unexpected entry ${scrub(name)}`);
      continue;
    }
    const receiptPath = path.join(runDirectory, name);
    if (receiptPath === excludedReceiptPath) continue;
    let receipt;
    try { receipt = await readResourceReceipt(receiptPath); }
    catch (error) { errors.push(`cannot validate run resource receipt ${scrub(name)}: ${scrub(error.message)}`); continue; }
    if (receipt.runId !== (options.runID ?? path.basename(options.runDirectory))) {
      errors.push(`resource receipt ${scrub(name)} has a mismatched run ID`);
      continue;
    }
    if (receipt.state === "cleaned") continue;
    errors.push(...await cleanupResourceReceipt(receiptPath, { ...options, recovery: true }));
  }
  return errors;
}

function isDockerManagedTask(task) {
  return Boolean(task.servicePool || task.requiresDocker || task.usesDocker || task.container?.dockerSocket || path.basename(task.command?.[0] ?? "") === "docker");
}

async function establishLocalDockerIdentity(root, environment, signal) {
  if (process.platform !== "linux") throw new Error("Docker-backed test selections require Linux and a local Docker daemon");
  const context = String(environment.DOCKER_CONTEXT ?? "").trim();
  const hostOverride = String(environment.DOCKER_HOST ?? "").trim();
  let endpoint;
  if (context) {
    const result = await runExternal("docker", ["context", "inspect", context, "--format", '{{(index .Endpoints "docker").Host}}'], { cwd: root, timeoutMs: 5_000, environment, signal });
    endpoint = result.stdout.preview.trim();
    if (result.status !== 0 || !endpoint) throw new Error("cannot prove Docker context endpoint is local; refusing unlocked or remote Docker access");
  } else if (hostOverride) {
    endpoint = hostOverride;
  } else {
    const result = await runExternal("docker", ["context", "inspect", "--format", '{{(index .Endpoints "docker").Host}}'], { cwd: root, timeoutMs: 5_000, environment, signal });
    endpoint = result.stdout.preview.trim();
    if (result.status !== 0 || !endpoint) throw new Error("cannot prove current Docker context endpoint is local; refusing unlocked or remote Docker access");
  }
  if (!endpoint.startsWith("unix:///")) throw new Error(`Docker endpoint ${scrub(endpoint)} is not a supported local Unix socket; daemon-wide recovery is disabled for remote endpoints`);
  const identity = await runExternal("docker", ["info", "--format", "{{.ID}}"], { cwd: root, timeoutMs: 10_000, environment, signal });
  const daemonId = identity.stdout.preview.trim();
  if (identity.status !== 0 || !daemonId) throw new Error(`cannot establish Docker daemon identity before resource mutation: ${scrub(identity.stderr.tailPreview || identity.stdout.tailPreview)}`);
  return daemonId;
}

async function recoverStaleDaemonReceipts(daemonId, options) {
  const root = options.resourceDirectory ?? defaultResourceDirectory();
  let runEntries;
  try { runEntries = await fs.readdir(root, { withFileTypes: true }); }
  catch (error) { return error.code === "ENOENT" ? [] : [`cannot inventory Docker receipt ledger: ${scrub(error.message)}`]; }
  const errors = [];
  for (const runEntry of runEntries.sort((left, right) => left.name.localeCompare(right.name))) {
    if (runEntry.name === "locks") continue;
    if (!runEntry.isDirectory() || runEntry.isSymbolicLink() || !/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,190}$/.test(runEntry.name)) {
      errors.push(`Docker receipt ledger contains an unexpected entry ${scrub(runEntry.name)}`);
      continue;
    }
    const runDirectory = path.join(root, runEntry.name);
    let receiptNames;
    try { receiptNames = await fs.readdir(runDirectory); }
    catch (error) { errors.push(`cannot inventory Docker receipt run ${scrub(runEntry.name)}: ${scrub(error.message)}`); continue; }
    for (const name of receiptNames.sort()) {
      if (!/^resource-[A-Za-z0-9_.:-]{1,200}\.json$/.test(name)) {
        errors.push(`Docker receipt run ${scrub(runEntry.name)} contains an unexpected entry ${scrub(name)}`);
        continue;
      }
      const receiptPath = path.join(runDirectory, name);
      let receipt;
      try { receipt = await readResourceReceipt(receiptPath); }
      catch (error) { errors.push(`cannot validate Docker receipt ${scrub(runEntry.name)}/${scrub(name)}; record preserved: ${scrub(error.message)}`); continue; }
      if (receipt.runId !== runEntry.name || name !== `resource-${receipt.project}.json`) {
        errors.push(`Docker receipt ${scrub(runEntry.name)}/${scrub(name)} has mismatched run or project identity; record preserved`);
        continue;
      }
      if (receipt.daemonId !== daemonId || receipt.state === "cleaned") continue;
      let owner;
      try { owner = await classifyReceiptOwner(receipt); }
      catch (error) { errors.push(`cannot prove Docker receipt owner ${scrub(receipt.project)} dead; record preserved: ${scrub(error.message)}`); continue; }
      if (owner.status === "active") {
        errors.push(`Docker receipt ${scrub(receipt.project)} is still owned by an active supervisor (${scrub(owner.reason)}); no resource mutation started`);
        continue;
      }
      if (owner.status !== "dead") {
        errors.push(`Docker receipt ${scrub(receipt.project)} has an ambiguous supervisor (${scrub(owner.reason)}); record preserved`);
        continue;
      }
      const cleanupOptions = resourceCleanupOptions(options);
      const cleanupErrors = await cleanupResourceReceipt(receiptPath, { ...cleanupOptions, recovery: true });
      errors.push(...cleanupErrors.map((error) => `recovery ${scrub(receipt.project)}: ${error}`));
    }
  }
  return errors;
}

function parseTimeFile(contents) {
  const metric = (label) => {
    const match = contents.match(new RegExp(`^\\s*${label}\\s+(.+)$`, "m"));
    return match ? match[1].trim() : null;
  };
  const rssKiB = Number(metric("Maximum resident set size \\(kbytes\\):"));
  const userSeconds = Number(metric("User time \\(seconds\\):"));
  const systemSeconds = Number(metric("System time \\(seconds\\):"));
  return {
    peakRssMiB: Number.isFinite(rssKiB) && rssKiB > 0 ? rssKiB / 1024 : 0,
    cpuMs: (Number.isFinite(userSeconds) ? userSeconds * 1000 : 0) + (Number.isFinite(systemSeconds) ? systemSeconds * 1000 : 0),
  };
}

async function readRSS(pid) {
  try {
    const status = await fs.readFile(`/proc/${pid}/status`, "utf8");
    const match = status.match(/^VmRSS:\s+(\d+)\s+kB$/m);
    return match ? Number(match[1]) * 1024 : 0;
  } catch {
    return 0;
  }
}

async function childPIDs(pid) {
  try {
    const children = await fs.readFile(`/proc/${pid}/task/${pid}/children`, "utf8");
    return children.trim() ? children.trim().split(/\s+/).map(Number) : [];
  } catch {
    return [];
  }
}

async function processTree(pid) {
  const result = [];
  const pending = [pid];
  const seen = new Set();
  while (pending.length > 0) {
    const current = pending.shift();
    if (!current || seen.has(current)) continue;
    seen.add(current);
    result.push(current);
    pending.push(...await childPIDs(current));
  }
  return result;
}

async function processTreeRSS(pid) {
  let bytes = 0;
  for (const processID of await processTree(pid)) bytes += await readRSS(processID);
  return bytes;
}

function terminateProcessGroup(child, signal) {
  if (!child?.pid) return;
  if (process.platform !== "win32") {
    try {
      process.kill(-child.pid, signal);
      return;
    } catch {
      // The process group may already have exited; fall through to the child.
    }
  }
  try {
    child.kill(signal);
  } catch {
    // The child may already be gone.
  }
}

async function cleanupContainer(containerIDPath) {
  if (!containerIDPath || !(await exists(containerIDPath))) return null;
  const containerID = (await fs.readFile(containerIDPath, "utf8")).trim();
  if (!/^[a-f0-9]{12,64}$/i.test(containerID)) return "invalid runner-owned container ID";
  const result = spawnSync("docker", ["rm", "-f", containerID], { encoding: "utf8", timeout: 5_000, stdio: ["ignore", "pipe", "pipe"] });
  if (result.status === 0) return null;
  const diagnostic = `${result.stdout ?? ""}\n${result.stderr ?? ""}`;
  if (/no such container|is not running/i.test(diagnostic)) return null;
  return scrub(diagnostic).trim().slice(0, 240) || `docker rm exited ${result.status ?? "unknown"}`;
}

function resolvedEnvironment(task, options) {
  const environment = {
    ...process.env,
    ...(options.environment ?? {}),
    ...(task.environment ?? {}),
    HARDEN_LLM_TEST_SEED: String(options.seed ?? DEFAULT_SEED),
    HARDEN_LLM_TEST_RUN_ID: options.runID,
    HARDEN_LLM_TEST_RESOURCE_DIR: options.resourceDirectory ?? defaultResourceDirectory(),
    HARDEN_LLM_BENCHMARK_COLD: options.cold ? "1" : "0",
  };
  if (task.network === "forbidden") {
    environment.HARDEN_LLM_TEST_NETWORK = "forbidden";
    environment.HARDEN_LLM_TEST_OFFLINE = "1";
  }
  return environment;
}

export function resolvedCommand(task, options) {
  const effective = task.testSeed
    ? [...task.command, "--seed", String(task.testSeed)]
    : task.seedArgument
      ? [...task.command, "--seed", String(options.seed ?? DEFAULT_SEED)]
      : [...task.command];
  const packageSlots = options.packageSlots ?? task.packageSlots;
  const interpolated = effective.map((part) => part.replaceAll("${HARDEN_LLM_TEST_PACKAGE_SLOTS}", packageSlots === undefined ? "" : String(packageSlots)));
  if (!task.container) {
    const environment = resolvedEnvironment(task, options);
    if (path.basename(interpolated[0]) === "mix") {
      environment.MIX_BUILD_PATH = path.join(options.taskDirectory, "mix-build");
    }
    return {
      executable: interpolated[0],
      args: interpolated.slice(1),
      cwd: path.resolve(options.root, task.workingDirectory ?? "."),
      environment,
      containerIDPath: null,
    };
  }
  const containerIDPath = path.join(options.taskDirectory, "container.id");
  const commandText = interpolated.map(shellQuote).join(" ");
  const bootstrap = task.container.bootstrap
    ? "mix local.hex --force >/dev/null 2>&1 && mix local.rebar --force >/dev/null 2>&1 && mix deps.get >/dev/null && "
    : "";
  const containerEnvironment = {
    HARDEN_LLM_TEST_SEED: String(options.seed ?? DEFAULT_SEED),
    HARDEN_LLM_TEST_RUN_ID: options.runID ?? path.basename(options.runDirectory),
    ...daemonLockEnvironment(options.daemonLockLease),
    ...(task.network === "forbidden" ? {
      HARDEN_LLM_TEST_NETWORK: "forbidden",
      HARDEN_LLM_TEST_OFFLINE: "1",
    } : {}),
    ...(task.environment ?? {}),
  };
  const args = ["run", "--rm", "--network", task.container.network ?? "none"];
  for (const [key, value] of Object.entries(containerEnvironment)) args.push("-e", `${key}=${value}`);
  if (task.container.shmSize) args.push("--shm-size", task.container.shmSize);
  if (task.container.dockerSocket) args.push("-v", "/var/run/docker.sock:/var/run/docker.sock");
  // A login shell rewrites PATH from the image's profile and can hide pinned
  // tools such as the copied Go binary. Keep the image environment intact.
  const mountPath = task.container.mountAtHostPath ? options.root : "/workspace";
  args.push("--cidfile", containerIDPath, "-v", `${options.root}:${mountPath}`, "-w", path.join(mountPath, task.workingDirectory ?? "."), task.container.image, "sh", "-c", `${bootstrap}${commandText}`);
  return {
    executable: "docker",
    args,
    cwd: options.root,
    environment: resolvedEnvironment(task, options),
    containerIDPath,
  };
}

export async function runCommand(task, options) {
  const taskDirectory = path.join(options.runDirectory, "tasks", task.id.replace(/[^A-Za-z0-9_.-]/g, "_"));
  await fs.mkdir(taskDirectory, { recursive: true, mode: 0o700 });
  const timePath = path.join(taskDirectory, "time.txt");
  const stdout = boundedCapture();
  const stderr = boundedCapture();
  const command = resolvedCommand(task, { ...options, taskDirectory });
  command.environment = { ...command.environment, ...daemonLockEnvironment(options.daemonLockLease) };
  const startedAt = performance.now();
  let peakRSS = 0;
  let timedOut = false;
  let status = 1;
  let signal = null;
  let failureSummary = null;
  let output = stdout.value;
  let errorOutput = stderr.value;
  let pool = null;
  let resourceSampler = null;
  let resourceSamplerStopped = false;
  let failedReceiptPath = null;
  let timeMetrics = {};
  const result = {
    taskId: task.id,
    tier: task.tier,
    resourceClass: task.resourceClass,
    command: [command.executable, ...command.args].map((part) => scrub(part)),
    status: 1,
    seed: options.seed ?? DEFAULT_SEED,
    cold: Boolean(options.cold),
    signal: null,
    timedOut: false,
    wallTimeMs: 0,
    peakRssMiB: 0,
    cpuMs: 0,
    stdoutBytes: 0,
    stderrBytes: 0,
    truncatedOutputBytes: 0,
    stdoutPreview: "",
    stderrPreview: "",
    failureSummary: null,
    failureDetail: null,
    cleanupError: null,
    cleanupWarnings: [],
    servicePoolStarted: false,
    servicePoolProject: null,
    startedAtMs: null,
    endedAtMs: null,
  };
  const taskOptions = {
    ...options,
    taskCleanupState: options.taskCleanupState ?? { cleanupDeadline: null },
    cleanupWarnings: result.cleanupWarnings,
  };
  try {
    if (process.platform === "linux" && command.environment) {
      command.environment.HARDEN_LLM_TEST_SUPERVISOR_PID = String(process.pid);
      command.environment.HARDEN_LLM_TEST_SUPERVISOR_START = await processStartIdentity(process.pid);
    }
    if (task.capacityReport && task.servicePool) {
      const dataRootResult = await runExternal("docker", ["info", "--format", "{{.DockerRootDir}}"], {
        cwd: options.root, signal: options.signal, timeoutMs: 5_000, environment: command.environment,
      });
      const dataRootLines = dataRootResult.stdout.preview.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
      if (dataRootResult.status !== 0 || dataRootLines.length !== 1 || !path.isAbsolute(dataRootLines[0])) {
        throw new Error("capacity safety stop: Docker data-root filesystem cannot be identified");
      }
      try {
        taskOptions.capacityDockerDataRoot = await fs.realpath(dataRootLines[0]);
        if (!(await fs.stat(taskOptions.capacityDockerDataRoot)).isDirectory()) throw new Error("not a directory");
      } catch {
        throw new Error("capacity safety stop: Docker data-root filesystem is not accessible for measurement");
      }
      const reason = capacitySafetyFailure(await hostResourceSample(options.root, taskOptions.capacityDockerDataRoot));
      if (reason) throw new Error(reason);
    }
    if (task.servicePool) {
      pool = await startServicePool(task, taskOptions);
      command.environment = {...command.environment, ...pool.environment};
      result.servicePoolStarted = true;
      result.servicePoolProject = pool.project;
    }
    if (task.sampleDockerResources) {
      if (!pool) throw new Error("Docker resource sampling requires the runner-owned service pool");
      resourceSampler = startDockerResourceSampler(pool.project, command.environment, options.root, taskOptions.capacityDockerDataRoot ?? options.root);
      const initialSample = await resourceSampler.sample();
      if (task.capacityReport) {
        const safetyFailure = capacitySafetyFailure(initialSample?.host);
        if (safetyFailure) throw new Error(safetyFailure);
        const imageIDs = initialSample?.containers?.map((container) => container.imageId) ?? [];
        if (imageIDs.length !== task.servicePool.services.length || imageIDs.some((value) => !/^sha256:[a-f0-9]{64}$/i.test(value))) {
          const sampleFailure = initialSample?.collectionNullReasons?.containers;
          const detail = sampleFailure ?? `observed ${imageIDs.length} exact service containers for ${task.servicePool.services.length} configured services`;
          throw new Error(`capacity fingerprint requires one exact immutable image identity per service-pool container (${detail})`);
        }
        const composeSHA256 = await sha256File(pool.composeFile);
        const services = task.servicePool.services.map(({ name, port }) => ({ name, port })).sort((left, right) => left.name.localeCompare(right.name));
        const topologySHA256 = createHash("sha256").update(JSON.stringify({ composeSHA256, services })).digest("hex");
        command.environment.HARDEN_LLM_TEST_IMAGE_SET_SHA256 = hashImageIDs(imageIDs);
        command.environment.HARDEN_LLM_TEST_TOPOLOGY_SHA256 = topologySHA256;
      }
    }
    const capacityReportPath = task.capacityReport ? path.join(taskDirectory, "capacity-report.json") : null;
    if (capacityReportPath) command.environment.HARDEN_LLM_CAPACITY_REPORT_PATH = capacityReportPath;
    const useGNUTime = process.platform === "linux" && await exists("/usr/bin/time");
    const executable = useGNUTime ? "/usr/bin/time" : command.executable;
    const args = useGNUTime ? ["-v", "-o", timePath, "--", command.executable, ...command.args] : command.args;
    const child = spawn(executable, args, {
      cwd: command.cwd,
      env: command.environment,
      stdio: ["ignore", "pipe", "pipe"],
      detached: process.platform !== "win32",
    });
    child.stdout.on("data", (chunk) => stdout.append(chunk));
    child.stderr.on("data", (chunk) => stderr.append(chunk));

    let sampling = true;
    let samplingInFlight = false;
    let capacitySafetyStop = null;
    let capacitySafetyKillTimer = null;
    const safetyInterval = task.capacityReport && resourceSampler ? setInterval(() => {
      if (capacitySafetyStop) return;
      const reason = resourceSampler.safetyFailure();
      if (!reason) return;
      capacitySafetyStop = reason;
      terminateProcessGroup(child, "SIGTERM");
      capacitySafetyKillTimer = setTimeout(() => terminateProcessGroup(child, "SIGKILL"), TASK_TIMEOUT_GRACE_MS);
      capacitySafetyKillTimer.unref();
    }, 1_000) : null;
    const sampleRSS = async () => {
      if (!sampling || samplingInFlight || !child.pid) return;
      samplingInFlight = true;
      peakRSS = Math.max(peakRSS, await processTreeRSS(child.pid));
      samplingInFlight = false;
    };
    const interval = setInterval(sampleRSS, 50);
    let killTimer = null;
    const timeoutTimer = setTimeout(() => {
      timedOut = true;
      terminateProcessGroup(child, "SIGTERM");
      killTimer = setTimeout(() => terminateProcessGroup(child, "SIGKILL"), TASK_TIMEOUT_GRACE_MS);
      killTimer.unref();
    }, task.timeoutMs);
    const abortHandler = () => terminateProcessGroup(child, "SIGTERM");
    options.signal?.addEventListener("abort", abortHandler, { once: true });

    const outcome = await new Promise((resolve) => {
      child.once("error", (error) => resolve({ error }));
      child.once("close", (exitCode, childSignal) => resolve({ exitCode, signal: childSignal }));
    });
    clearTimeout(timeoutTimer);
    if (killTimer) clearTimeout(killTimer);
    if (capacitySafetyKillTimer) clearTimeout(capacitySafetyKillTimer);
    if (safetyInterval) clearInterval(safetyInterval);
    options.signal?.removeEventListener("abort", abortHandler);
    sampling = false;
    clearInterval(interval);
    await sampleRSS();
    if (resourceSampler && !resourceSamplerStopped) {
      result.resourceMetrics = await resourceSampler.stop();
      resourceSamplerStopped = true;
    }
    status = outcome.error ? 1 : (outcome.exitCode ?? 1);
    if (capacitySafetyStop) status = 1;
    signal = outcome.signal ?? null;
    try {
      timeMetrics = parseTimeFile(await fs.readFile(timePath, "utf8"));
    } catch {
      // Some platforms do not provide GNU time; process sampling remains valid.
    }
    output = stdout.value;
    errorOutput = stderr.value;
    const failureDiagnostic = `${errorOutput.tailPreview}\n${output.tailPreview}`;
    failureSummary = status === 0 ? null : capacitySafetyStop ?? summarizeFailure(failureDiagnostic || `exit=${outcome.exitCode ?? "null"} signal=${outcome.signal ?? "none"}`);
  } catch (error) {
    failedReceiptPath = error.resourceReceiptPath ?? null;
    if (error.resourceCleanupErrors?.length) result.cleanupError = error.resourceCleanupErrors.join("; ");
    failureSummary = summarizeFailure(error?.message ?? String(error));
    errorOutput = { bytes: 0, preview: "", tailPreview: failureSummary, truncatedBytes: 0 };
  } finally {
    if (resourceSampler && !resourceSamplerStopped) {
      try {
        result.resourceMetrics = await resourceSampler.stop();
        resourceSamplerStopped = true;
      } catch {
        result.cleanupWarnings.push("capacity resource sampling did not finish; unavailable metrics remain unknown");
      }
    }
    const cleanupOptions = resourceCleanupOptions(taskOptions);
    const containerError = await cleanupContainer(command.containerIDPath);
    if (containerError) result.cleanupError = [result.cleanupError, containerError].filter(Boolean).join("; ");
    if (pool) {
      const poolErrors = await cleanupServicePool(pool, cleanupOptions);
      if (poolErrors.length > 0) result.cleanupError = [result.cleanupError, ...poolErrors].filter(Boolean).join("; ");
    }
    if (pool || failedReceiptPath || task.servicePool || task.requiresDocker || task.usesDocker) {
      const resourceErrors = await cleanupRunResourceReceipts(cleanupOptions, pool?.receiptPath ?? failedReceiptPath);
      if (resourceErrors.length > 0) result.cleanupError = [result.cleanupError, ...resourceErrors].filter(Boolean).join("; ");
    }
  }
  if (task.capacityReport) {
    try {
      result.capacityReport = await readCapacityReport(path.join(taskDirectory, "capacity-report.json"), options.runID ?? path.basename(options.runDirectory));
    } catch (error) {
      if (status === 0) {
        status = 1;
        failureSummary = `capacity report invalid or missing: ${scrub(error.message ?? String(error))}`;
      } else {
        result.capacityReportFailure = scrub(error.message ?? String(error));
      }
    }
  }
  const endedAt = performance.now();
  result.startedAtMs = Math.round(startedAt);
  result.endedAtMs = Math.round(endedAt);
  result.status = status;
  result.signal = signal;
  result.timedOut = timedOut;
  result.wallTimeMs = Math.round(endedAt - startedAt);
  result.peakRssMiB = Math.max(peakRSS / (1024 * 1024), timeMetrics.peakRssMiB ?? 0);
  result.cpuMs = timeMetrics.cpuMs ?? 0;
  result.stdoutBytes = output.bytes;
  result.stderrBytes = errorOutput.bytes;
  result.truncatedOutputBytes = output.truncatedBytes + errorOutput.truncatedBytes;
  result.stdoutPreview = task.tier === "T5" || task.network === "public" ? "[suppressed]" : output.preview;
  result.stderrPreview = task.tier === "T5" || task.network === "public" ? "[suppressed]" : errorOutput.preview;
  result.failureSummary = status === 0 ? null : failureSummary;
  result.failureDetail = status === 0 ? null : failureDetail(task, output, errorOutput);
  try {
    await fs.rm(taskDirectory, { recursive: true, force: true });
  } catch (error) {
    result.cleanupError = scrub(error.message);
  }
  return result;
}

const MAX_CAPACITY_REPORT_BYTES = 1 << 20;

async function readCapacityReport(filename, expectedRunID) {
  if (!Number.isInteger(fsConstants.O_NOFOLLOW) || typeof process.getuid !== "function") {
    throw new Error("capacity report ownership and no-follow checks are unavailable on this platform");
  }
  const file = await fs.open(filename, fsConstants.O_RDONLY | fsConstants.O_NOFOLLOW);
  let content;
  try {
    const metadata = await file.stat();
    if (!metadata.isFile() || metadata.uid !== process.getuid() || (metadata.mode & 0o077) !== 0 || metadata.size > MAX_CAPACITY_REPORT_BYTES) {
      throw new Error("capacity report file type, ownership, permissions, or size is invalid");
    }
    const bounded = Buffer.alloc(MAX_CAPACITY_REPORT_BYTES + 1);
    const { bytesRead } = await file.read(bounded, 0, bounded.length, 0);
    const afterRead = await file.stat();
    if (bytesRead !== metadata.size || afterRead.size !== metadata.size || bytesRead > MAX_CAPACITY_REPORT_BYTES) {
      throw new Error("capacity report changed while being read or exceeded its byte limit");
    }
    content = bounded.subarray(0, bytesRead);
  } finally {
    await file.close();
  }
  if (content.byteLength > MAX_CAPACITY_REPORT_BYTES) throw new Error("capacity report exceeds its byte limit");
  const report = JSON.parse(content.toString("utf8"));
  if (!report || report.schemaVersion !== 1 || report.reportKind !== "harden-llm-capacity.v1" ||
      report.testRunId !== expectedRunID || !["correctness", "exploration", "holdout"].includes(report.caseSet) ||
      !Array.isArray(report.testIds) || !report.testIds.includes("TEST-277") || !Array.isArray(report.cases) || report.cases.length === 0) {
    throw new Error("capacity report identity or contents are invalid");
  }
  return report;
}

function resourceAvailable(task, resourceClasses, state, candidateSlots) {
  const definition = resourceClasses[task.resourceClass];
  if (!definition) throw new Error(`task ${task.id} references unknown resource ${task.resourceClass}`);
  const limit = task.resourceClass === "cpu" && candidateSlots ? candidateSlots : definition.slots;
  if (definition.exclusive && state.running.size > 0) return false;
  for (const runningTask of state.running.values()) if (resourceClasses[runningTask.resourceClass].exclusive) return false;
  return (state.used.get(task.resourceClass) ?? 0) < limit;
}

function acquire(task, state) {
  state.used.set(task.resourceClass, (state.used.get(task.resourceClass) ?? 0) + 1);
  state.running.set(task.id, task);
}

function release(task, state) {
  state.used.set(task.resourceClass, Math.max(0, (state.used.get(task.resourceClass) ?? 1) - 1));
  state.running.delete(task.id);
}

function cancelledResult(task, reason) {
  return {
    taskId: task.id,
    tier: task.tier,
    resourceClass: task.resourceClass,
    command: task.command.map((part) => scrub(part)),
    status: 125,
    signal: "SIGTERM",
    timedOut: false,
    wallTimeMs: 0,
    peakRssMiB: 0,
    cpuMs: 0,
    stdoutBytes: 0,
    stderrBytes: 0,
    truncatedOutputBytes: 0,
    stdoutPreview: "",
    stderrPreview: "",
    failureSummary: reason,
    failureDetail: null,
    cleanupError: null,
    cleanupWarnings: [],
    servicePoolStarted: false,
    servicePoolProject: null,
    startedAtMs: null,
    endedAtMs: null,
  };
}

export async function runTasks(tasks, options) {
  if (!options.allowBrowser && tasks.some(task => task.requiresBrowser)) {
    throw new Error("Explicit browser authorization required");
  }
  const resourceClasses = options.resourceClasses ?? {};
  validateTaskGraph(tasks, resourceClasses);
  const runDirectory = options.runDirectory ?? path.join(DEFAULT_RUN_ROOT, `run-${Date.now()}-${process.pid}-${Math.random().toString(36).slice(2, 8)}`);
  await fs.mkdir(runDirectory, { recursive: true, mode: 0o700 });
  const resourceDirectory = options.resourceDirectory
    ?? options.environment?.HARDEN_LLM_TEST_RESOURCE_DIR
    ?? defaultResourceDirectory();
  const runOptions = { ...options, resourceDirectory, cleanupWarnings: [] };
  const lifecycleState = { cleanupDeadline: null };
  const lifecycleTimings = { dockerIdentityMs: null, daemonLockWaitMs: null, staleReceiptRecoveryMs: null };
  let daemonLockLease = null;
  let daemonLockCleanupError = null;
  let preflightFailure = null;
  if (!options.signal?.aborted && tasks.some(isDockerManagedTask)) {
    try {
      const dockerEnvironment = { ...process.env, ...(options.environment ?? {}) };
      const identityStartedAt = performance.now();
      let daemonId;
      try { daemonId = await establishLocalDockerIdentity(options.root, dockerEnvironment, options.signal); }
      finally { lifecycleTimings.dockerIdentityMs = Math.round(performance.now() - identityStartedAt); }
      const lockWaitStartedAt = performance.now();
      let needsRecovery = false;
      try {
        daemonLockLease = await inheritedDaemonLockLease({ daemonId, resourceDirectory, environment: dockerEnvironment });
        if (!daemonLockLease) {
          daemonLockLease = await acquireDaemonLock({
            daemonId,
            resourceDirectory,
            waitMs: options.daemonLockWaitMs ?? 30_000,
            environment: dockerEnvironment,
            signal: options.signal,
          });
          needsRecovery = true;
        }
      } finally {
        lifecycleTimings.daemonLockWaitMs = Math.round(performance.now() - lockWaitStartedAt);
      }
      if (needsRecovery) {
        const recoveryStartedAt = performance.now();
        let recoveryErrors;
        try {
          recoveryErrors = await recoverStaleDaemonReceipts(daemonId, {
            ...runOptions,
            daemonLockLease,
            taskCleanupState: { cleanupDeadline: null },
          });
        } finally {
          lifecycleTimings.staleReceiptRecoveryMs = Math.round(performance.now() - recoveryStartedAt);
        }
        if (recoveryErrors.length > 0) throw new Error(`Docker resource recovery blocked before task execution: ${recoveryErrors.join("; ")}`);
      }
      runOptions.daemonLockLease = daemonLockLease;
    } catch (error) {
      if (lifecycleTimings.dockerIdentityMs === null) lifecycleTimings.dockerIdentityMs = 0;
      preflightFailure = scrub(error.message ?? String(error));
      try { await releaseDaemonLock(daemonLockLease); }
      catch (releaseError) { preflightFailure += `; daemon lock release failed: ${scrub(releaseError.message)}`; }
      daemonLockLease = null;
    }
  }
  const pending = new Set(tasks.map((task) => task.id));
  const byID = new Map(tasks.map((task) => [task.id, task]));
  const results = new Map();
  const state = { running: new Map(), used: new Map() };
  const controller = new AbortController();
  const abortWithCleanupDeadline = () => {
    lifecycleState.cleanupDeadline ??= performance.now() + DEFAULT_RESOURCE_CLEANUP_MS;
    controller.abort();
  };
  const externalAbort = abortWithCleanupDeadline;
  options.signal?.addEventListener("abort", externalAbort, { once: true });
  if (options.signal?.aborted) externalAbort();
  let firstFailure = preflightFailure ? {
    taskId: tasks[0]?.id ?? "docker-preflight",
    status: 1,
    failureSummary: `Docker preflight failed: ${preflightFailure}`,
    failureDetail: null,
  } : null;
  let graphError = null;

  if (preflightFailure) {
    for (const task of tasks) results.set(task.id, cancelledResult(task, `Docker preflight failed; task not started: ${preflightFailure}`));
    pending.clear();
  }

  try {
    while (pending.size > 0 || state.running.size > 0) {
      let launched = false;
      for (const taskID of [...pending]) {
        const task = byID.get(taskID);
        const dependencyResults = (task.dependsOn ?? []).map((id) => results.get(id)).filter(Boolean);
        if (dependencyResults.some((result) => result.status !== 0 || result.cleanupError)) {
          results.set(task.id, cancelledResult(task, "dependency failed; task not started"));
          pending.delete(task.id);
          continue;
        }
        if ((task.dependsOn ?? []).some((id) => !results.has(id))) continue;
        if (controller.signal.aborted || firstFailure || !resourceAvailable(task, resourceClasses, state, options.candidateSlots)) continue;
        pending.delete(task.id);
        acquire(task, state);
        launched = true;
        runCommand(task, {
          ...runOptions,
          lifecycleState,
          packageSlots: options.packageSlotsByTask?.[task.id] ?? options.packageSlots,
          runDirectory,
          runID: options.runID ?? path.basename(runDirectory),
          signal: controller.signal,
        }).then((result) => {
          results.set(task.id, result);
          release(task, state);
          if ((result.status !== 0 || result.cleanupError) && !firstFailure) {
            firstFailure = result.status !== 0 ? result : {
              ...result,
              status: 1,
              failureSummary: `cleanup failed: ${result.cleanupError}`,
            };
            abortWithCleanupDeadline();
          }
        }).catch((error) => {
          const result = cancelledResult(task, scrub(error.message));
          result.status = 1;
          results.set(task.id, result);
          release(task, state);
          if (!firstFailure) {
            firstFailure = result;
            abortWithCleanupDeadline();
          }
        });
      }
      if (controller.signal.aborted || firstFailure) {
        const reason = firstFailure ? "cancelled after first causal failure" : "cancelled after external signal";
        for (const taskID of pending) results.set(taskID, cancelledResult(byID.get(taskID), reason));
        pending.clear();
      }
      if (state.running.size > 0) await new Promise((resolve) => setTimeout(resolve, launched ? 10 : 50));
      else if (pending.size > 0 && !launched) {
        graphError = new Error("task graph cannot make progress");
        break;
      }
    }
  } finally {
    options.signal?.removeEventListener("abort", externalAbort);
    if (state.running.size > 0) abortWithCleanupDeadline();
    while (state.running.size > 0) await new Promise((resolve) => setTimeout(resolve, 10));
    try { await releaseDaemonLock(daemonLockLease); }
    catch (error) { daemonLockCleanupError = scrub(error.message); }
  }

  const orderedResults = tasks.map((task) => results.get(task.id) ?? cancelledResult(task, "task did not produce a result"));
  const cleanupErrors = [];
  const cleanupWarnings = [
    ...runOptions.cleanupWarnings,
    ...orderedResults.flatMap((result) => result.cleanupWarnings ?? []),
  ];
  for (const result of orderedResults) {
    if (result.cleanupError) cleanupErrors.push(`${result.taskId}: ${result.cleanupError}`);
  }
  try {
    await fs.rm(runDirectory, { recursive: true, force: true });
  } catch (error) {
    cleanupErrors.push(scrub(error.message));
  }
  if (graphError) cleanupErrors.push(graphError.message);
  if (daemonLockCleanupError) cleanupErrors.push(`daemon lock release: ${daemonLockCleanupError}`);
  return {
    accepted: !graphError && firstFailure === null && orderedResults.every((result) => result.status === 0 && !result.cleanupError) && cleanupErrors.length === 0,
    runDirectory,
    results: orderedResults,
    firstFailure: firstFailure ? {
      taskId: firstFailure.taskId,
      status: firstFailure.status,
      failureSummary: firstFailure.failureSummary,
      failureDetail: firstFailure.failureDetail,
    } : null,
    preflightFailure,
    lifecycleTimings,
    cleanupErrors,
    cleanupWarnings,
  };
}

export async function runSelection({ manifest, selector, root = REPOSITORY_ROOT, seed = DEFAULT_SEED, candidateSlots, signal, runID, allowBrowser = false }) {
  const tasks = selectTasks(manifest, selector);
  if (!allowBrowser && tasks.some(task => task.requiresBrowser)) {
    throw new Error("Explicit browser authorization required: pass --allow-browser only when requested");
  }
  const runDirectory = path.join(root, "tmp", "test-feedback", runID ?? `run-${Date.now()}-${process.pid}-${Math.random().toString(36).slice(2, 8)}`);
  const result = await runTasks(tasks, {
    allowBrowser,
    root,
    resourceClasses: manifest.resourceClasses,
    seed,
    candidateSlots,
    signal,
    runID: runID ?? path.basename(runDirectory),
    runDirectory,
  });
  return { schemaVersion: 1, selector, seed, manifestSHA256: manifest.__manifestSHA256 ?? null, ...result };
}

export async function writeRunReport(report, reportPath) {
  if (typeof reportPath !== "string" || reportPath.trim() === "") throw new Error("run report path is required");
  const resolvedPath = path.resolve(reportPath);
  const payload = `${JSON.stringify(report, null, 2)}\n`;
  const payloadBytes = Buffer.byteLength(payload, "utf8");
  if (payloadBytes > MAX_RUN_REPORT_BYTES) throw new Error(`run report exceeds the ${MAX_RUN_REPORT_BYTES}-byte limit (${payloadBytes} bytes)`);
  const directory = path.dirname(resolvedPath);
  await fs.mkdir(directory, { recursive: true, mode: 0o700 });
  const temporaryPath = path.join(directory, `.${path.basename(resolvedPath)}.${process.pid}.${randomBytes(8).toString("hex")}.tmp`);
  const handle = await fs.open(temporaryPath, fsConstants.O_CREAT | fsConstants.O_EXCL | fsConstants.O_WRONLY, 0o600);
  let writeError = null;
  try {
    await handle.writeFile(payload, "utf8");
    await handle.sync();
  } catch (error) {
    writeError = error;
  }
  try { await handle.close(); }
  catch (error) { writeError ??= error; }
  if (writeError) {
    await fs.unlink(temporaryPath).catch(() => {});
    throw writeError;
  }
  try {
    await fs.rename(temporaryPath, resolvedPath);
    const directoryHandle = await fs.open(directory, "r");
    try { await directoryHandle.sync(); } finally { await directoryHandle.close(); }
  } catch (error) {
    await fs.unlink(temporaryPath).catch(() => {});
    throw error;
  }
  return resolvedPath;
}

function parseArgs(argv) {
  const result = { manifest: path.join(REPOSITORY_ROOT, "test", "test-tiers.json"), root: REPOSITORY_ROOT, selector: "fast", seed: DEFAULT_SEED };
  for (let index = 0; index < argv.length; index += 1) {
    const argument = argv[index];
    if (argument === "--help") return { help: true };
    if (argument === "--allow-browser") { result.allowBrowser = true; continue; }
    if (!argument.startsWith("--")) throw new Error(`unexpected argument ${argument}`);
    const name = argument.slice(2);
    const value = argv[++index];
    if (value === undefined || value.startsWith("--")) throw new Error(`missing value for --${name}`);
    if (name === "manifest") result.manifest = path.resolve(value);
    else if (name === "root") result.root = path.resolve(value);
    else if (name === "task") result.selector = value;
    else if (name === "seed") result.seed = Number(value);
    else if (name === "candidate-slots") result.candidateSlots = Number(value);
    else if (name === "output") result.output = path.resolve(value);
    else if (name === "run-id") result.runID = value;
    else throw new Error(`unknown option --${name}`);
  }
  if (!Number.isInteger(result.seed) || result.seed <= 0) throw new Error("--seed must be a positive integer");
  if (result.candidateSlots !== undefined && (!Number.isInteger(result.candidateSlots) || result.candidateSlots <= 0)) throw new Error("--candidate-slots must be a positive integer");
  return result;
}

function usage() {
  return "Usage: node scripts/run-test-tier.mjs --task fast|browser|release|live [--allow-browser] [--manifest PATH] [--root PATH] [--output PATH] [--seed N] [--candidate-slots N]";
}

export async function main(argv = process.argv.slice(2)) {
  const args = parseArgs(argv);
  if (args.help) {
    console.log(usage());
    return 0;
  }
  const manifest = await loadManifest(args.manifest);
  manifest.__manifestSHA256 = await sha256File(args.manifest);
  const controller = new AbortController();
  const onSignal = () => controller.abort();
  process.once("SIGINT", onSignal);
  process.once("SIGTERM", onSignal);
  try {
    const result = await runSelection({ ...args, manifest, signal: controller.signal });
    const reportPath = args.output ?? path.join(args.root, "tmp", "test-feedback", `runner-${Date.now()}-${process.pid}-${randomBytes(8).toString("hex")}.json`);
    const output = { ...result, generatedAt: new Date().toISOString(), reportPath };
    await writeRunReport(output, reportPath);
    console.log(JSON.stringify({ accepted: output.accepted, selector: output.selector, taskCount: output.results.length, failure: output.firstFailure, cleanupErrors: output.cleanupErrors, cleanupWarnings: output.cleanupWarnings, output: reportPath }));
    return output.accepted ? 0 : 1;
  } finally {
    process.removeListener("SIGINT", onSignal);
    process.removeListener("SIGTERM", onSignal);
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().then((code) => { process.exitCode = code; }).catch((error) => {
    console.error(scrub(error.stack ?? error.message));
    process.exitCode = 1;
  });
}
