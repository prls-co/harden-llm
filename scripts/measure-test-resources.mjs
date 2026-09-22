// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-275

import { createHash } from "node:crypto";

const BYTE_UNITS = new Map([
  ["b", 1],
  ["byte", 1],
  ["bytes", 1],
  ["kb", 1_000],
  ["mb", 1_000_000],
  ["gb", 1_000_000_000],
  ["tb", 1_000_000_000_000],
  ["kib", 1_024],
  ["mib", 1_048_576],
  ["gib", 1_073_741_824],
  ["tib", 1_099_511_627_776],
]);

const metric = (value, source, nullReason = null, unit = "bytes") => ({
  value,
  unit,
  source,
  ...(nullReason ? { nullReason } : {}),
});

function unknown(source, reason, unit = "bytes") {
  return metric(null, source, reason, unit);
}

/**
 * Parse a byte count without guessing at omitted/unknown units. Numeric inputs
 * are Docker-API byte counts; strings are the human-readable Docker CLI form.
 */
export function parseResourceBytes(value) {
  if (typeof value === "number") {
    if (Number.isSafeInteger(value) && value >= 0) return metric(value, "numeric_bytes");
    return unknown("numeric_bytes", "numeric byte count must be a non-negative safe integer");
  }
  if (typeof value !== "string" || value.trim() === "") {
    return unknown("unknown", "byte count is missing or is not a string/number");
  }

  const match = value.trim().match(/^(\d+(?:\.\d+)?)\s*([a-zA-Z]+)$/);
  if (!match) return unknown("docker_cli", "byte count does not include a supported numeric value and unit");
  const multiplier = BYTE_UNITS.get(match[2].toLowerCase());
  if (multiplier === undefined) return unknown("docker_cli", `unsupported byte unit: ${match[2]}`);
  const scaled = Number(match[1]) * multiplier;
  if (!Number.isSafeInteger(scaled) || scaled < 0) {
    return unknown("docker_cli", "converted byte count is not an exact safe integer");
  }
  return metric(scaled, "docker_cli");
}

function exactProjectResources(resources, project) {
  return resources.filter((resource) =>
    resource?.labels && resource.labels["com.docker.compose.project"] === project,
  );
}

function sumMetric(resources, readValue, source, unit = "bytes") {
  if (resources.length === 0) return metric(0, source, null, unit);
  let total = 0;
  for (const resource of resources) {
    const parsed = readValue(resource);
    if (parsed.value === null) return unknown(source, parsed.nullReason, unit);
    total += parsed.value;
    if (!Number.isSafeInteger(total)) return unknown(source, "aggregate is not an exact safe integer", unit);
  }
  return metric(total, source, null, unit);
}

function sumPercentMetric(resources, readValue, source) {
  if (resources.length === 0) return metric(0, source, null, "percent");
  let total = 0;
  for (const resource of resources) {
    const parsed = readValue(resource);
    if (parsed.value === null) return unknown(source, parsed.nullReason, "percent");
    total += parsed.value;
    if (!Number.isFinite(total)) return unknown(source, "aggregate is not a finite number", "percent");
  }
  // Docker's CLI percentage is display-rounded; avoid leaking binary-float
  // addition noise while retaining substantially more precision than it reports.
  return metric(Number(total.toPrecision(12)), source, null, "percent");
}

function percentMetric(value, source, reason) {
  const normalized = typeof value === "string" && value.trim().endsWith("%")
    ? Number(value.trim().slice(0, -1))
    : value;
  if (typeof normalized === "number" && Number.isFinite(normalized) && normalized >= 0) {
    return metric(normalized, source, null, "percent");
  }
  return unknown(source, reason, "percent");
}

export function parseResourcePercent(value) {
  return percentMetric(value, "docker_cli_cpu_percent", "CPU percentage is missing or invalid");
}

function measuredMetric(value, unavailableReason, parse, source, unit = "bytes") {
  const parsed = parse(value);
  return parsed.value === null
    ? unknown(source, unavailableReason ?? parsed.nullReason, unit)
    : metric(parsed.value, source, null, unit);
}

export function capacitySafetyFailure(host) {
  const { availableMemoryBytes, totalMemoryBytes, availableDiskBytes } = host ?? {};
  for (const [name, value] of Object.entries({ availableMemoryBytes, totalMemoryBytes, availableDiskBytes })) {
    if (!Number.isSafeInteger(value) || value < 0) return `capacity safety stop: ${name} is unavailable or invalid`;
  }
  if (totalMemoryBytes === 0 || BigInt(availableMemoryBytes) * 10n < BigInt(totalMemoryBytes)) {
    return "capacity safety stop: host available memory is below 10 percent";
  }
  if (availableDiskBytes < 5 * 1024 * 1024 * 1024) {
    return "capacity safety stop: host filesystem has less than 5 GiB available";
  }
  return null;
}

function outputText(result, label) {
  if (!result || result.status !== 0 || (result.truncatedBytes ?? 0) > 0) {
    const exit = Number.isInteger(result?.status) ? `exit=${result.status}` : "exit=unknown";
    const truncation = (result?.truncatedBytes ?? 0) > 0 ? `; truncatedBytes=${result.truncatedBytes}` : "";
    const timeout = result?.timedOut ? "; timedOut=true" : "";
    const stderr = typeof result?.redactedStderr === "string"
      ? result.redactedStderr.replace(/\s+/g, " ").trim().slice(-256)
      : "";
    throw new DockerSampleCommandError(`${label} did not produce a complete successful result (${exit}${timeout}${truncation}${stderr ? `; stderr=${stderr}` : ""})`);
  }
  if (typeof result.stdout !== "string") throw new Error(`${label} output is unavailable`);
  return result.stdout;
}

class DockerSampleCommandError extends Error {}

function outputLines(value) {
  return value.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);
}

function parseLabels(lines, label, expectedCount) {
  if (lines.length !== expectedCount) throw new Error(`${label} count did not match inventory`);
  return lines.map((line) => {
    let labels;
    try { labels = JSON.parse(line); } catch { throw new Error(`${label} is not valid JSON`); }
    if (labels === null || typeof labels !== "object" || Array.isArray(labels)) throw new Error(`${label} is not a label object`);
    return labels;
  });
}

/**
 * Collects one bounded local Docker sample without retaining inspect output.
 * An executor may supply a short, already-redacted `redactedStderr` preview for
 * nonzero commands; raw stderr is never accepted into this diagnostic field.
 */
export async function collectDockerResourceSample(project, execute, { timestamp = new Date().toISOString(), host = {} } = {}) {
  if (typeof project !== "string" || !/^[A-Za-z0-9_.-]{1,128}$/.test(project)) throw new TypeError("project must be a safe exact Docker project label");
  if (typeof execute !== "function") throw new TypeError("Docker command executor is required");
  const sample = { timestamp, host, collectionNullReasons: {} };

  let containerStage = "container inventory command";
  try {
    const ids = outputLines(outputText(await execute([
      "ps", "--all", "--filter", `label=com.docker.compose.project=${project}`,
      "--filter", "status=running", "--format", "{{.ID}}",
    ]), "container inventory"));
    if (ids.length > 64 || ids.some((id) => !/^[a-f0-9]{12,64}$/i.test(id)) || new Set(ids).size !== ids.length) {
      throw new Error("container inventory exceeded bounds or contained invalid IDs");
    }
    containerStage = "container identity inspection";
    const inspectRows = ids.length === 0 ? [] : outputLines(outputText(await execute([
      "inspect", "--format", "{{json .Config.Labels}}|{{.Image}}", ...ids,
    ]), "container identity inventory"));
    if (inspectRows.length !== ids.length) throw new Error("container identity count did not match inventory");
    const identities = inspectRows.map((row) => {
      const separator = row.lastIndexOf("|");
      if (separator < 0) throw new Error("container identity row is malformed");
      let labels;
      try { labels = JSON.parse(row.slice(0, separator)); } catch { throw new Error("container labels are invalid JSON"); }
      const imageId = row.slice(separator + 1);
      if (!/^sha256:[a-f0-9]{64}$/i.test(imageId)) throw new Error("container immutable image ID is invalid");
      return { labels, imageId };
    });
    const exact = ids.flatMap((id, index) => identities[index].labels?.["com.docker.compose.project"] === project
      ? [{ id, imageId: identities[index].imageId, labels: { "com.docker.compose.project": project } }]
      : []);
    const stats = new Map();
    if (exact.length > 0) {
      containerStage = "container stats command";
      const rows = outputLines(outputText(await execute([
        "stats", "--no-stream", "--format", "{{.ID}}\t{{.MemUsage}}\t{{.CPUPerc}}", ...exact.map((container) => container.id),
      ]), "container resource sample"));
      for (const row of rows) {
        const [id, memory, cpu, ...extra] = row.split("\t");
        if (extra.length > 0 || !/^[a-f0-9]{12,64}$/i.test(id)) throw new Error("container stats row is malformed");
        const memoryValue = memory?.split("/")[0]?.trim();
        const memoryMetric = parseResourceBytes(memoryValue);
        const cpuMetric = parseResourcePercent(cpu);
        stats.set(id, {
          memoryUsageBytes: memoryMetric.value,
          memoryUsageNullReason: memoryMetric.value === null
            ? (memoryValue ? memoryMetric.nullReason : "Docker stats memory field is empty")
            : null,
          cpuPercent: cpuMetric.value,
          cpuPercentNullReason: cpuMetric.value === null
            ? (cpu?.trim() ? cpuMetric.nullReason : "Docker stats CPU field is empty")
            : null,
        });
      }
      if (stats.size !== exact.length || exact.some((container) => !stats.has(container.id))) throw new Error("container stats did not cover the exact running inventory");
    }
    sample.containers = exact.map((container) => {
      const resource = stats.get(container.id);
      return {
        ...container,
        imageId: container.imageId,
        memoryUsageBytes: resource.memoryUsageBytes,
        memoryUsageNullReason: resource.memoryUsageNullReason,
        processRssBytes: null,
        processRssNullReason: "process RSS is not exposed by Docker stats",
        cpuPercent: resource.cpuPercent,
        cpuPercentNullReason: resource.cpuPercentNullReason,
      };
    });
  } catch (error) {
    const detail = error instanceof DockerSampleCommandError ? `: ${error.message}` : "";
    sample.collectionNullReasons.containers = `${containerStage} failed${detail}`;
  }

  try {
    const names = outputLines(outputText(await execute([
      "volume", "ls", "--filter", `label=com.docker.compose.project=${project}`, "--format", "{{.Name}}",
    ]), "volume inventory"));
    if (names.length > 64 || names.some((name) => !/^[A-Za-z0-9][A-Za-z0-9_.-]{0,254}$/.test(name)) || new Set(names).size !== names.length) {
      throw new Error("volume inventory exceeded bounds or contained invalid names");
    }
    const labels = names.length === 0 ? [] : parseLabels(outputLines(outputText(await execute([
      "volume", "inspect", "--format", "{{json .Labels}}", ...names,
    ]), "volume label inventory")), "volume labels", names.length);
    sample.volumes = names.flatMap((name, index) => labels[index]?.["com.docker.compose.project"] === project
      ? [{ name, labels: { "com.docker.compose.project": project }, size: null, sizeNullReason: "Docker volume used bytes are not collected" }]
      : []);
  } catch {
    sample.collectionNullReasons.volumes = "exact project volume sample could not be collected";
  }
  return sample;
}

function summarizeHostPressure(pressure) {
  const result = {};
  for (const resource of ["cpu", "memory", "io"]) {
    for (const scope of ["some", "full"]) {
      const key = `${resource}${scope[0].toUpperCase()}${scope.slice(1)}Avg10`;
      const value = pressure?.[key];
      result[key] = percentMetric(value, "linux_psi_avg10", `host ${key} is unavailable`);
    }
  }
  return result;
}

function summarizeSampling(samples, expectedIntervalMs) {
  const timestamps = samples.map((sample) => {
    const parsed = Date.parse(sample?.timestamp ?? "");
    return Number.isFinite(parsed) ? parsed : null;
  });
  const valid = timestamps.filter((timestamp) => timestamp !== null);
  const intervalsMs = [];
  for (let index = 1; index < timestamps.length; index++) {
    const previous = timestamps[index - 1];
    const current = timestamps[index];
    intervalsMs.push(previous === null || current === null ? null : current - previous);
  }
  const validIntervals = intervalsMs.filter((interval) => interval !== null && interval >= 0);
  const invalidIntervalIndexes = intervalsMs.flatMap((interval, index) => interval === null || interval < 0 ? [index + 1] : []);
  const hasExpectedInterval = Number.isSafeInteger(expectedIntervalMs) && expectedIntervalMs > 0;
  return {
    sampleCount: samples.length,
    firstTimestamp: valid.length ? new Date(valid[0]).toISOString() : null,
    lastTimestamp: valid.length ? new Date(valid[valid.length - 1]).toISOString() : null,
    intervalsMs,
    maxObservedIntervalMs: validIntervals.length ? Math.max(...validIntervals) : null,
    invalidIntervalIndexes,
    expectedIntervalMs: hasExpectedInterval ? expectedIntervalMs : null,
    overExpectedIntervalIndexes: hasExpectedInterval
      ? intervalsMs.flatMap((interval, index) => interval !== null && interval > expectedIntervalMs ? [index + 1] : [])
      : null,
    cadenceNullReason: !hasExpectedInterval
      ? "expected sample interval was not supplied; missing samples cannot be inferred"
      : (invalidIntervalIndexes.length ? "one or more adjacent sample timestamps are invalid or out of order" : null),
  };
}

/**
 * Summarize one run's sampled Docker/host values. Only exact Compose project
 * labels contribute to container and volume totals. RSS is accepted only from
 * an explicit process-RSS field; Docker memory usage is never relabeled as RSS.
 */
export function summarizeResourceSamples(project, samples, { expectedIntervalMs } = {}) {
  if (typeof project !== "string" || project.trim() === "") throw new TypeError("project must be a non-empty string");
  if (!Array.isArray(samples)) throw new TypeError("samples must be an array");

  const normalized = samples.map((sample) => {
    const containersAvailable = Array.isArray(sample?.containers);
    const volumesAvailable = Array.isArray(sample?.volumes);
    const containers = exactProjectResources(containersAvailable ? sample.containers : [], project);
    const volumes = exactProjectResources(volumesAvailable ? sample.volumes : [], project);
    const dockerMemoryUsage = containersAvailable ? sumMetric(
      containers,
      (container) => measuredMetric(
        container.memoryUsageBytes,
        container.memoryUsageNullReason,
        parseResourceBytes,
        "docker_container_memory_usage_bytes",
      ),
      "docker_container_memory_usage_bytes",
    ) : unknown("docker_container_memory_usage_bytes", "container inventory sample is unavailable");
    const processRSS = containersAvailable ? sumMetric(
      containers,
      (container) => measuredMetric(
        container.processRssBytes,
        container.processRssNullReason ?? "process RSS is not available in this sample",
        parseResourceBytes,
        "process_rss_bytes",
      ),
      "process_rss_bytes",
    ) : unknown("process_rss_bytes", "container inventory sample is unavailable");
    const cpuPercent = containersAvailable ? sumPercentMetric(
      containers,
      (container) => measuredMetric(
        container.cpuPercent,
        container.cpuPercentNullReason ?? "container CPU percentage is unavailable",
        (value) => percentMetric(value, "docker_container_cpu_percent", "container CPU percentage is unavailable"),
        "docker_container_cpu_percent",
        "percent",
      ),
      "docker_container_cpu_percent",
    ) : unknown("docker_container_cpu_percent", "container inventory sample is unavailable", "percent");
    const diskBytes = volumesAvailable
      ? sumMetric(volumes, (volume) => measuredMetric(
        volume.size,
        volume.sizeNullReason ?? "Docker volume used bytes are not available in this sample",
        parseResourceBytes,
        "docker_volume_size_bytes",
      ), "docker_volume_size_bytes")
      : unknown("docker_volume_size_bytes", "volume inventory sample is unavailable");
    const hostAvailableMemory = Number.isSafeInteger(sample?.host?.availableMemoryBytes) && sample.host.availableMemoryBytes >= 0
      ? metric(sample.host.availableMemoryBytes, "linux_proc_meminfo")
      : unknown("linux_proc_meminfo", "host available memory sample is unavailable");
    const hostAvailableDisk = Number.isSafeInteger(sample?.host?.availableDiskBytes) && sample.host.availableDiskBytes >= 0
      ? metric(sample.host.availableDiskBytes, "docker_data_root_statfs")
      : unknown("docker_data_root_statfs", "Docker data-root available disk sample is unavailable");
    const hostMemoryPercent = Number.isSafeInteger(sample?.host?.availableMemoryBytes)
      && Number.isSafeInteger(sample?.host?.totalMemoryBytes)
      && sample.host.totalMemoryBytes > 0
      ? metric((sample.host.availableMemoryBytes / sample.host.totalMemoryBytes) * 100, "linux_proc_meminfo", null, "percent")
      : unknown("linux_proc_meminfo", "host available-memory percentage is unavailable", "percent");

    return {
      timestamp: typeof sample?.timestamp === "string" ? sample.timestamp : null,
      containerCount: containersAvailable ? containers.length : null,
      volumeCount: volumesAvailable ? volumes.length : null,
      imageIds: containersAvailable ? containers.map((container) => container.imageId).sort() : null,
      inventoryNullReasons: {
        ...(!containersAvailable ? { containers: "container inventory sample is unavailable" } : {}),
        ...(!volumesAvailable ? { volumes: "volume inventory sample is unavailable" } : {}),
      },
      collectionNullReasons: sample?.collectionNullReasons ?? {},
      metrics: { dockerMemoryUsage, processRSS, cpuPercent, diskBytes, hostAvailableMemory, hostAvailableDisk, hostMemoryPercent },
      hostPressure: summarizeHostPressure(sample?.host?.pressure),
    };
  });

  const imageSets = normalized.flatMap((sample) => {
    if (sample.containerCount === null) return [null];
    return [sample.imageIds];
  });
  const firstImageSet = imageSets[0] ?? [];
  const imageSetStable = imageSets.length > 0 &&
    firstImageSet.every((imageID) => typeof imageID === "string" && /^sha256:[a-f0-9]{64}$/i.test(imageID)) &&
    imageSets.every((set) => Array.isArray(set) && JSON.stringify(set) === JSON.stringify(firstImageSet));
  const uniqueImageSet = [...new Set(firstImageSet)].sort();
  const imageSet = imageSetStable
    ? { sha256: hashImageIDs(uniqueImageSet), imageCount: uniqueImageSet.length, source: "exact_project_docker_inspect_image_ids" }
    : { sha256: null, imageCount: null, source: "exact_project_docker_inspect_image_ids", nullReason: "image inventory was unavailable or changed between resource samples" };

  const sampledPeak = (key, unit) => {
    const known = normalized.flatMap((sample) => sample.metrics[key].value === null ? [] : [sample.metrics[key].value]);
    if (known.length === 0) return unknown("sampled_peak", `no known ${key} samples`, unit);
    return metric(Math.max(...known), `sampled_peak_of_${key}`, null, unit);
  };

  return {
    project,
    imageSet,
    sampling: summarizeSampling(samples, expectedIntervalMs),
    samples: normalized,
    peaks: {
      dockerMemoryUsage: sampledPeak("dockerMemoryUsage", "bytes"),
      processRSS: sampledPeak("processRSS", "bytes"),
      cpuPercent: sampledPeak("cpuPercent", "percent"),
    },
  };
}

export function hashImageIDs(imageIDs) {
  if (!Array.isArray(imageIDs) || imageIDs.some((value) => typeof value !== "string" || !/^sha256:[a-f0-9]{64}$/i.test(value))) {
    throw new TypeError("imageIDs must be a list of immutable Docker sha256 image IDs");
  }
  const normalized = [...new Set(imageIDs)].sort();
  return createHash("sha256").update(JSON.stringify(normalized)).digest("hex");
}
