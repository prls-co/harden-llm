// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-049 TEST-281

import assert from "node:assert/strict";
import { afterEach, describe, test } from "node:test";
import { spawnSync } from "node:child_process";
import { promises as fs } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { main, resourceCleanupOptions, resolvedCommand, runTasks, writeRunReport } from "../run-test-tier.mjs";

const TEST_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const fixtureSource = `
import fs from "node:fs";

const [mode, id, durationText] = process.argv.slice(2);
const duration = Number(durationText ?? 50);
const eventsPath = process.env.HARDEN_LLM_FAKE_EVENTS;
const record = (event) => fs.appendFileSync(eventsPath, JSON.stringify({ event, id, at: Number(process.hrtime.bigint()) }) + "\\n");

record("start");
const finish = (status = 0) => {
  record("end");
  process.exit(status);
};
process.on("SIGTERM", () => finish(143));
process.on("SIGINT", () => finish(130));

if (mode === "output" || mode === "output-fail") process.stdout.write("x".repeat(20_000));
if (mode === "capacity-report-symlink") {
  fs.symlinkSync(process.env.HARDEN_LLM_CAPACITY_REPORT_TARGET, process.env.HARDEN_LLM_CAPACITY_REPORT_PATH);
}
if (mode === "wait-for-release") {
  const releasePath = process.env.HARDEN_LLM_FAKE_RELEASE;
  const wait = () => fs.existsSync(releasePath) ? finish() : setTimeout(wait, 10);
  wait();
} else if (mode === "fail" || mode === "output-fail") setTimeout(() => finish(7), duration);
else setTimeout(() => finish(0), duration);
`;

const openFixtures = new Set();

async function fixture() {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "harden-llm-tier-test-"));
  const fixturePath = path.join(root, "fake-task.mjs");
  const eventsPath = path.join(root, "events.jsonl");
  await fs.writeFile(fixturePath, fixtureSource, { mode: 0o600 });
  await fs.writeFile(eventsPath, "", { mode: 0o600 });
  openFixtures.add(root);
  return { root, fixturePath, eventsPath };
}

async function closeFixtures() {
  for (const root of openFixtures) await fs.rm(root, { recursive: true, force: true });
  openFixtures.clear();
}

afterEach(closeFixtures);

function resources(overrides = {}) {
  return {
    cpu: { slots: 2, exclusive: false },
    service: { slots: 1, exclusive: false },
    release: { slots: 1, exclusive: true },
    ...overrides,
  };
}

function task(fixtureData, id, resourceClass = "cpu", mode = "ok", duration = 80, extra = {}) {
  return {
    id,
    testIds: [`TEST-049-${id}`],
    tier: "T0",
    resourceClass,
    command: [process.execPath, fixtureData.fixturePath, mode, id, String(duration)],
    workingDirectory: ".",
    dependsOn: [],
    timeoutMs: 10_000,
    cleanupOwner: "runner-test",
    network: "forbidden",
    credentialKeys: [],
    requiredFor: ["test"],
    pathSelectors: [],
    ...extra,
  };
}

async function runFixture(fixtureData, tasks, options = {}) {
  const { environment = {}, ...runnerOptions } = options;
  return runTasks(tasks, {
    root: fixtureData.root,
    resourceClasses: resources(),
    environment: { HARDEN_LLM_FAKE_EVENTS: fixtureData.eventsPath, ...environment },
    runDirectory: path.join(fixtureData.root, "run"),
    ...runnerOptions,
  });
}

async function events(fixtureData) {
  const contents = await fs.readFile(fixtureData.eventsPath, "utf8");
  return contents.trim() ? contents.trim().split("\n").map((line) => JSON.parse(line)) : [];
}

async function waitForEvent(fixtureData, event, id, timeoutMs = 2_000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if ((await events(fixtureData)).some((record) => record.event === event && record.id === id)) return;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  throw new Error(`timed out waiting for ${event}:${id}`);
}

test("TEST-049 refuses the real Docker lifecycle boundary without a managed lease", async () => {
  const data = await fixture();
  const environment = Object.fromEntries(Object.entries(process.env).filter(([key]) => (
    !key.startsWith("HARDEN_LLM_TEST_DAEMON_LOCK_")
    && key !== "HARDEN_LLM_TEST_RUN_ID"
    && key !== "HARDEN_LLM_TEST_RESOURCE_DIR"
  )));
  environment.PATH = data.root;
  const result = spawnSync(process.execPath, [path.join(TEST_ROOT, "scripts", "test", "test_resource_lifecycle_docker_test.mjs")], {
    cwd: TEST_ROOT,
    env: environment,
    encoding: "utf8",
    timeout: 5_000,
  });
  assert.equal(result.status, 1, result.stderr);
  assert.match(result.stderr, /requires scripts\/run-test-tier\.mjs to supply a managed run ID and inherited Docker lease/);
  assert.doesNotMatch(result.stderr, /ENOENT.*docker|spawn.*docker/i);
});

test("TEST-281 cleanup budgets are per task until an invocation-wide cancellation tail begins", () => {
  const invocationState = { cleanupDeadline: null };
  const firstTaskState = { cleanupDeadline: null };
  const firstCleanup = resourceCleanupOptions({
    cleanupTimeoutMs: 1_000,
    lifecycleState: invocationState,
    taskCleanupState: firstTaskState,
  }, 100);
  assert.equal(firstCleanup.cleanupDeadline, 1_100);
  assert.equal(invocationState.cleanupDeadline, null, "normal cleanup must not start the whole-run cancellation tail");

  const sameTaskCleanup = resourceCleanupOptions({
    cleanupTimeoutMs: 1_000,
    lifecycleState: invocationState,
    taskCleanupState: firstTaskState,
  }, 500);
  assert.equal(sameTaskCleanup.cleanupDeadline, 1_100, "setup-failure and final cleanup for one task share its deadline");

  const laterTask = resourceCleanupOptions({
    cleanupTimeoutMs: 1_000,
    lifecycleState: invocationState,
    taskCleanupState: { cleanupDeadline: null },
  }, 5_000);
  assert.equal(laterTask.cleanupDeadline, 6_000, "a long earlier test cannot consume the next task's cleanup allowance");

  invocationState.cleanupDeadline = 7_000;
  const abortedSibling = resourceCleanupOptions({
    cleanupTimeoutMs: 1_000,
    lifecycleState: invocationState,
    taskCleanupState: { cleanupDeadline: null },
  }, 6_500);
  assert.equal(abortedSibling.cleanupDeadline, 7_000, "after cancellation, all remaining task cleanup respects one invocation-wide cap");
});

function interval(records, id) {
  const matching = records.filter((record) => record.id === id);
  return { start: matching.find((record) => record.event === "start")?.at, end: matching.find((record) => record.event === "end")?.at };
}

describe("resource-aware tier runner", () => {
  test("isolates parallel Mix build output in runner-owned task directories", async () => {
    const data = await fixture();
    const firstDirectory = path.join(data.root, "run", "tasks", "frontend-compile");
    const secondDirectory = path.join(data.root, "run", "tasks", "frontend-deterministic");
    const mixTask = task(data, "frontend-compile", "cpu", "ok", 20, {
      command: ["mix", "compile"],
      workingDirectory: "frontend",
    });
    const options = {
      root: data.root,
      runID: "runner-test",
      seed: 104729,
      environment: { MIX_BUILD_PATH: path.join(data.root, "shared-build") },
    };

    const first = resolvedCommand(mixTask, { ...options, taskDirectory: firstDirectory });
    const second = resolvedCommand(
      { ...mixTask, id: "frontend-deterministic", command: ["mix", "test"] },
      { ...options, taskDirectory: secondDirectory },
    );

    assert.equal(first.environment.MIX_BUILD_PATH, path.join(firstDirectory, "mix-build"));
    assert.equal(second.environment.MIX_BUILD_PATH, path.join(secondDirectory, "mix-build"));
    assert.notEqual(first.environment.MIX_BUILD_PATH, second.environment.MIX_BUILD_PATH);
  });

  test("honors dependency ordering and exposes a stable result record", async () => {
    const data = await fixture();
    const first = task(data, "first", "cpu", "ok", 30);
    const second = task(data, "second", "cpu", "ok", 20, { dependsOn: ["first"] });
    const result = await runFixture(data, [first, second]);
    const records = await events(data);

    assert.equal(result.accepted, true, JSON.stringify(result.results));
    assert.deepEqual(result.results.map((item) => item.taskId), ["first", "second"]);
    assert.ok(interval(records, "first").end < interval(records, "second").start);
    assert.equal(result.results.every((item) => item.status === 0), true);
    assert.equal(result.cleanupErrors.length, 0);
  });

  test("enforces resource slots while allowing independent resources to overlap", async () => {
    const data = await fixture();
    const releasePath = path.join(data.root, "release-tasks");
    const tasks = [
      task(data, "cpu-a", "cpu", "wait-for-release", 750, { timeoutMs: 30_000 }),
      task(data, "cpu-b", "cpu", "wait-for-release", 750, { timeoutMs: 30_000 }),
      task(data, "cpu-c", "cpu", "wait-for-release", 750, { timeoutMs: 30_000 }),
      task(data, "service-a", "service", "wait-for-release", 750, { timeoutMs: 30_000 }),
    ];
    const running = runFixture(data, tasks, { environment: { HARDEN_LLM_FAKE_RELEASE: releasePath } });
    let result;
    try {
      await Promise.all([
        waitForEvent(data, "start", "cpu-a", 10_000),
        waitForEvent(data, "start", "cpu-b", 10_000),
        waitForEvent(data, "start", "service-a", 10_000),
      ]);
      const beforeRelease = await events(data);
      assert.equal(beforeRelease.some((record) => record.event === "end"), false, "barrier keeps launched tasks active until overlap is observed");
      const cpuA = interval(beforeRelease, "cpu-a");
      const cpuB = interval(beforeRelease, "cpu-b");
      const service = interval(beforeRelease, "service-a");
      assert.equal(cpuA.end, undefined);
      assert.equal(cpuB.end, undefined);
      assert.equal(service.end, undefined);
      assert.ok(Number.isFinite(cpuA.start) && Number.isFinite(cpuB.start) && Number.isFinite(service.start), "both resource classes reached the barrier");
      await fs.writeFile(releasePath, "release", { mode: 0o600 });
      result = await running;
    } finally {
      await fs.writeFile(releasePath, "release", { mode: 0o600 }).catch(() => {});
      result ??= await running;
    }
    const records = await events(data);
    const cpuIntervals = tasks.filter((item) => item.resourceClass === "cpu").map((item) => interval(records, item.id));
    const maxCpuOverlap = cpuIntervals.reduce((maximum, current, index) => Math.max(maximum, cpuIntervals.filter((other, otherIndex) => otherIndex !== index && other.start < current.end && other.end > current.start).length + 1), 0);
    assert.equal(result.accepted, true, JSON.stringify(result.results));
    assert.ok(maxCpuOverlap <= 2, `cpu overlap was ${maxCpuOverlap}`);
    assert.ok(interval(records, "cpu-a").start < interval(records, "service-a").end);
    assert.ok(interval(records, "service-a").start < interval(records, "cpu-a").end);
  });

  test("keeps exclusive resources isolated from all other work", async () => {
    const data = await fixture();
    const tasks = [
      task(data, "normal", "cpu", "ok", 750),
      task(data, "release", "release", "ok", 750),
      task(data, "release-two", "release", "ok", 300),
    ];
    const result = await runFixture(data, tasks);
    const records = await events(data);
    const normal = interval(records, "normal");
    const release = interval(records, "release");
    const releaseTwo = interval(records, "release-two");
    assert.equal(result.accepted, true);
    assert.ok(release.end <= normal.start || normal.end <= release.start);
    assert.ok(releaseTwo.end <= normal.start || normal.end <= releaseTwo.start);
    assert.ok(release.end <= releaseTwo.start || releaseTwo.end <= release.start);
  });

  test("cancels eligible siblings after the first causal failure", async () => {
    const data = await fixture();
    const failing = task(data, "failing", "cpu", "fail", 40);
    const sibling = task(data, "sibling", "cpu", "ok", 1_500);
    const dependent = task(data, "dependent", "cpu", "ok", 20, { dependsOn: ["failing"] });
    const result = await runFixture(data, [failing, sibling, dependent]);

    const byId = new Map(result.results.map((item) => [item.taskId, item]));
    assert.equal(byId.get("failing").status, 7);
    assert.notEqual(byId.get("sibling").status, 0);
    assert.equal(byId.get("dependent").status, 125);
    assert.match(byId.get("dependent").failureSummary, /cancelled|dependency/i);
    assert.equal(result.cleanupErrors.length, 0);
  });

  test("propagates an external abort through the child process group", async () => {
    const data = await fixture();
    const controller = new AbortController();
    const running = runFixture(data, [task(data, "interruptible", "cpu", "ok", 1_500)], { signal: controller.signal });
    await waitForEvent(data, "start", "interruptible");
    controller.abort();
    const result = await running;
    assert.notEqual(result.results[0].status, 0);
    assert.equal(result.cleanupErrors.length, 0);
    await new Promise((resolve) => setTimeout(resolve, 80));
    assert.equal((await events(data)).filter((record) => record.event === "start").length, 1);
  });

  test("terminates a task that exceeds its bounded timeout", async () => {
    const data = await fixture();
    const result = await runFixture(data, [task(data, "timed-out", "cpu", "ok", 1_500, { timeoutMs: 100 })]);
    const timedOut = result.results[0];
    assert.notEqual(timedOut.status, 0);
    assert.equal(timedOut.timedOut, true);
    assert.equal(result.cleanupErrors.length, 0);
  });

  test("bounds captured output and preserves the child exit status", async () => {
    const data = await fixture();
    const result = await runFixture(data, [task(data, "noisy", "cpu", "output-fail", 20)]);
    const noisy = result.results[0];
    assert.equal(noisy.status, 7);
    assert.equal(noisy.stdoutBytes, 20_000);
    assert.ok(noisy.truncatedOutputBytes > 0);
    assert.ok(noisy.failureSummary.length <= 240);
    assert.ok(noisy.failureDetail.length <= 4096);
    assert.equal(result.firstFailure.failureDetail, noisy.failureDetail);
    assert.equal(result.cleanupErrors.length, 0);
  });
});

test("writes a unique private run report by default with task and lifecycle timings", async () => {
  const data = await fixture();
  const manifestPath = path.join(data.root, "report-manifest.json");
  const manifest = {
    schemaVersion: 1,
    documentId: "TEST-049",
    resourceClasses: { cpu: { slots: 1, exclusive: false } },
    tasks: [{
      id: "report-smoke",
      testIds: ["TEST-049-report-smoke"],
      tier: "T0",
      resourceClass: "cpu",
      command: [process.execPath, "-e", "process.exit(0)"],
      dependsOn: [],
      timeoutMs: 5_000,
      cleanupOwner: "runner",
      network: "forbidden",
      credentialKeys: [],
      requiredFor: ["report-smoke"],
      pathSelectors: [],
    }],
  };
  await fs.writeFile(manifestPath, JSON.stringify(manifest), { mode: 0o600 });
  const originalLog = console.log;
  const logLines = [];
  console.log = (...values) => logLines.push(values.join(" "));
  try {
    assert.equal(await main(["--manifest", manifestPath, "--root", data.root, "--task", "report-smoke"]), 0);
  } finally {
    console.log = originalLog;
  }
  const summary = JSON.parse(logLines.at(-1));
  assert.equal(typeof summary.output, "string", "the CLI must expose the durable report path when --output is omitted");
  assert.deepEqual(summary.cleanupWarnings, []);
  const reportPath = summary.output;
  const metadata = await fs.stat(reportPath);
  assert.equal(metadata.mode & 0o777, 0o600, "diagnostic report files are private");
  const report = JSON.parse(await fs.readFile(reportPath, "utf8"));
  assert.equal(report.accepted, true);
  assert.equal(report.results[0].taskId, "report-smoke");
  assert.equal(Number.isFinite(report.results[0].wallTimeMs), true);
  assert.deepEqual(report.cleanupWarnings, []);
  assert.deepEqual(report.results[0].cleanupWarnings, []);
  assert.deepEqual(report.lifecycleTimings, { dockerIdentityMs: null, daemonLockWaitMs: null, staleReceiptRecoveryMs: null });
});

test("refuses an oversized diagnostic report without leaving a partial artifact", async () => {
  const data = await fixture();
  const reportPath = path.join(data.root, "oversized.json");
  await assert.rejects(writeRunReport({ diagnostics: "x".repeat(2 * 1024 * 1024) }, reportPath), /exceeds the 2097152-byte limit/);
  await assert.rejects(fs.stat(reportPath), { code: "ENOENT" });
});

test("capacity child report is validated and included in the private runner result", async () => {
  const data = await fixture();
  const source = `
import fs from "node:fs";
const report = { schemaVersion: 1, reportKind: "harden-llm-capacity.v1", testRunId: process.env.HARDEN_LLM_TEST_RUN_ID, caseSet: "correctness", testIds: ["TEST-277"], cases: [{ scenarioId: "synthetic" }] };
fs.writeFileSync(process.env.HARDEN_LLM_CAPACITY_REPORT_PATH, JSON.stringify(report), { mode: 0o600 });
`;
  const task = {
    id: "capacity-report-fixture", testIds: ["TEST-277"], tier: "T3", resourceClass: "cpu",
    command: [process.execPath, "--input-type=module", "-e", source], dependsOn: [], timeoutMs: 5000,
    cleanupOwner: "runner", network: "local-only", credentialKeys: [], requiredFor: [], pathSelectors: [], capacityReport: true,
  };
  const result = await runTasks([task], {
    root: data.root, runDirectory: path.join(data.root, "run-capacity-report"), runID: "capacity-report-fixture",
    resourceClasses: { cpu: { slots: 1, exclusive: false } },
  });
  assert.equal(result.accepted, true);
  assert.equal(result.results[0].capacityReport.reportKind, "harden-llm-capacity.v1");
  assert.equal(result.results[0].capacityReport.testRunId, "capacity-report-fixture");
});

test("a successful capacity task without its required report is rejected", async () => {
  const data = await fixture();
  const task = {
    id: "capacity-report-missing", testIds: ["TEST-277"], tier: "T3", resourceClass: "cpu",
    command: [process.execPath, "-e", "process.exit(0)"], dependsOn: [], timeoutMs: 5000,
    cleanupOwner: "runner", network: "local-only", credentialKeys: [], requiredFor: [], pathSelectors: [], capacityReport: true,
  };
  const result = await runTasks([task], {
    root: data.root, runDirectory: path.join(data.root, "run-capacity-report-missing"), runID: "capacity-report-missing",
    resourceClasses: { cpu: { slots: 1, exclusive: false } },
  });
  assert.equal(result.accepted, false);
  assert.match(result.results[0].failureSummary, /capacity report invalid or missing/);
});

test("capacity reports cannot be supplied through a child-created symlink", async () => {
  const data = await fixture();
  const target = path.join(data.root, "outside-capacity-report.json");
  await fs.writeFile(target, JSON.stringify({
    schemaVersion: 1,
    reportKind: "harden-llm-capacity.v1",
    testRunId: "capacity-symlink-run",
    caseSet: "correctness",
    testIds: ["TEST-277"],
    cases: [{ scenarioId: "forged" }],
  }), { mode: 0o600 });
  const capacityTask = task(data, "capacity-symlink", "service", "capacity-report-symlink", 20, { capacityReport: true });
  const result = await runFixture(data, [capacityTask], {
    runID: "capacity-symlink-run",
    environment: {
      HARDEN_LLM_FAKE_EVENTS: data.eventsPath,
      HARDEN_LLM_CAPACITY_REPORT_TARGET: target,
    },
  });

  assert.equal(result.accepted, false);
  assert.match(result.results[0].failureSummary, /capacity report invalid or missing/i);
});
