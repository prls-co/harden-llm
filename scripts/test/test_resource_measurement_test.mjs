// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-275

import assert from "node:assert/strict";
import { test } from "node:test";

import { capacitySafetyFailure, collectDockerResourceSample, hashImageIDs, parseResourceBytes, summarizeResourceSamples } from "../measure-test-resources.mjs";

test("TEST-275 parses byte and decimal/binary CLI units exactly", () => {
  const cases = [
    [123, 123],
    ["1 kB", 1_000],
    ["1.5 MB", 1_500_000],
    ["2 KiB", 2_048],
    ["1.25 MiB", 1_310_720],
    ["3 GiB", 3_221_225_472],
  ];
  for (const [input, expected] of cases) {
    assert.equal(parseResourceBytes(input).value, expected, `bytes for ${input}`);
  }
});

test("TEST-275 missing or unknown units remain unknown rather than zero", () => {
  for (const input of [null, undefined, "", "one MiB", "2 widgets", -1, Number.NaN]) {
    const parsed = parseResourceBytes(input);
    assert.equal(parsed.value, null, `unknown input ${String(input)} must not become zero`);
    assert.ok(parsed.nullReason, `unknown input ${String(input)} needs a reason`);
  }
});

test("TEST-275 attributes exact projects and keeps Docker memory, RSS, and sampled peaks distinct", () => {
  const result = summarizeResourceSamples("owned-project", [
    {
      timestamp: "2026-09-21T00:00:00Z",
      containers: [
        {
          id: "owned-a",
          imageId: `sha256:${"a".repeat(64)}`,
          labels: { "com.docker.compose.project": "owned-project" },
          memoryUsageBytes: 100,
          processRssBytes: 80,
          cpuPercent: 50,
        },
        {
          id: "foreign-b",
          imageId: `sha256:${"f".repeat(64)}`,
          labels: { "com.docker.compose.project": "foreign-project" },
          memoryUsageBytes: 900,
          processRssBytes: 700,
          cpuPercent: 99,
        },
      ],
      volumes: [{ labels: { "com.docker.compose.project": "owned-project" }, size: "1 KiB" }],
      host: { availableMemoryBytes: 2_048 },
    },
    {
      timestamp: "2026-09-21T00:00:01Z",
      containers: [{
        id: "owned-a",
        imageId: `sha256:${"a".repeat(64)}`,
        labels: { "com.docker.compose.project": "owned-project" },
        memoryUsageBytes: 200,
        processRssBytes: 170,
        cpuPercent: 75,
      }],
      volumes: [{ labels: { "com.docker.compose.project": "owned-project" }, size: "2 KiB" }],
      host: { availableMemoryBytes: 1_024 },
    },
  ]);

  assert.equal(result.project, "owned-project");
  assert.equal(result.samples[0].containerCount, 1, "foreign project containers must not be attributed");
  assert.equal(result.samples[0].metrics.dockerMemoryUsage.value, 100);
  assert.equal(result.samples[0].metrics.processRSS.value, 80);
  assert.equal(result.samples[0].metrics.cpuPercent.value, 50);
  assert.equal(result.samples[0].metrics.diskBytes.value, 1_024);
  assert.equal(result.samples[0].metrics.hostAvailableMemory.value, 2_048);
  assert.equal(result.peaks.dockerMemoryUsage.value, 200);
  assert.equal(result.peaks.processRSS.value, 170);
  assert.equal(result.peaks.cpuPercent.value, 75);
  assert.equal(result.imageSet.sha256, hashImageIDs([`sha256:${"a".repeat(64)}`]));
  assert.equal(result.imageSet.imageCount, 1);
});

test("TEST-275 malformed sample units are reported with provenance", () => {
  const result = summarizeResourceSamples("owned-project", [{
    timestamp: "2026-09-21T00:00:00Z",
    containers: [{
      id: "owned-a",
      imageId: `sha256:${"a".repeat(64)}`,
      labels: { "com.docker.compose.project": "owned-project" },
      memoryUsageBytes: 100,
      processRssBytes: null,
      cpuPercent: null,
    }],
    volumes: [{ labels: { "com.docker.compose.project": "owned-project" }, size: "100 widgets" }],
    host: { availableMemoryBytes: null },
  }]);

  assert.equal(result.samples[0].metrics.processRSS.value, null);
  assert.ok(result.samples[0].metrics.processRSS.nullReason);
  assert.equal(result.samples[0].metrics.diskBytes.value, null);
  assert.ok(result.samples[0].metrics.diskBytes.nullReason);
  assert.equal(result.samples[0].metrics.hostAvailableMemory.value, null);
  assert.ok(result.samples[0].metrics.hostAvailableMemory.nullReason);
});

test("TEST-275 preserves cadence gaps, explicit host pressure, and unavailable inventories", () => {
  const result = summarizeResourceSamples("owned-project", [
    {
      timestamp: "2026-09-21T00:00:00Z",
      containers: [],
      volumes: [],
      host: { availableMemoryBytes: 4096, pressure: { cpuSomeAvg10: "2.50%" } },
    },
    {
      timestamp: "2026-09-21T00:00:02Z",
      host: { availableMemoryBytes: null },
    },
  ], { expectedIntervalMs: 1000 });

  assert.equal(result.sampling.maxObservedIntervalMs, 2000);
  assert.deepEqual(result.sampling.overExpectedIntervalIndexes, [1]);
  assert.equal(result.samples[0].metrics.dockerMemoryUsage.value, 0);
  assert.equal(result.samples[0].hostPressure.cpuSomeAvg10.value, 2.5);
  assert.equal(result.samples[0].hostPressure.cpuSomeAvg10.unit, "percent");
  assert.equal(result.samples[1].containerCount, null);
  assert.equal(result.samples[1].metrics.dockerMemoryUsage.value, null);
  assert.ok(result.samples[1].inventoryNullReasons.containers);
  assert.equal(result.samples[1].metrics.hostAvailableMemory.value, null);
});

test("TEST-275 samples exact project resources without inventing RSS or volume disk usage", async () => {
  const ownedID = "aaaaaaaaaaaa";
  const foreignID = "bbbbbbbbbbbb";
  const calls = [];
  const sample = await collectDockerResourceSample("owned-project", async (args) => {
    calls.push(args);
    if (args[0] === "ps") return { status: 0, stdout: `${ownedID}\n${foreignID}\n` };
    if (args[0] === "inspect") {
      return {
        status: 0,
        stdout: `${JSON.stringify({ "com.docker.compose.project": "owned-project", documentation: "https://docs.example.test" })}|sha256:${"a".repeat(64)}\n${JSON.stringify({ "com.docker.compose.project": "foreign-project" })}|sha256:${"f".repeat(64)}\n`,
      };
    }
    if (args[0] === "stats") return { status: 0, stdout: `${ownedID}\t1.25MiB / 1GiB\t25.0%\n` };
    if (args[0] === "volume" && args[1] === "ls") return { status: 0, stdout: "owned-volume\n" };
    if (args[0] === "volume" && args[1] === "inspect") {
      return { status: 0, stdout: `${JSON.stringify({ "com.docker.compose.project": "owned-project" })}\n` };
    }
    throw new Error(`unexpected Docker command ${args[0]}`);
  }, {
    timestamp: "2026-09-21T00:00:00Z",
    host: { availableMemoryBytes: 8192, pressure: { cpuSomeAvg10: 1.5 } },
  });

  assert.equal(sample.containers.length, 1);
  assert.equal(sample.containers[0].imageId, `sha256:${"a".repeat(64)}`);
  assert.equal(sample.containers[0].memoryUsageBytes, 1_310_720);
  assert.equal(sample.containers[0].processRssBytes, null);
  assert.equal(sample.containers[0].cpuPercent, 25);
  assert.equal(sample.volumes.length, 1);
  assert.equal(sample.volumes[0].size, null, "Docker volume inventory does not expose used bytes");
  assert.ok(calls.some((args) => args[0] === "stats" && args.at(-1) === ownedID));
  assert.ok(!calls.some((args) => args[0] === "stats" && args.includes(foreignID)));

  const report = summarizeResourceSamples("owned-project", [sample]);
  assert.equal(report.samples[0].metrics.dockerMemoryUsage.value, 1_310_720);
  assert.equal(report.samples[0].metrics.processRSS.value, null);
  assert.equal(report.samples[0].metrics.diskBytes.value, null);
  assert.ok(report.samples[0].metrics.diskBytes.nullReason);
  assert.equal(report.imageSet.sha256, hashImageIDs([`sha256:${"a".repeat(64)}`]));
  assert.ok(!JSON.stringify(report).includes("docs.example.test"));
});

test("TEST-275 preserves precise unavailable-metric reasons without retaining Docker stats output", async () => {
  const containerID = "aaaaaaaaaaaa";
  const sample = await collectDockerResourceSample("owned-project", async (args) => {
    if (args[0] === "ps") return { status: 0, stdout: `${containerID}\n` };
    if (args[0] === "inspect") {
      return { status: 0, stdout: `${JSON.stringify({ "com.docker.compose.project": "owned-project" })}|sha256:${"a".repeat(64)}\n` };
    }
    if (args[0] === "stats") return { status: 0, stdout: `${containerID}\tstats-output-must-not-be-retained\tNaN\n` };
    if (args[0] === "volume" && args[1] === "ls") return { status: 0, stdout: "owned-volume\n" };
    if (args[0] === "volume" && args[1] === "inspect") {
      return { status: 0, stdout: `${JSON.stringify({ "com.docker.compose.project": "owned-project" })}\n` };
    }
    throw new Error(`unexpected Docker command ${args[0]}`);
  });

  const report = summarizeResourceSamples("owned-project", [sample]);
  assert.equal(
    report.samples[0].metrics.dockerMemoryUsage.nullReason,
    "byte count does not include a supported numeric value and unit",
  );
  assert.equal(report.samples[0].metrics.processRSS.nullReason, "process RSS is not exposed by Docker stats");
  assert.equal(report.samples[0].metrics.cpuPercent.nullReason, "CPU percentage is missing or invalid");
  assert.equal(report.samples[0].metrics.diskBytes.nullReason, "Docker volume used bytes are not collected");
  assert.doesNotMatch(JSON.stringify(sample), /stats-output-must-not-be-retained/);
  assert.doesNotMatch(JSON.stringify(report), /stats-output-must-not-be-retained/);
});

test("TEST-275 reports which bounded Docker container sampling stage was unavailable", async () => {
  const containerID = "aaaaaaaaaaaa";
  const identity = `${JSON.stringify({ "com.docker.compose.project": "owned-project" })}|sha256:${"a".repeat(64)}\n`;
  const cases = [
    { failedCommand: "ps", expected: "container inventory command failed" },
    { failedCommand: "inspect", expected: "container identity inspection failed" },
    { failedCommand: "stats", expected: "container stats command failed" },
  ];

  for (const { failedCommand, expected } of cases) {
    const sample = await collectDockerResourceSample("owned-project", async (args) => {
      if (args[0] === failedCommand) {
        return {
          status: 1,
          stdout: "untrusted-secret-output",
          redactedStderr: "Error response from daemon: authorization=Bearer [redacted]",
          timedOut: args[0] === "stats",
        };
      }
      if (args[0] === "ps") return { status: 0, stdout: `${containerID}\n` };
      if (args[0] === "inspect") return { status: 0, stdout: identity };
      if (args[0] === "stats") return { status: 0, stdout: `${containerID}\t1 MiB / 1 GiB\t1.0%\n` };
      if (args[0] === "volume" && args[1] === "ls") return { status: 0, stdout: "" };
      throw new Error(`unexpected Docker command ${args[0]}`);
    });

    assert.match(sample.collectionNullReasons.containers, new RegExp(`^${expected}: .*exit=1`));
    assert.match(sample.collectionNullReasons.containers, /Bearer \[redacted\]/);
    assert.equal(sample.collectionNullReasons.containers.includes("timedOut=true"), failedCommand === "stats");
    assert.doesNotMatch(JSON.stringify(sample), /untrusted-secret-output/);
  }
});

test("TEST-275 rejects mutable image tags from capacity fingerprints", () => {
  assert.throws(() => hashImageIDs(["postgres:17-alpine"]), /immutable Docker sha256 image IDs/);
});

test("TEST-275 capacity pressure thresholds fail closed and preserve host headroom metrics", () => {
  const gib = 1024 ** 3;
  const safeHost = { totalMemoryBytes: 32 * gib, availableMemoryBytes: 4 * gib, availableDiskBytes: 10 * gib };
  assert.equal(capacitySafetyFailure(safeHost), null);
  assert.match(capacitySafetyFailure({ ...safeHost, availableMemoryBytes: 3 * gib - 1 }), /below 10 percent/);
  assert.match(capacitySafetyFailure({ ...safeHost, availableDiskBytes: 5 * gib - 1 }), /less than 5 GiB/);
  assert.match(capacitySafetyFailure({ ...safeHost, availableDiskBytes: null }), /unavailable or invalid/);

  const report = summarizeResourceSamples("owned-project", [{
    timestamp: "2026-09-21T00:00:00Z",
    containers: [],
    volumes: [],
    host: safeHost,
  }]);
  assert.equal(report.samples[0].metrics.hostMemoryPercent.value, 12.5);
  assert.equal(report.samples[0].metrics.hostAvailableDisk.value, 10 * gib);
  assert.equal(report.samples[0].metrics.hostAvailableDisk.source, "docker_data_root_statfs");
  assert.equal(report.samples[0].metrics.hostAvailableMemory.source, "linux_proc_meminfo");
});
