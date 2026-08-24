#!/usr/bin/env node

import { spawn, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { promises as fs } from "node:fs";
import os from "node:os";
import path from "node:path";
import { performance } from "node:perf_hooks";
import { fileURLToPath } from "node:url";

const REPOSITORY_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const DEFAULT_RUN_ROOT = path.join(REPOSITORY_ROOT, "tmp", "test-feedback");
const DEFAULT_SEED = 104729;
const MAX_CAPTURE_BYTES = 8192;
const TASK_TIMEOUT_GRACE_MS = 2_000;

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
      return { bytes, preview: scrub(first), tailPreview: scrub(tail), truncatedBytes };
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
    HARDEN_LLM_BENCHMARK_COLD: options.cold ? "1" : "0",
  };
  if (task.network === "forbidden") {
    environment.HARDEN_LLM_TEST_NETWORK = "forbidden";
    environment.HARDEN_LLM_TEST_OFFLINE = "1";
  }
  return environment;
}

function resolvedCommand(task, options) {
  const effective = task.testSeed ? [...task.command, "--seed", String(task.testSeed)] : [...task.command];
  if (!task.container) {
    return {
      executable: effective[0],
      args: effective.slice(1),
      cwd: path.resolve(options.root, task.workingDirectory ?? "."),
      environment: resolvedEnvironment(task, options),
      containerIDPath: null,
    };
  }
  const containerIDPath = path.join(options.taskDirectory, "container.id");
  const commandText = effective.map(shellQuote).join(" ");
  const bootstrap = task.container.bootstrap
    ? "mix local.hex --force >/dev/null 2>&1 && mix local.rebar --force >/dev/null 2>&1 && mix deps.get >/dev/null && "
    : "";
  const containerEnvironment = {
    HARDEN_LLM_TEST_SEED: String(options.seed ?? DEFAULT_SEED),
    HARDEN_LLM_TEST_RUN_ID: options.runID ?? path.basename(options.runDirectory),
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
  args.push("--cidfile", containerIDPath, "-v", `${options.root}:/workspace`, "-w", `/workspace/${task.workingDirectory ?? "."}`, task.container.image, "sh", "-lc", `${bootstrap}${commandText}`);
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
  const useGNUTime = process.platform === "linux" && await exists("/usr/bin/time");
  const executable = useGNUTime ? "/usr/bin/time" : command.executable;
  const args = useGNUTime ? ["-v", "-o", timePath, "--", command.executable, ...command.args] : command.args;
  const startedAt = performance.now();
  const child = spawn(executable, args, {
    cwd: command.cwd,
    env: command.environment,
    stdio: ["ignore", "pipe", "pipe"],
    detached: process.platform !== "win32",
  });
  child.stdout.on("data", (chunk) => stdout.append(chunk));
  child.stderr.on("data", (chunk) => stderr.append(chunk));

  let peakRSS = 0;
  let timedOut = false;
  let sampling = true;
  let samplingInFlight = false;
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
    child.once("close", (exitCode, signal) => resolve({ exitCode, signal }));
  });
  clearTimeout(timeoutTimer);
  if (killTimer) clearTimeout(killTimer);
  options.signal?.removeEventListener("abort", abortHandler);
  sampling = false;
  clearInterval(interval);
  await sampleRSS();
  let timeMetrics = {};
  try {
    timeMetrics = parseTimeFile(await fs.readFile(timePath, "utf8"));
  } catch {
    // Some platforms do not provide GNU time; process sampling remains valid.
  }
  const status = outcome.error ? 1 : (outcome.exitCode ?? 1);
  const endedAt = performance.now();
  const output = stdout.value;
  const errorOutput = stderr.value;
  const failureDiagnostic = `${errorOutput.tailPreview}\n${output.tailPreview}`.split("\n").map((line) => line.trim()).filter(Boolean).pop();
  const result = {
    taskId: task.id,
    tier: task.tier,
    resourceClass: task.resourceClass,
    command: [command.executable, ...command.args].map((part) => scrub(part)),
    status,
    seed: options.seed ?? DEFAULT_SEED,
    cold: Boolean(options.cold),
    signal: outcome.signal ?? null,
    timedOut,
    wallTimeMs: Math.round(endedAt - startedAt),
    peakRssMiB: Math.max(peakRSS / (1024 * 1024), timeMetrics.peakRssMiB ?? 0),
    cpuMs: timeMetrics.cpuMs ?? 0,
    stdoutBytes: output.bytes,
    stderrBytes: errorOutput.bytes,
    truncatedOutputBytes: output.truncatedBytes + errorOutput.truncatedBytes,
    stdoutPreview: task.tier === "T5" || task.network === "public" ? "[suppressed]" : output.preview,
    stderrPreview: task.tier === "T5" || task.network === "public" ? "[suppressed]" : errorOutput.preview,
    failureSummary: status === 0 ? null : scrub(failureDiagnostic ?? `exit=${outcome.exitCode ?? "null"} signal=${outcome.signal ?? "none"}`).slice(0, 240),
    cleanupError: null,
  };
  const containerError = await cleanupContainer(command.containerIDPath);
  if (containerError) result.cleanupError = containerError;
  try {
    await fs.rm(taskDirectory, { recursive: true, force: true });
  } catch (error) {
    result.cleanupError = scrub(error.message);
  }
  return result;
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
    cleanupError: null,
  };
}

export async function runTasks(tasks, options) {
  const resourceClasses = options.resourceClasses ?? {};
  validateTaskGraph(tasks, resourceClasses);
  const runDirectory = options.runDirectory ?? path.join(DEFAULT_RUN_ROOT, `run-${Date.now()}-${process.pid}-${Math.random().toString(36).slice(2, 8)}`);
  await fs.mkdir(runDirectory, { recursive: true, mode: 0o700 });
  const pending = new Set(tasks.map((task) => task.id));
  const byID = new Map(tasks.map((task) => [task.id, task]));
  const results = new Map();
  const state = { running: new Map(), used: new Map() };
  const controller = new AbortController();
  const externalAbort = () => controller.abort();
  options.signal?.addEventListener("abort", externalAbort, { once: true });
  let firstFailure = null;
  let graphError = null;

  try {
    while (pending.size > 0 || state.running.size > 0) {
      let launched = false;
      for (const taskID of [...pending]) {
        const task = byID.get(taskID);
        const dependencyResults = (task.dependsOn ?? []).map((id) => results.get(id)).filter(Boolean);
        if (dependencyResults.some((result) => result.status !== 0)) {
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
          ...options,
          runDirectory,
          runID: options.runID ?? path.basename(runDirectory),
          signal: controller.signal,
        }).then((result) => {
          results.set(task.id, result);
          release(task, state);
          if (result.status !== 0 && !firstFailure) {
            firstFailure = result;
            controller.abort();
          }
        }).catch((error) => {
          const result = cancelledResult(task, scrub(error.message));
          result.status = 1;
          results.set(task.id, result);
          release(task, state);
          if (!firstFailure) {
            firstFailure = result;
            controller.abort();
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
    if (state.running.size > 0) controller.abort();
    while (state.running.size > 0) await new Promise((resolve) => setTimeout(resolve, 10));
  }

  const orderedResults = tasks.map((task) => results.get(task.id) ?? cancelledResult(task, "task did not produce a result"));
  const cleanupErrors = [];
  try {
    await fs.rm(runDirectory, { recursive: true, force: true });
  } catch (error) {
    cleanupErrors.push(scrub(error.message));
  }
  if (graphError) cleanupErrors.push(graphError.message);
  return {
    accepted: !graphError && firstFailure === null && orderedResults.every((result) => result.status === 0) && cleanupErrors.length === 0,
    runDirectory,
    results: orderedResults,
    firstFailure: firstFailure ? { taskId: firstFailure.taskId, status: firstFailure.status, failureSummary: firstFailure.failureSummary } : null,
    cleanupErrors,
  };
}

export async function runSelection({ manifest, selector, root = REPOSITORY_ROOT, seed = DEFAULT_SEED, candidateSlots, signal, runID }) {
  const tasks = selectTasks(manifest, selector);
  const runDirectory = path.join(root, "tmp", "test-feedback", runID ?? `run-${Date.now()}-${process.pid}-${Math.random().toString(36).slice(2, 8)}`);
  const result = await runTasks(tasks, {
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

function parseArgs(argv) {
  const result = { manifest: path.join(REPOSITORY_ROOT, "test", "test-tiers.json"), root: REPOSITORY_ROOT, selector: "fast", seed: DEFAULT_SEED };
  for (let index = 0; index < argv.length; index += 1) {
    const argument = argv[index];
    if (argument === "--help") return { help: true };
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
  return "Usage: node scripts/run-test-tier.mjs --task fast|browser|release|live [--manifest PATH] [--root PATH] [--output PATH] [--seed N] [--candidate-slots N]";
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
    const output = { ...result, generatedAt: new Date().toISOString() };
    if (args.output) {
      await fs.mkdir(path.dirname(args.output), { recursive: true });
      const temporaryPath = `${args.output}.writing`;
      await fs.writeFile(temporaryPath, `${JSON.stringify(output, null, 2)}\n`, { mode: 0o600 });
      await fs.rename(temporaryPath, args.output);
    }
    console.log(JSON.stringify({ accepted: output.accepted, selector: output.selector, taskCount: output.results.length, failure: output.firstFailure, cleanupErrors: output.cleanupErrors, output: args.output ?? null }));
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
