#!/usr/bin/env node

import { spawn, execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { promises as fs } from "node:fs";
import os from "node:os";
import path from "node:path";
import { performance } from "node:perf_hooks";
import { fileURLToPath } from "node:url";

const REPOSITORY_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const MAX_CAPTURE_BYTES = 8192;
const SAMPLE_INTERVAL_MS = 50;
const DEFAULT_SEED = 104729;
// The benchmark contract uses GNU time -v and bounded /proc process-tree sampling.

export function parseArgs(argv) {
  const result = {
    mode: "baseline",
    task: null,
    warmSamples: 5,
    coldSamples: 3,
    output: null,
    compare: null,
    verifyBaseline: null,
    seeds: [DEFAULT_SEED],
    candidateSlots: null,
  };

  for (let index = 0; index < argv.length; index += 1) {
    const argument = argv[index];
    if (!argument.startsWith("--")) {
      throw new Error(`unexpected argument ${argument}`);
    }
    const name = argument.slice(2);
    if (name === "help") {
      result.help = true;
      continue;
    }
    const value = argv[index + 1];
    if (value === undefined || value.startsWith("--")) {
      throw new Error(`missing value for --${name}`);
    }
    index += 1;
    if (name === "mode") result.mode = value;
    else if (name === "task") result.task = value;
    else if (name === "warm-samples") result.warmSamples = nonNegativeInteger(value, name);
    else if (name === "cold-samples") result.coldSamples = nonNegativeInteger(value, name);
    else if (name === "output") result.output = value;
    else if (name === "compare") result.compare = value;
    else if (name === "verify-baseline") result.verifyBaseline = value;
    else if (name === "seeds") result.seeds = value.split(",").filter(Boolean).map((seed) => positiveInteger(seed, "seed"));
    else if (name === "candidate-slots") result.candidateSlots = positiveInteger(value, name);
    else throw new Error(`unknown option --${name}`);
  }
  return result;
}

function positiveInteger(value, name) {
  const parsed = Number(value);
  if (!Number.isInteger(parsed) || parsed <= 0) throw new Error(`--${name} must be a positive integer`);
  return parsed;
}

function nonNegativeInteger(value, name) {
  const parsed = Number(value);
  if (!Number.isInteger(parsed) || parsed < 0) throw new Error(`--${name} must be a non-negative integer`);
  return parsed;
}

export function selectTasks(manifest, selector) {
  const byId = new Map(manifest.tasks.map((task) => [task.id, task]));
  const direct = byId.get(selector);
  const selected = new Map();

  const addWithDependencies = (task) => {
    if (selected.has(task.id)) return;
    selected.set(task.id, task);
    for (const dependency of task.dependsOn ?? []) {
      const dependencyTask = byId.get(dependency);
      if (!dependencyTask) throw new Error(`task ${task.id} depends on unknown task ${dependency}`);
      addWithDependencies(dependencyTask);
    }
  };

  if (direct) addWithDependencies(direct);
  else {
    const group = manifest.tasks.filter((task) => (task.requiredFor ?? []).includes(selector));
    if (group.length === 0) throw new Error(`no tasks selected for ${selector}`);
    for (const task of group) addWithDependencies(task);
  }
  return [...selected.values()];
}

function baselineLanes(manifest) {
  const lanes = [
    ["fast-candidates", ["go-static", "go-unit", "go-parity", "go-api", "go-observability", "frontend-deterministic"]],
    ["integration", ["go-integration"]],
    ["browser", ["frontend-browser"]],
    ["full-system", ["backend-verify-baseline"]],
  ];
  const byId = new Map(manifest.tasks.map((task) => [task.id, task]));
  return lanes.map(([id, taskIds]) => {
    const tasks = taskIds.map((taskId) => {
      const task = byId.get(taskId);
      if (!task) throw new Error(`baseline lane ${id} references unknown task ${taskId}`);
      return task;
    });
    return { id, tasks };
  });
}

async function loadJSON(filePath) {
  return JSON.parse(await fs.readFile(filePath, "utf8"));
}

async function sha256File(filePath) {
  return createHash("sha256").update(await fs.readFile(filePath)).digest("hex");
}

function commandOutput(command, args, timeout = 3000) {
  try {
    return execFileSync(command, args, {
      cwd: REPOSITORY_ROOT,
      encoding: "utf8",
      timeout,
      stdio: ["ignore", "pipe", "ignore"],
    }).trim();
  } catch {
    return "unavailable";
  }
}

export function collectHostFingerprint() {
  const kernel = commandOutput("uname", ["-r"]);
  const architecture = commandOutput("uname", ["-m"]);
  const physicalCpuCount = Number(commandOutput("sh", ["-c", "lscpu -p=CORE,SOCKET 2>/dev/null | grep -v '^#' | sort -u | wc -l"])) || os.cpus().length;
  const logicalCpuCount = os.cpus().length;
  return {
    os: `${process.platform}-${kernel}-${architecture}`,
    physicalCpuCount,
    logicalCpuCount,
    memoryMiB: Math.round(os.totalmem() / (1024 * 1024)),
    goVersion: commandOutput("go", ["version"]),
    nodeVersion: process.version,
    dockerVersion: commandOutput("docker", ["version", "--format", "{{.Server.Version}}"]),
    composeVersion: commandOutput("docker", ["compose", "version", "--short"]),
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

function boundedCapture() {
  let bytes = 0;
  let text = "";
  let tailText = "";
  let truncatedBytes = 0;
  return {
    append(chunk) {
      const chunkBytes = Buffer.byteLength(chunk);
      bytes += chunkBytes;
      if (Buffer.byteLength(text) < MAX_CAPTURE_BYTES) {
        const remaining = MAX_CAPTURE_BYTES - Buffer.byteLength(text);
        text += Buffer.from(chunk).subarray(0, remaining).toString("utf8");
      }
      tailText = (tailText + String(chunk)).slice(-MAX_CAPTURE_BYTES);
      truncatedBytes = Math.max(0, bytes - MAX_CAPTURE_BYTES);
    },
    get value() {
      return { bytes, preview: scrub(text), tailPreview: scrub(tailText), truncatedBytes };
    },
  };
}

async function readProcessMemory(pid) {
  try {
    const status = await fs.readFile(`/proc/${pid}/status`, "utf8");
    const match = status.match(/^VmRSS:\s+(\d+)\s+kB$/m);
    return match ? Number(match[1]) * 1024 : 0;
  } catch {
    return 0;
  }
}

async function childPids(pid) {
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
    pending.push(...await childPids(current));
  }
  return result;
}

async function processTreeRSS(pid) {
  let bytes = 0;
  for (const processID of await processTree(pid)) bytes += await readProcessMemory(processID);
  return bytes;
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

async function readContainerMetrics(containerIDPath) {
  try {
    const containerID = (await fs.readFile(containerIDPath, "utf8")).trim();
    if (!containerID) return { rssBytes: 0, cpuMs: 0 };
    const cgroupRoots = [
      `/sys/fs/cgroup/system.slice/docker-${containerID}.scope`,
      `/sys/fs/cgroup/docker/${containerID}`,
    ];
    let rssBytes = 0;
    let cpuMs = 0;
    for (const root of cgroupRoots) {
      for (const filename of ["memory.current", "memory.usage_in_bytes"]) {
        try {
          const value = Number((await fs.readFile(`${root}/${filename}`, "utf8")).trim());
          if (Number.isFinite(value) && value > 0) rssBytes = Math.max(rssBytes, value);
        } catch {
          // The container runtime may expose a different cgroup layout.
        }
      }
      try {
        const cpuStat = await fs.readFile(`${root}/cpu.stat`, "utf8");
        const usageMatch = cpuStat.match(/^usage_usec\s+(\d+)$/m);
        if (usageMatch) cpuMs = Math.max(cpuMs, Number(usageMatch[1]) / 1000);
      } catch {
        // The container runtime may expose a different cgroup layout.
      }
    }
    return { rssBytes, cpuMs };
  } catch {
    // The cidfile is not available until Docker has created the container.
    return { rssBytes: 0, cpuMs: 0 };
  }
}

function failureSummary(task, exitCode, signal, stderr, stdout) {
  if (task.network === "public" || task.tier === "T5") return "task failed; live output suppressed";
  const diagnostic = `${stderr.tailPreview}\n${stdout.tailPreview}`;
  const firstLine = diagnostic.split("\n").map((line) => line.trim()).filter(Boolean).pop();
  return firstLine ? scrub(firstLine).slice(0, 240) : `exit=${exitCode ?? "null"} signal=${signal ?? "none"}`;
}

function shellQuote(value) {
  return `'${String(value).replaceAll("'", "'\\''")}'`;
}

export async function runCommand(task, { root = REPOSITORY_ROOT, seed = DEFAULT_SEED, cold = false, signal } = {}) {
  const runDirectory = await fs.mkdtemp(path.join(os.tmpdir(), "harden-llm-test-feedback-"));
  const timePath = path.join(runDirectory, "time.txt");
  const stdout = boundedCapture();
  const stderr = boundedCapture();
  const startedAt = performance.now();
  let containerIDPath = null;
  let executableCommand = task.command[0];
  const effectiveCommand = task.testSeed ? [...task.command, "--seed", String(task.testSeed)] : task.command;
  let executableArgs = effectiveCommand.slice(1);
  if (task.container) {
    const taskCommand = effectiveCommand.map(shellQuote).join(" ");
    const containerCommand = task.container.bootstrap
      ? "mix local.hex --force >/dev/null 2>&1 && mix local.rebar --force >/dev/null 2>&1 && mix deps.get >/dev/null && " + taskCommand
      : taskCommand;
    executableCommand = "docker";
    executableArgs = ["run", "--rm", "--network", task.container.network ?? "none"];
    if (task.container.shmSize) executableArgs.push("--shm-size", task.container.shmSize);
    if (task.container.dockerSocket) executableArgs.push("-v", "/var/run/docker.sock:/var/run/docker.sock");
    containerIDPath = path.join(runDirectory, "container.id");
    executableArgs.push("--cidfile", containerIDPath);
    executableArgs.push("-v", `${root}:/workspace`, "-w", `/workspace/${task.workingDirectory ?? "."}`, task.container.image, "sh", "-lc", containerCommand);
  }
  const command = process.platform === "linux" && await exists("/usr/bin/time") ? "/usr/bin/time" : executableCommand;
  const commandArgs = command === "/usr/bin/time"
    ? ["-v", "-o", timePath, "--", executableCommand, ...executableArgs]
    : executableArgs;
  const executable = command === "/usr/bin/time" ? command : executableCommand;
  const child = spawn(executable, commandArgs, {
    cwd: path.resolve(root, task.workingDirectory ?? "."),
    env: {
      ...process.env,
      HARDEN_LLM_TEST_SEED: String(seed),
      HARDEN_LLM_BENCHMARK_COLD: cold ? "1" : "0",
    },
    stdio: ["ignore", "pipe", "pipe"],
    detached: false,
  });

  let peakRSS = 0;
  let containerCPU = 0;
  let sampling = true;
  let samplingInFlight = false;
  const sampleMemory = async () => {
    if (!sampling || samplingInFlight || !child.pid) return;
    samplingInFlight = true;
    peakRSS = Math.max(peakRSS, await processTreeRSS(child.pid));
    if (containerIDPath) {
      const containerMetrics = await readContainerMetrics(containerIDPath);
      peakRSS = Math.max(peakRSS, containerMetrics.rssBytes);
      containerCPU = Math.max(containerCPU, containerMetrics.cpuMs);
    }
    samplingInFlight = false;
  };
  const interval = setInterval(sampleMemory, SAMPLE_INTERVAL_MS);
  child.stdout.on("data", (chunk) => stdout.append(chunk));
  child.stderr.on("data", (chunk) => stderr.append(chunk));

  let timedOut = false;
  let timeoutHandle = setTimeout(() => {
    timedOut = true;
    child.kill("SIGTERM");
    setTimeout(() => child.kill("SIGKILL"), 2000).unref();
  }, task.timeoutMs);
  const abortHandler = () => child.kill("SIGTERM");
  signal?.addEventListener("abort", abortHandler, { once: true });

  const outcome = await new Promise((resolve) => {
    child.once("error", (error) => resolve({ error }));
    child.once("close", (exitCode, closeSignal) => resolve({ exitCode, signal: closeSignal }));
  });
  clearTimeout(timeoutHandle);
  signal?.removeEventListener("abort", abortHandler);
  sampling = false;
  clearInterval(interval);
  await sampleMemory();

  let timeMetrics = {};
  try {
    timeMetrics = parseTimeFile(await fs.readFile(timePath, "utf8"));
  } catch {
    timeMetrics = {};
  }
  const endedAt = performance.now();
  const status = outcome.error ? 1 : (outcome.exitCode ?? 1);
  const result = {
    taskId: task.id,
    tier: task.tier,
    command: effectiveCommand.map((part) => scrub(part)),
    seed,
    cold,
    status,
    signal: outcome.signal ?? null,
    timedOut,
    wallTimeMs: Math.round(endedAt - startedAt),
    peakRssMiB: Math.max(peakRSS / (1024 * 1024), timeMetrics.peakRssMiB ?? 0),
    cpuMs: containerCPU > 0 ? containerCPU : (timeMetrics.cpuMs ?? 0),
    stdoutBytes: stdout.value.bytes,
    stderrBytes: stderr.value.bytes,
    truncatedOutputBytes: stdout.value.truncatedBytes + stderr.value.truncatedBytes,
    failureSummary: status === 0 ? null : failureSummary(task, outcome.exitCode, outcome.signal, stderr.value, stdout.value),
    cleanupError: null,
  };
  try {
    await fs.rm(runDirectory, { recursive: true, force: true });
  } catch (error) {
    result.cleanupError = scrub(error.message);
  }
  return result;
}

async function exists(filePath) {
  try {
    await fs.access(filePath);
    return true;
  } catch {
    return false;
  }
}

function resourceAvailable(task, manifest, state, candidateSlots) {
  const definition = manifest.resourceClasses[task.resourceClass];
  if (!definition) throw new Error(`task ${task.id} references unknown resource ${task.resourceClass}`);
  const limit = task.resourceClass === "cpu" && candidateSlots ? candidateSlots : definition.slots;
  if (definition.exclusive && state.running.size > 0) return false;
  for (const runningTask of state.running.values()) {
    const runningDefinition = manifest.resourceClasses[runningTask.resourceClass];
    if (runningDefinition.exclusive) return false;
  }
  return (state.used.get(task.resourceClass) ?? 0) < limit;
}

function acquireResource(task, state) {
  state.used.set(task.resourceClass, (state.used.get(task.resourceClass) ?? 0) + 1);
  state.running.set(task.id, task);
}

function releaseResource(task, state) {
  state.used.set(task.resourceClass, Math.max(0, (state.used.get(task.resourceClass) ?? 1) - 1));
  state.running.delete(task.id);
}

async function runTaskSet(tasks, manifest, options) {
  if (options.parallel === false) {
    const results = [];
    for (const task of tasks) results.push(await runCommand(task, options));
    return results;
  }

  const byId = new Map(tasks.map((task) => [task.id, task]));
  const pending = new Set(tasks.map((task) => task.id));
  const results = new Map();
  const state = { running: new Map(), used: new Map() };
  const abortController = new AbortController();
  let firstFailure = null;

  while (pending.size > 0 || state.running.size > 0) {
    let launched = false;
    for (const taskID of [...pending]) {
      const task = byId.get(taskID);
      const dependencyResults = (task.dependsOn ?? []).map((id) => results.get(id)).filter(Boolean);
      if (dependencyResults.some((result) => result.status !== 0)) {
        results.set(task.id, {
          taskId: task.id,
          tier: task.tier,
          status: 125,
          signal: null,
          timedOut: false,
          wallTimeMs: 0,
          peakRssMiB: 0,
          cpuMs: 0,
          stdoutBytes: 0,
          stderrBytes: 0,
          truncatedOutputBytes: 0,
          failureSummary: "dependency failed; task not started",
          cleanupError: null,
        });
        pending.delete(task.id);
        launched = true;
        continue;
      }
      if ((task.dependsOn ?? []).some((id) => !results.has(id)) || !resourceAvailable(task, manifest, state, options.candidateSlots)) continue;
      pending.delete(task.id);
      acquireResource(task, state);
      launched = true;
      runCommand(task, options)
        .then((result) => {
          results.set(task.id, result);
          releaseResource(task, state);
          if (result.status !== 0 && !firstFailure) {
            firstFailure = result;
            abortController.abort();
          }
        })
        .catch((error) => {
          const result = { taskId: task.id, tier: task.tier, status: 1, failureSummary: scrub(error.message), cleanupError: null };
          results.set(task.id, result);
          releaseResource(task, state);
          if (!firstFailure) {
            firstFailure = result;
            abortController.abort();
          }
        });
    }

    if (firstFailure) {
      for (const taskID of pending) {
        results.set(taskID, {
          taskId: taskID,
          tier: byId.get(taskID).tier,
          status: 125,
          signal: "SIGTERM",
          timedOut: false,
          wallTimeMs: 0,
          peakRssMiB: 0,
          cpuMs: 0,
          stdoutBytes: 0,
          stderrBytes: 0,
          truncatedOutputBytes: 0,
          failureSummary: "cancelled after first causal failure",
          cleanupError: null,
        });
      }
      pending.clear();
    }
    if (state.running.size > 0) {
      await new Promise((resolve) => setTimeout(resolve, launched ? 10 : 50));
    } else if (pending.size > 0 && !launched) {
      throw new Error("task graph cannot make progress; check dependencies or resource slots");
    }
  }
  return tasks.map((task) => results.get(task.id));
}

function numericValues(results, field) {
  return results.map((result) => Number(result[field])).filter((value) => Number.isFinite(value));
}

function percentile(values, fraction) {
  if (values.length === 0) return null;
  const sorted = [...values].sort((left, right) => left - right);
  const index = Math.min(sorted.length - 1, Math.ceil(fraction * sorted.length) - 1);
  return Math.round(sorted[Math.max(0, index)] * 100) / 100;
}

function coefficientOfVariation(values) {
  if (values.length < 2) return null;
  const mean = values.reduce((total, value) => total + value, 0) / values.length;
  if (mean === 0) return null;
  const variance = values.reduce((total, value) => total + ((value - mean) ** 2), 0) / values.length;
  return Math.round((Math.sqrt(variance) / mean) * 10000) / 10000;
}

function metricSummary(values) {
  return {
    p50: percentile(values, 0.5),
    p95: percentile(values, 0.95),
    max: values.length ? Math.max(...values) : null,
    coefficientOfVariation: coefficientOfVariation(values),
  };
}

export function aggregate(results, { parallel = false } = {}) {
  const taskWall = numericValues(results, "wallTimeMs");
  const wall = taskWall.length === 0 ? [] : [parallel ? Math.max(...taskWall) : taskWall.reduce((total, value) => total + value, 0)];
  const rss = numericValues(results, "peakRssMiB");
  const taskCPU = numericValues(results, "cpuMs");
  const cpu = taskCPU.length === 0 ? [] : [taskCPU.reduce((total, value) => total + value, 0)];
  const failureCount = results.filter((result) => result.status !== 0).length;
  const cleanupCount = results.filter((result) => result.cleanupError).length;
  return {
    accepted: failureCount === 0 && cleanupCount === 0 && taskWall.length === results.length && taskWall.length > 0,
    sampleCount: results.length,
    wallTimeMs: { p50: percentile(wall, 0.5), p95: percentile(wall, 0.95), max: wall.length ? Math.max(...wall) : null },
    peakRssMiB: { max: rss.length ? Math.max(...rss) : null },
    cpuMs: { p50: percentile(cpu, 0.5), max: cpu.length ? Math.max(...cpu) : null },
    failureCount,
    leakedResourceCount: cleanupCount,
  };
}

async function runBaseline(manifest, args, common) {
  const lanes = baselineLanes(manifest);
  const samples = [];
  for (let index = 0; index < args.coldSamples; index += 1) {
    for (const lane of lanes) {
      const results = await runTaskSet(lane.tasks, manifest, { ...common, cold: true, parallel: false, seed: args.seeds[index % args.seeds.length] });
      samples.push({ lane: lane.id, kind: "cold", sample: index + 1, seed: args.seeds[index % args.seeds.length], results, aggregate: aggregate(results, { parallel: false }) });
    }
  }
  for (let index = 0; index < args.warmSamples; index += 1) {
    for (const lane of lanes) {
      const results = await runTaskSet(lane.tasks, manifest, { ...common, cold: false, parallel: false, seed: args.seeds[index % args.seeds.length] });
      samples.push({ lane: lane.id, kind: "warm", sample: index + 1, seed: args.seeds[index % args.seeds.length], results, aggregate: aggregate(results, { parallel: false }) });
    }
  }
  return samples;
}

export function groupAggregates(samples) {
  const groups = new Map();
  for (const sample of samples) {
    const key = `${sample.lane}:${sample.kind}`;
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push(sample.aggregate);
  }
  const result = {};
  for (const [key, values] of groups) {
    const accepted = values.every((value) => value.accepted);
    const wall = values.map((value) => value.wallTimeMs.p50).filter((value) => value !== null);
    const rss = values.map((value) => value.peakRssMiB.max).filter((value) => value !== null);
    const cpu = values.map((value) => value.cpuMs.p50).filter((value) => value !== null);
    result[key] = {
      accepted,
      sampleCount: values.length,
      wallTimeMs: metricSummary(wall),
      peakRssMiB: metricSummary(rss),
      cpuMs: metricSummary(cpu),
      failureCount: values.reduce((total, value) => total + value.failureCount, 0),
      leakedResourceCount: values.reduce((total, value) => total + value.leakedResourceCount, 0),
    };
  }
  return result;
}

async function runRequestedTask(manifest, args, common) {
  const tasks = selectTasks(manifest, args.task);
  const results = [];
  for (let index = 0; index < args.warmSamples; index += 1) {
    const sampleResults = await runTaskSet(tasks, manifest, {
      ...common,
      cold: false,
      parallel: true,
      seed: args.seeds[index % args.seeds.length],
      candidateSlots: args.candidateSlots,
    });
    results.push({ sample: index + 1, seed: args.seeds[index % args.seeds.length], results: sampleResults, aggregate: aggregate(sampleResults, { parallel: true }) });
  }
  return results;
}

async function verifyBaseline(filePath) {
  const baseline = await loadJSON(path.resolve(REPOSITORY_ROOT, filePath));
  const required = ["schemaVersion", "documentId", "kerId", "hostFingerprint", "executionStatus", "evaluations", "acceptedEvaluationFields"];
  for (const field of required) if (!(field in baseline)) throw new Error(`baseline is missing ${field}`);
  if (baseline.schemaVersion !== 1) throw new Error(`baseline schemaVersion is ${baseline.schemaVersion}`);
  if (baseline.documentId !== "PLAN-HARDEN-LLM-TEST-FEEDBACK-002") throw new Error("baseline documentId is incorrect");
  if (baseline.kerId !== "KER-HLLM-TEST-FEEDBACK-001") throw new Error("baseline kerId is incorrect");
  if (baseline.executionStatus !== "not_run" && baseline.executionStatus !== "measured") throw new Error(`unsupported executionStatus ${baseline.executionStatus}`);
  const current = collectHostFingerprint();
  for (const field of ["os", "physicalCpuCount", "logicalCpuCount", "memoryMiB", "goVersion", "nodeVersion", "dockerVersion", "composeVersion"]) {
    if (!baseline.hostFingerprint[field]) throw new Error(`baseline hostFingerprint is missing ${field}`);
    if (baseline.hostFingerprint[field] !== current[field]) throw new Error(`host fingerprint mismatch for ${field}`);
  }
  return { accepted: true, executionStatus: baseline.executionStatus, hostFingerprint: baseline.hostFingerprint };
}

function usage() {
  return "Usage: node scripts/benchmark-test-feedback.mjs [--mode baseline|task] [--task ID|GROUP] [--warm-samples N] [--cold-samples N] [--seeds CSV] [--output PATH] [--compare PATH] [--verify-baseline PATH]";
}

export async function main(argv = process.argv.slice(2)) {
  const args = parseArgs(argv);
  if (args.help) {
    console.log(usage());
    return 0;
  }
  if (args.verifyBaseline) {
    const result = await verifyBaseline(args.verifyBaseline);
    console.log(JSON.stringify(result));
    return 0;
  }
  const manifestPath = path.join(REPOSITORY_ROOT, "test", "test-tiers.json");
  const manifest = await loadJSON(manifestPath);
  const common = { root: REPOSITORY_ROOT };
  const hostFingerprint = collectHostFingerprint();
  const manifestSHA256 = await sha256File(manifestPath);
  let samples;
  let evaluations;
  if (args.mode === "baseline") {
    samples = await runBaseline(manifest, args, common);
    evaluations = groupAggregates(samples);
  } else {
    if (!args.task) throw new Error("--task is required unless --mode baseline is used");
    samples = await runRequestedTask(manifest, args, common);
    evaluations = { [args.task]: {
      ...aggregate(samples.flatMap((sample) => sample.results)),
      sampleCount: samples.length,
      samples: samples.map((sample) => sample.aggregate),
    } };
  }
  const result = {
    schemaVersion: 1,
    documentId: "PLAN-HARDEN-LLM-TEST-FEEDBACK-002",
    generatedAt: new Date().toISOString(),
    hostFingerprint,
    manifestSHA256,
    mode: args.mode,
    selector: args.task ?? "baseline",
    seeds: args.seeds,
    samples,
    evaluations,
    failureCount: samples.reduce((total, sample) => total + (sample.aggregate?.failureCount ?? 0), 0),
    leakedResourceCount: samples.reduce((total, sample) => total + (sample.aggregate?.leakedResourceCount ?? 0), 0),
  };
  if (args.compare) result.comparison = await loadJSON(path.resolve(REPOSITORY_ROOT, args.compare));
  if (args.output) {
    const outputPath = path.resolve(REPOSITORY_ROOT, args.output);
    await fs.mkdir(path.dirname(outputPath), { recursive: true });
    await fs.writeFile(outputPath, `${JSON.stringify(result, null, 2)}\n`, { mode: 0o600 });
  }
  console.log(JSON.stringify({
    accepted: result.failureCount === 0 && result.leakedResourceCount === 0,
    mode: result.mode,
    selector: result.selector,
    sampleCount: result.samples.length,
    failureCount: result.failureCount,
    leakedResourceCount: result.leakedResourceCount,
    output: args.output ?? null,
  }));
  return result.failureCount === 0 && result.leakedResourceCount === 0 ? 0 : 1;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().then((code) => process.exitCode = code).catch((error) => {
    console.error(scrub(error.stack ?? error.message));
    process.exitCode = 1;
  });
}
