// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-049

import assert from "node:assert/strict";
import { afterEach, describe, test } from "node:test";
import { promises as fs } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { resolvedCommand, runTasks } from "../run-test-tier.mjs";

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
if (mode === "fail" || mode === "output-fail") setTimeout(() => finish(7), duration);
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
  return runTasks(tasks, {
    root: fixtureData.root,
    resourceClasses: resources(),
    environment: { HARDEN_LLM_FAKE_EVENTS: fixtureData.eventsPath },
    runDirectory: path.join(fixtureData.root, "run"),
    ...options,
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
    const tasks = [
      task(data, "cpu-a", "cpu", "ok", 750),
      task(data, "cpu-b", "cpu", "ok", 750),
      task(data, "cpu-c", "cpu", "ok", 750),
      task(data, "service-a", "service", "ok", 750),
    ];
    const result = await runFixture(data, tasks);
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
