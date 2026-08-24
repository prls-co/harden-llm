#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { promises as fs } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

import {
  loadManifest,
  runTasks as runTierTasks,
  selectTasks as selectTierTasks,
} from "./run-test-tier.mjs";

const REPOSITORY_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const DEFAULT_SEED = 104729;

// The canonical runner owns performance.now, GNU time -v, /proc sampling, and sha256-safe evidence.

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
    if (!argument.startsWith("--")) throw new Error(`unexpected argument ${argument}`);
    const name = argument.slice(2);
    if (name === "help") {
      result.help = true;
      continue;
    }
    const value = argv[index + 1];
    if (value === undefined || value.startsWith("--")) throw new Error(`missing value for --${name}`);
    index += 1;
    if (name === "mode") result.mode = value;
    else if (name === "task") result.task = value;
    else if (name === "warm-samples") {
      result.warmSamples = nonNegativeInteger(value, name);
      result.warmSamplesExplicit = true;
    }
    else if (name === "cold-samples") {
      result.coldSamples = nonNegativeInteger(value, name);
      result.coldSamplesExplicit = true;
    }
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
  return selectTierTasks(manifest, selector);
}

function benchmarkLanes(manifest) {
  const laneOrder = manifest.benchmarkLaneOrder ?? ["fast-candidates", "integration", "browser", "full-system"];
  const byID = new Map(manifest.tasks.map((task) => [task.id, task]));
  return laneOrder.map((laneID) => {
    const selected = new Set();
    const add = (task) => {
      if (selected.has(task.id)) return;
      selected.add(task.id);
      for (const dependencyID of task.dependsOn ?? []) {
        const dependency = byID.get(dependencyID);
        if (!dependency) throw new Error(`benchmark lane ${laneID} has unknown dependency ${dependencyID}`);
        add(dependency);
      }
    };
    for (const task of manifest.tasks) if ((task.benchmarkLanes ?? []).includes(laneID)) add(task);
    if (selected.size === 0) throw new Error(`benchmark lane ${laneID} has no tasks`);
    return { id: laneID, tasks: manifest.tasks.filter((task) => selected.has(task.id)) };
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
  return {
    os: `${process.platform}-${kernel}-${architecture}`,
    physicalCpuCount,
    logicalCpuCount: os.cpus().length,
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

export function aggregate(results, { parallel = false, cleanupErrors = [] } = {}) {
  const taskWall = numericValues(results, "wallTimeMs");
  const wall = taskWall.length === 0 ? [] : [parallel ? Math.max(...taskWall) : taskWall.reduce((total, value) => total + value, 0)];
  const rss = numericValues(results, "peakRssMiB");
  const cpu = numericValues(results, "cpuMs");
  const failureCount = results.filter((result) => result.status !== 0).length;
  const cleanupCount = results.filter((result) => result.cleanupError).length + cleanupErrors.length;
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

async function runSample(tasks, manifest, { root, seed, cold, parallel, candidateSlots, runID }) {
  const result = await runTierTasks(tasks, {
    root,
    resourceClasses: manifest.resourceClasses,
    seed,
    cold,
    candidateSlots: parallel ? candidateSlots : 1,
    runID,
  });
  return {
    results: result.results,
    runner: {
      accepted: result.accepted,
      firstFailure: result.firstFailure,
      cleanupErrors: result.cleanupErrors,
    },
    aggregate: aggregate(result.results, { parallel, cleanupErrors: result.cleanupErrors }),
  };
}

async function runBaseline(manifest, args, common) {
  const samples = [];
  for (const kind of ["cold", "warm"]) {
    const count = kind === "cold" ? args.coldSamples : args.warmSamples;
    for (let index = 0; index < count; index += 1) {
      for (const lane of benchmarkLanes(manifest)) {
        const seed = args.seeds[index % args.seeds.length];
        const sample = await runSample(lane.tasks, manifest, {
          ...common,
          seed,
          cold: kind === "cold",
          parallel: false,
          runID: `baseline-${kind}-${index + 1}-${lane.id}`,
        });
        samples.push({ lane: lane.id, kind, sample: index + 1, seed, taskIds: lane.tasks.map((task) => task.id), ...sample });
      }
    }
  }
  return samples;
}

async function runRequestedTask(manifest, args, common) {
  const tasks = selectTasks(manifest, args.task);
  const samples = [];
  const seedEachSample = tasks.some((task) => task.seedEachSample);
  const explicitSampleCounts = args.warmSamplesExplicit || args.coldSamplesExplicit;
  const samplePlan = seedEachSample && !explicitSampleCounts
    ? args.seeds.map((seed, index) => ({ kind: "warm", index, seed }))
    : ["cold", "warm"].flatMap((kind) => Array.from({ length: kind === "cold" ? args.coldSamples : args.warmSamples }, (_, index) => ({
      kind,
      index,
      seed: args.seeds[index % args.seeds.length],
    })));
  for (const { kind, index, seed } of samplePlan) {
    const sample = await runSample(tasks, manifest, {
      ...common,
      seed,
      cold: kind === "cold",
      parallel: true,
      candidateSlots: args.candidateSlots,
      runID: `task-${args.task}-${kind}-${index + 1}`,
    });
    samples.push({ lane: args.task, kind, sample: index + 1, seed, taskIds: tasks.map((task) => task.id), ...sample });
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
    const wall = values.map((value) => value.wallTimeMs.p50).filter((value) => value !== null);
    const rss = values.map((value) => value.peakRssMiB.max).filter((value) => value !== null);
    const cpu = values.map((value) => value.cpuMs.p50).filter((value) => value !== null);
    result[key] = {
      accepted: values.every((value) => value.accepted),
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

function compareTaskEvaluation(evaluations, baseline) {
  const current = evaluations["fast:warm"];
  const currentCold = evaluations["fast:cold"];
  const reference = baseline.evaluations?.["fast-candidates:warm"];
  if (!current || !currentCold || !reference) return { accepted: false, reason: "fast comparison requires fast:cold, fast:warm, and fast-candidates:warm evaluations" };
  const wallLimit = reference.wallTimeMs.p95 * 0.8;
  const acceptedBudget = baseline.acceptedBudgets?.["fast-candidates:warm"];
  const rssLimit = acceptedBudget?.peakRssMiBMax ?? reference.peakRssMiB.max;
  const currentPeakRSS = Math.max(currentCold.peakRssMiB.max, current.peakRssMiB.max);
  const checks = {
    warmP95WithinBudget: current.wallTimeMs.p95 <= wallLimit,
    peakRSSWithinBaseline: currentPeakRSS <= rssLimit,
    zeroFailures: current.failureCount === 0,
    zeroLeaks: current.leakedResourceCount === 0,
  };
  return {
    accepted: Object.values(checks).every(Boolean),
    reference: { wallP95Ms: reference.wallTimeMs.p95, wallBudgetMs: wallLimit, peakRssMiB: rssLimit, rawPeakRssMiB: reference.peakRssMiB.max },
    current: { wallP95Ms: current.wallTimeMs.p95, peakRssMiB: currentPeakRSS },
    checks,
  };
}

async function compareSeededEvaluation(evaluations, baseline, manifest) {
  const current = Object.entries(evaluations).find(([key]) => key.endsWith(":warm"))?.[1];
  if (!current) return { accepted: false, reason: "seeded comparison requires a warm evaluation" };

  let sequentialP95 = baseline.evaluations?.["fast-candidates:warm"]?.wallTimeMs?.p95 ?? null;
  const rawEvidencePath = baseline.reference?.evidencePath;
  if (rawEvidencePath) {
    try {
      const rawEvidence = await loadJSON(path.resolve(REPOSITORY_ROOT, rawEvidencePath));
      const frontendTaskWall = rawEvidence.samples
        .filter((sample) => sample.kind === "warm")
        .map((sample) => sample.results.find((result) => result.taskId === "frontend-deterministic")?.wallTimeMs)
        .filter((value) => Number.isFinite(value));
      if (frontendTaskWall.length > 0) sequentialP95 = percentile(frontendTaskWall, 0.95);
    } catch {
      // The committed KER aggregate remains the conservative fallback when raw evidence is unavailable.
    }
  }
  const checks = {
    seededFrontendPassCount: current.sampleCount === 10 && current.failureCount === 0,
    ownershipErrorCount: current.failureCount === 0,
    leakedMessageOrProcessCount: current.leakedResourceCount === 0,
    deterministicSerialExceptionCount: (manifest.frontendSerialExceptions ?? []).length <= 2,
    p95WithinSequentialBaseline: sequentialP95 !== null && current.wallTimeMs.p95 <= sequentialP95,
  };
  return {
    accepted: Object.values(checks).every(Boolean),
    reference: { sequentialP95WallTimeMs: sequentialP95 },
    current: { sampleCount: current.sampleCount, p95WallTimeMs: current.wallTimeMs.p95 },
    checks,
  };
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
  return "Usage: node scripts/benchmark-test-feedback.mjs [--mode baseline|task] [--task ID|GROUP] [--warm-samples N] [--cold-samples N] [--seeds CSV] [--candidate-slots N] [--output PATH] [--compare PATH] [--verify-baseline PATH]";
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
  const manifest = await loadManifest(manifestPath);
  const common = { root: REPOSITORY_ROOT };
  const hostFingerprint = collectHostFingerprint();
  const manifestSHA256 = await sha256File(manifestPath);
  const selectedTasks = args.task ? selectTasks(manifest, args.task) : [];
  const samples = args.mode === "baseline"
    ? await runBaseline(manifest, args, common)
    : await runRequestedTask(manifest, args, common);
  const evaluations = groupAggregates(samples);
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
    failureCount: samples.reduce((total, sample) => total + sample.aggregate.failureCount, 0),
    leakedResourceCount: samples.reduce((total, sample) => total + sample.aggregate.leakedResourceCount, 0),
  };
  if (args.compare) {
    const comparison = await loadJSON(path.resolve(REPOSITORY_ROOT, args.compare));
    result.comparison = args.mode === "task" && args.task === "fast"
      ? compareTaskEvaluation(evaluations, comparison)
      : args.mode === "task" && selectedTasks.some((task) => task.seedEachSample)
        ? await compareSeededEvaluation(evaluations, comparison, manifest)
        : { accepted: true, reason: "comparison recorded without a task budget" };
  }
  result.accepted = result.failureCount === 0 && result.leakedResourceCount === 0 && (result.comparison?.accepted ?? true);
  if (args.output) {
    const outputPath = path.resolve(REPOSITORY_ROOT, args.output);
    await fs.mkdir(path.dirname(outputPath), { recursive: true });
    const temporaryPath = `${outputPath}.writing`;
    await fs.writeFile(temporaryPath, `${JSON.stringify(result, null, 2)}\n`, { mode: 0o600 });
    await fs.rename(temporaryPath, outputPath);
  }
  console.log(JSON.stringify({
    accepted: result.accepted,
    mode: result.mode,
    selector: result.selector,
    sampleCount: result.samples.length,
    failureCount: result.failureCount,
    leakedResourceCount: result.leakedResourceCount,
    comparison: result.comparison ?? null,
    output: args.output ?? null,
  }));
  return result.accepted ? 0 : 1;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().then((code) => { process.exitCode = code; }).catch((error) => {
    console.error(scrub(error.stack ?? error.message));
    process.exitCode = 1;
  });
}
