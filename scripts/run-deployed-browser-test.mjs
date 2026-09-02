#!/usr/bin/env node

// PLAN-HLLM-WIDGET-PARITY-001 TEST-118

import { execFileSync, spawnSync } from "node:child_process";
import { promises as fs } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const browserImage = "harden-llm-browser-test:local";
const composeFiles = [
  "docker-compose.yml",
  "deploy/langfuse/docker-compose.upstream.yml",
  "deploy/langfuse/compose.private.yml",
  "deploy/frontend/compose.frontend.yml",
];
const dotEnvKeys = new Set([
  "HARDEN_LLM_LOCAL_OPERATOR_EMAIL",
  "HARDEN_LLM_LOCAL_OPERATOR_PASSWORD",
  "HARDEN_LLM_LIVE_USER_EMAIL",
  "HARDEN_LLM_LIVE_USER_PASSWORD",
  "HARDEN_LLM_WEB_HOST",
  "HARDEN_LLM_API_HOST",
  "HARDEN_LLM_EXPECTED_RELEASE",
  "HARDEN_LLM_COMPOSE_ENV_FILE",
]);

function parseDotEnv(contents) {
  const values = {};
  for (const line of contents.split(/\r?\n/)) {
    const match = line.match(/^\s*(?:export\s+)?([A-Z][A-Z0-9_]*)\s*=\s*(.*)\s*$/);
    if (!match || !dotEnvKeys.has(match[1])) continue;
    let value = match[2];
    if ((value.startsWith("\"") && value.endsWith("\"")) || (value.startsWith("'") && value.endsWith("'"))) {
      value = value.slice(1, -1);
    }
    values[match[1]] = value;
  }
  return values;
}

async function loadDotEnv() {
  const candidates = [
    process.env.HARDEN_LLM_COMPOSE_ENV_FILE,
    path.join(repositoryRoot, ".env"),
  ].filter(Boolean);
  for (const candidate of candidates) {
    try {
      return parseDotEnv(await fs.readFile(candidate, "utf8"));
    } catch {
      // Try the next approved environment-file location.
    }
  }
  return {};
}

function firstValue(environment, names) {
  for (const name of names) {
    if (environment[name]) return environment[name];
  }
  return null;
}

function hostOrigin(host, name) {
  const value = String(host ?? "").trim();
  if (!value) throw new Error(`${name} is missing`);
  const origin = value.startsWith("http://") || value.startsWith("https://") ? value : `https://${value}`;
  const parsed = new URL(origin);
  if (!parsed.hostname || parsed.pathname !== "/" || parsed.search || parsed.hash) throw new Error(`${name} is not a host origin`);
  return parsed.origin;
}

function command(command, args, options = {}) {
  return execFileSync(command, args, {
    cwd: repositoryRoot,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "ignore"],
    timeout: options.timeoutMs ?? 30_000,
    env: options.env ?? process.env,
  }).trim();
}

function composeArgs(environment = {}) {
  const envFile = firstValue(environment, ["HARDEN_LLM_COMPOSE_ENV_FILE"]);
  return [
    "compose",
    ...(envFile ? ["--env-file", envFile] : []),
    "-p",
    "harden-llm",
    ...composeFiles.flatMap((file) => ["-f", file]),
  ];
}

function composeInspectionEnvironment(environment, expectedRelease) {
  const result = { ...environment };
  if (firstValue(environment, ["HARDEN_LLM_COMPOSE_ENV_FILE"])) {
    for (const name of Object.keys(result)) {
      if (name.startsWith("HARDEN_LLM_") || name.startsWith("LANGFUSE_") || name.startsWith("MINIO_") || name.startsWith("CLICKHOUSE_") || name.startsWith("REDIS_") || name.startsWith("GRAFANA_")) {
        delete result[name];
      }
    }
    result.HARDEN_LLM_RELEASE = expectedRelease;
  }
  return result;
}

function cleanRepositoryRelease() {
  if (command("git", ["status", "--porcelain"])) throw new Error("checkout is not clean before deployed certification");
  return command("git", ["rev-parse", "HEAD"]);
}

async function expectedRelease(environment) {
  if (environment.HARDEN_LLM_EXPECTED_RELEASE) {
    return { value: environment.HARDEN_LLM_EXPECTED_RELEASE, source: "explicit diagnostic override" };
  }
  try {
    const status = JSON.parse(await fs.readFile(path.join(repositoryRoot, "plans", "implementation-status.json"), "utf8"));
    const release = status.testFeedbackHierarchy?.applicationRelease;
    const value = typeof release === "string"
      ? release
      : release?.frontendRelease ?? release?.frontendLabel ?? release?.commitSHA ?? null;
    if (value) return { value, source: "implementation status" };
  } catch {
    // The clean checkout fallback below is the required pre-deployment path.
  }
  return { value: cleanRepositoryRelease(), source: "clean checkout HEAD" };
}

function inspectFrontendContainer(environment, expectedRelease) {
  const composeEnvironment = composeInspectionEnvironment(environment, expectedRelease);
  const containerID = command("docker", [...composeArgs(environment), "ps", "-q", "harden-llm-web"], { env: composeEnvironment });
  if (!containerID) throw new Error("harden-llm-web container is not running in project harden-llm");
  const records = JSON.parse(command("docker", ["inspect", containerID], { env: composeEnvironment }));
  const record = records[0];
  const labels = record?.Config?.Labels ?? {};
  const releaseLabel = labels["org.opencontainers.image.revision"] ?? labels["org.opencontainers.image.version"] ?? null;
  if (!releaseLabel) throw new Error("frontend image has no immutable release label");
  return {
    containerID: containerID.slice(0, 12),
    imageID: record?.Image ?? null,
    releaseLabel,
    health: record?.State?.Health?.Status ?? record?.State?.Status ?? "unknown",
  };
}

async function probe(origin, route) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 15_000);
  try {
    const response = await fetch(`${origin}${route}`, { redirect: "manual", signal: controller.signal });
    if (!response.ok) throw new Error(`${route} returned HTTP ${response.status}`);
    return response.status;
  } finally {
    clearTimeout(timer);
  }
}

function runDeployedBrowser(environment, webOrigin, apiOrigin, nonce) {
  const childEnvironment = {
    ...environment,
    HARDEN_LLM_DEPLOYED_BASE_URL: webOrigin,
    HARDEN_LLM_API_BASE_URL: apiOrigin,
    HARDEN_LLM_PUBLIC_API_BASE_URL: apiOrigin,
    HARDEN_LLM_SMOKE_NONCE: nonce,
    HARDEN_LLM_DEPLOYED_NO_SCREENSHOTS: "1",
  };
  const inheritedNames = [
    "HARDEN_LLM_DEPLOYED_BASE_URL",
    "HARDEN_LLM_API_BASE_URL",
    "HARDEN_LLM_PUBLIC_API_BASE_URL",
    "HARDEN_LLM_LOCAL_OPERATOR_EMAIL",
    "HARDEN_LLM_LOCAL_OPERATOR_PASSWORD",
    "HARDEN_LLM_SMOKE_NONCE",
    "HARDEN_LLM_DEPLOYED_NO_SCREENSHOTS",
  ];
  const args = [
    "run",
    "--rm",
    "--network",
    "host",
    "--shm-size",
    "2g",
    "--mount",
    `type=bind,src=${repositoryRoot},dst=/workspace`,
    "--mount",
    "type=tmpfs,destination=/workspace/frontend/tmp/wallaby,tmpfs-mode=1777",
    "--workdir",
    "/workspace/frontend",
    ...inheritedNames.flatMap((name) => ["--env", name]),
    browserImage,
    "sh",
    "-c",
    "mix local.hex --force >/dev/null 2>&1 && mix local.rebar --force >/dev/null 2>&1 && mix deps.get >/dev/null && exec mix test --only deployed --max-cases 1 test/browser/deployed_canary_test.exs",
  ];
  const result = spawnSync("docker", args, {
    cwd: repositoryRoot,
    env: childEnvironment,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
    timeout: 300_000,
  });
  if (result.error?.code === "ETIMEDOUT") throw new Error("deployed browser canary timed out");
  if (result.error) throw new Error(`deployed browser launcher failed before test: ${result.error.code ?? "spawn"}`);
  if (result.status !== 0) {
    const detail = sanitizeBrowserFailure(`${result.stdout ?? ""}\n${result.stderr ?? ""}`, childEnvironment);
    throw new Error(`deployed browser canary failed with exit ${result.status ?? "signal"}: ${detail}`);
  }
}

function sanitizeBrowserFailure(output, environment) {
  let sanitized = String(output ?? "");
  for (const name of ["HARDEN_LLM_LOCAL_OPERATOR_EMAIL", "HARDEN_LLM_LOCAL_OPERATOR_PASSWORD"]) {
    const value = environment[name];
    if (value) sanitized = sanitized.replaceAll(value, "[REDACTED]");
  }
  sanitized = sanitized.replace(/Bearer\s+[A-Za-z0-9._~-]+/gi, "Bearer [REDACTED]").trim();
  if (!sanitized) return "no test output was captured";
  return sanitized.slice(-8_000);
}

async function cleanupScreenshots() {
  const screenshotDirectory = path.join(repositoryRoot, "frontend", "tmp", "wallaby");
  try {
    await fs.rm(screenshotDirectory, { recursive: true, force: true });
  } catch (error) {
    if (error?.code !== "EACCES" && error?.code !== "EPERM") throw error;
    execFileSync("docker", [
      "run",
      "--rm",
      "--mount",
      `type=bind,src=${screenshotDirectory},dst=/artifacts`,
      browserImage,
      "chmod",
      "-R",
      "a+rwX",
      "/artifacts",
    ], { cwd: repositoryRoot, stdio: "ignore", timeout: 30_000 });
    await fs.rm(screenshotDirectory, { recursive: true, force: true });
  }
}

export async function main() {
  const fileEnvironment = await loadDotEnv();
  const environment = { ...fileEnvironment, ...process.env };
  environment.HARDEN_LLM_LOCAL_OPERATOR_EMAIL = firstValue(environment, [
    "HARDEN_LLM_LOCAL_OPERATOR_EMAIL",
    "HARDEN_LLM_LIVE_USER_EMAIL",
  ]) ?? "";
  environment.HARDEN_LLM_LOCAL_OPERATOR_PASSWORD = firstValue(environment, [
    "HARDEN_LLM_LOCAL_OPERATOR_PASSWORD",
    "HARDEN_LLM_LIVE_USER_PASSWORD",
  ]) ?? "";
  const webOrigin = hostOrigin(firstValue(environment, ["HARDEN_LLM_WEB_HOST"]), "HARDEN_LLM_WEB_HOST");
  const apiOrigin = hostOrigin(firstValue(environment, ["HARDEN_LLM_API_HOST"]), "HARDEN_LLM_API_HOST");
  if (!environment.HARDEN_LLM_LOCAL_OPERATOR_EMAIL || !environment.HARDEN_LLM_LOCAL_OPERATOR_PASSWORD) {
    throw new Error("operator credentials are missing from approved environment names");
  }
  const expected = await expectedRelease(environment);
  const identity = inspectFrontendContainer(environment, expected.value);
  if (identity.releaseLabel !== expected.value) {
    console.error(JSON.stringify({
      accepted: false,
      failure: "frontend release mismatch",
      expectedReleaseSource: expected.source,
      frontendIdentityChecked: true,
      authenticatedCanaryStarted: false,
    }));
    return 1;
  }
  const probes = {
    webHealthz: await probe(webOrigin, "/healthz"),
    login: await probe(webOrigin, "/login"),
    apiHealthz: await probe(apiOrigin, "/healthz"),
    apiReadyz: await probe(apiOrigin, "/readyz"),
  };
  const nonce = `harden-llm-deployed-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
  await cleanupScreenshots();
  try {
    runDeployedBrowser(environment, webOrigin, apiOrigin, nonce);
  } finally {
    await cleanupScreenshots();
  }
  console.log(JSON.stringify({ accepted: true, expectedReleaseSource: expected.source, identity, probes, smokeHistoryCleanup: "asserted by canary" }));
  return 0;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().then((exitCode) => {
    if (Number.isInteger(exitCode)) {
      process.exitCode = exitCode;
    }
  }).catch((error) => {
    console.error(JSON.stringify({
      accepted: false,
      failure: "deployed certification failed",
      detail: error instanceof Error ? error.message : "unknown certification failure",
    }));
    process.exitCode = 1;
  });
}
