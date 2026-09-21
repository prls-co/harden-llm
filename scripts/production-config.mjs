#!/usr/bin/env node

// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-233 TEST-234 TEST-235
//
// This module is deliberately a small, read-only-by-default boundary around
// Docker Compose.  It resolves approved files in memory, never executes dotenv
// contents as shell code, and never prints a resolved environment or inspect
// response.  The descriptor is trusted host metadata, not branch input.

import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  chmodSync,
  existsSync,
  lstatSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  realpathSync,
  rmSync,
  statSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parseEnv } from "node:util";
import { sharedApplicationVariables } from "./shared-profiles.mjs";

export const SPECIFICATION_ID = "SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001";
export const DEFAULT_DESCRIPTOR_PATH = "/home/kirill/.config/harden-llm/production.json";
export const COMPOSE_FILE_ORDER = Object.freeze([
  "docker-compose.yml",
  "deploy/langfuse/docker-compose.upstream.yml",
  "deploy/langfuse/compose.private.yml",
  "deploy/frontend/compose.frontend.yml",
]);
export const PRLS_REQUIRED_VARIABLES = Object.freeze([
  "PRLS_ALLURE_HOST",
  "PRLS_TESTS_BASIC_AUTH_USER",
  "PRLS_TESTS_BASIC_AUTH_HASH",
  "PRLS_LAMINAR_PROJECT_API_KEY",
  "PRLS_LOKI_S3_ACCESS_KEY",
  "PRLS_LOKI_S3_SECRET_KEY",
]);
export const APPLICATION_SERVICES = Object.freeze(["harden-llm-gateway", "harden-llm-web"]);

const DEFAULT_REQUIRED_VARIABLES = Object.freeze([...PRLS_REQUIRED_VARIABLES, "HARDEN_LLM_RELEASE"]);
const HOST_ENVIRONMENT_KEYS = Object.freeze([
  "PATH",
  "HOME",
  "LANG",
  "LC_ALL",
  "TMPDIR",
  "XDG_RUNTIME_DIR",
]);
const DOCKER_TRANSPORT_KEYS = new Set(["DOCKER_CONFIG", "DOCKER_HOST", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH"]);
const SAFE_DESCRIPTOR_ENVIRONMENT_KEY = /^[A-Z][A-Z0-9_]*$/;
const RELEASE_ENVIRONMENT_KEY = /(?:^|_)RELEASE$/;
const SENSITIVE_KEY = /(password|secret|token|api[_-]?key|access[_-]?key|encryption|database|bearer)/i;
const ALLOWED_DIFFERENCE_FIELDS = new Set(["environment", "image", "mounts", "mount-contents", "command", "entrypoint", "networks", "ports", "healthcheck", "restart", "identity", "runtime"]);
const RELEASE_SHA_PATTERN = /^[0-9a-f]{40}$/;

export class ProductionConfigError extends Error {
  constructor(message, code = "invalid_configuration") {
    super(message);
    this.name = "ProductionConfigError";
    this.code = code;
  }
}

function fail(message, code = "invalid_configuration") {
  throw new ProductionConfigError(message, code);
}

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function assertString(value, name) {
  if (typeof value !== "string" || value.length === 0) fail(`${name} is required`);
  return value;
}

function assertAbsolutePath(value, name) {
  assertString(value, name);
  if (!path.isAbsolute(value)) fail(`${name} must be absolute`);
  return path.normalize(value);
}

function assertDigest(value, name) {
  assertString(value, name);
  if (!/^sha256:[a-f0-9]{64}$/.test(value)) fail(`${name} must be a sha256 image ID`);
  return value;
}

function descriptorServiceNames(descriptor) {
  return Object.keys(descriptor.services).sort();
}

function normalizeSelectedServices(descriptor, services) {
  const selected = [...new Set(services)].sort();
  if (selected.length === 0) fail("at least one service is required", "invalid_arguments");
  for (const service of selected) {
    if (!descriptor.services[service]) fail(`service ${service} is not in the descriptor`, "scope_blocked");
  }
  return selected;
}

function normalizeExpectedRelease(expectedRelease) {
  if (expectedRelease === undefined || expectedRelease === null) return null;
  if (typeof expectedRelease !== "string" || !RELEASE_SHA_PATTERN.test(expectedRelease)) {
    fail("expected release must be a 40-character lowercase hexadecimal SHA", "invalid_arguments");
  }
  return expectedRelease;
}

function validateCandidateScope(services, expectedRelease, resolveOnly) {
  if (!expectedRelease) return;
  if (resolveOnly) fail("--expected-release cannot be combined with --resolve-only", "invalid_arguments");
  if (services.some((service) => !APPLICATION_SERVICES.includes(service))) {
    fail("--expected-release is limited to harden-llm-gateway and harden-llm-web", "scope_blocked");
  }
}

function validateDescriptorEnvironment(values, name) {
  if (!isObject(values)) fail(`${name} must be an object`);
  for (const [key, value] of Object.entries(values)) {
    if (!SAFE_DESCRIPTOR_ENVIRONMENT_KEY.test(key)) fail(`${name} has an invalid variable name`);
    if (typeof value !== "string" || value.length === 0) fail(`${name}.${key} must be a nonempty string`);
    if (SENSITIVE_KEY.test(key) && !RELEASE_ENVIRONMENT_KEY.test(key)) {
      fail(`${name} contains a credential-shaped variable`);
    }
  }
}

function validateImageOverrides(values, name, services) {
  if (values === undefined) return;
  if (!isObject(values)) fail(`${name} is invalid`);
  for (const [service, image] of Object.entries(values)) {
    if (!Object.hasOwn(services, service)) fail(`${name} names unknown service ${service}`);
    if (typeof image !== "string" || image.length === 0 || SENSITIVE_KEY.test(image)) fail(`${name}.${service} is invalid`);
  }
}

export function validateDescriptor(input) {
  if (!isObject(input) || input.schemaVersion !== 1) fail("descriptor schemaVersion must be 1");
  assertString(input.project, "descriptor project");
  if (input.project !== "harden-llm") fail("descriptor project is not harden-llm");
  assertString(input.dockerContext, "descriptor dockerContext");
  if (input.dockerTransportEnvironment !== undefined) {
    if (!isObject(input.dockerTransportEnvironment)) fail("descriptor dockerTransportEnvironment is invalid");
    for (const [key, value] of Object.entries(input.dockerTransportEnvironment)) {
      if (!DOCKER_TRANSPORT_KEYS.has(key) || typeof value !== "string" || value.length === 0) fail("descriptor dockerTransportEnvironment is invalid");
    }
  }
  assertAbsolutePath(input.composeRoot, "descriptor composeRoot");
  assertAbsolutePath(input.applicationRoot, "descriptor applicationRoot");
  for (const key of ["productionEnvFile", "observabilityEnvFile", "sharedApplicationEnvFile"]) {
    assertAbsolutePath(input[key], `descriptor ${key}`);
  }
  if (!Array.isArray(input.composeFiles) || JSON.stringify(input.composeFiles) !== JSON.stringify(COMPOSE_FILE_ORDER)) {
    fail("descriptor composeFiles do not match the approved graph");
  }
  if (!isObject(input.services) || Object.keys(input.services).length === 0) fail("descriptor services are required");

  for (const [service, identity] of Object.entries(input.services)) {
    if (!/^[a-z0-9][a-z0-9_-]*$/.test(service)) fail("descriptor has an invalid service name");
    if (!isObject(identity)) fail(`descriptor service ${service} is invalid`);
    assertString(identity.container, `descriptor service ${service}.container`);
    assertDigest(identity.expectedImage, `descriptor service ${service}.expectedImage`);
    if (typeof identity.manageable !== "boolean") fail(`descriptor service ${service}.manageable is required`);
    if (!Array.isArray(identity.ignoredEnvironmentKeys)) fail(`descriptor service ${service}.ignoredEnvironmentKeys is required`);
    for (const key of identity.ignoredEnvironmentKeys) {
      if (!SAFE_DESCRIPTOR_ENVIRONMENT_KEY.test(key)) fail(`descriptor service ${service} has an invalid ignored variable`);
      if (SENSITIVE_KEY.test(key) && !RELEASE_ENVIRONMENT_KEY.test(key)) fail(`descriptor service ${service} ignores a credential-shaped variable`);
    }
    validateDescriptorEnvironment(identity.identityEnvironment ?? {}, `descriptor service ${service}.identityEnvironment`);
    const allowed = identity.allowedDifferenceFields ?? ["environment"];
    if (!Array.isArray(allowed) || allowed.some((field) => typeof field !== "string" || !ALLOWED_DIFFERENCE_FIELDS.has(field))) {
      fail(`descriptor service ${service}.allowedDifferenceFields is invalid`);
    }
    if (identity.compareMountContents !== undefined && typeof identity.compareMountContents !== "boolean") {
      fail(`descriptor service ${service}.compareMountContents is invalid`);
    }
  }

  if (input.serviceEnvironmentOverrides !== undefined) {
    if (!isObject(input.serviceEnvironmentOverrides)) fail("descriptor serviceEnvironmentOverrides is invalid");
    for (const [service, values] of Object.entries(input.serviceEnvironmentOverrides)) {
      if (!Object.hasOwn(input.services, service)) fail(`descriptor override names unknown service ${service}`);
      validateDescriptorEnvironment(values, `descriptor serviceEnvironmentOverrides.${service}`);
    }
  }
  validateImageOverrides(input.serviceImageOverrides, "descriptor serviceImageOverrides", input.services);
  if (input.requiredVariables !== undefined) {
    if (!Array.isArray(input.requiredVariables) || input.requiredVariables.some((key) => !SAFE_DESCRIPTOR_ENVIRONMENT_KEY.test(key))) {
      fail("descriptor requiredVariables is invalid");
    }
  }
  return input;
}

export function loadDescriptor(descriptorPath = DEFAULT_DESCRIPTOR_PATH) {
  const resolvedPath = assertAbsolutePath(descriptorPath, "descriptor path");
  let descriptor;
  try {
    descriptor = JSON.parse(readFileSync(resolvedPath, "utf8"));
  } catch {
    fail("unable to read production descriptor", "missing_descriptor");
  }
  return validateDescriptor({ ...descriptor, descriptorPath: resolvedPath });
}

function assignmentNames(contents) {
  const names = [];
  const duplicates = new Set();
  let quote = null;
  const lines = contents.split(/\r?\n/);
  for (const line of lines) {
    const trimmed = line.trim();
    if (quote !== null) {
      let escaped = false;
      for (const character of line) {
        if (escaped) {
          escaped = false;
        } else if (character === "\\") {
          escaped = true;
        } else if (character === quote) {
          quote = null;
          break;
        }
      }
      continue;
    }
    if (!trimmed || trimmed.startsWith("#")) continue;
    const match = trimmed.match(/^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=/);
    if (!match) fail("environment source contains a malformed assignment");
    const name = match[1];
    if (names.includes(name)) duplicates.add(name);
    names.push(name);
    const rhs = trimmed.slice(match[0].length).trimStart();
    let escaped = false;
    for (let index = 0; index < rhs.length; index += 1) {
      const character = rhs[index];
      if (escaped) {
        escaped = false;
      } else if (character === "\\") {
        escaped = true;
      } else if (quote === null && character === "#" && (index === 0 || /\s/.test(rhs[index - 1]))) {
        break;
      } else if (character === "'" || character === '"') {
        if (quote === null) quote = character;
        else if (quote === character) quote = null;
      }
    }
  }
  if (quote !== null) fail("environment source contains an unterminated quoted value");
  return { names, duplicates: [...duplicates].sort() };
}

export function readEnvironmentSource(filePath, source, { requirePrivateMode = true } = {}) {
  const resolvedPath = assertAbsolutePath(filePath, `${source} environment path`);
  let metadata;
  let contents;
  try {
    metadata = statSync(resolvedPath);
    contents = readFileSync(resolvedPath, "utf8");
  } catch {
    fail(`${source} environment source is unavailable`, "missing_source");
  }
  if (!metadata.isFile()) fail(`${source} environment source is not a regular file`);
  if (requirePrivateMode && (metadata.mode & 0o077) !== 0) fail(`${source} environment source must not be group/world readable`);
  let values;
  try {
    values = parseEnv(contents);
  } catch {
    fail(`${source} environment source is not valid dotenv`);
  }
  const { names, duplicates } = assignmentNames(contents);
  if (duplicates.length > 0) fail(`${source} environment source contains duplicate assignments`, "duplicate_source");
  return {
    source,
    path: resolvedPath,
    mode: metadata.mode & 0o777,
    values,
    names,
    digest: createHash("sha256").update(contents).digest("hex"),
  };
}

export function resolveSourcePrecedence(sources, requiredVariables = DEFAULT_REQUIRED_VARIABLES) {
  const byName = new Map(sources.map((source) => [source.source, source]));
  const observability = byName.get("observability");
  const production = byName.get("production");
  const shared = byName.get("shared-application");
  if (!observability || !production || !shared) fail("approved source set is incomplete");

  const conflicts = [];
  const duplicateNames = new Set([...observability.names, ...production.names].filter((key) => {
    return Object.hasOwn(observability.values, key) && Object.hasOwn(production.values, key);
  }));
  for (const key of [...duplicateNames].sort()) {
    const equal = observability.values[key] === production.values[key];
    if (!equal && key.startsWith("PRLS_")) conflicts.push({ key, kind: "prls_conflict" });
    else if (!equal) conflicts.push({ key, kind: "production_wins" });
  }
  if (conflicts.some(({ kind }) => kind === "prls_conflict")) {
    fail("observability and production sources conflict for a PRLS variable", "source_conflict");
  }

  const effective = { ...observability.values, ...production.values };
  const required = requiredVariables.length > 0 ? requiredVariables : DEFAULT_REQUIRED_VARIABLES;
  for (const key of required) {
    if (!Object.hasOwn(effective, key) || effective[key] === "") fail(`required environment variable ${key} is missing or empty`, "missing_required");
  }
  const sharedValues = sharedApplicationVariables(shared.values);
  return {
    effective,
    sharedValues,
    conflicts,
    sourceDigests: Object.fromEntries(sources.map(({ source, digest }) => [source, digest])),
  };
}

export function resolveConfiguration(descriptor, { readSource = readEnvironmentSource } = {}) {
  validateDescriptor(descriptor);
  const sources = [
    readSource(descriptor.observabilityEnvFile, "observability"),
    readSource(descriptor.productionEnvFile, "production"),
    readSource(descriptor.sharedApplicationEnvFile, "shared-application"),
  ];
  const precedence = resolveSourcePrecedence(sources, descriptor.requiredVariables ?? DEFAULT_REQUIRED_VARIABLES);
  const serviceEnvironmentOverrides = {};
  const overrideServices = new Set([
    ...Object.keys(descriptor.serviceEnvironmentOverrides ?? {}),
    ...Object.keys(descriptor.serviceImageOverrides ?? {}),
  ]);
  for (const service of overrideServices) {
    serviceEnvironmentOverrides[service] = {
      image: descriptor.serviceImageOverrides?.[service],
      environment: descriptor.serviceEnvironmentOverrides?.[service] ?? {},
    };
  }
  return {
    descriptor,
    sources,
    ...precedence,
    environmentFiles: sources.slice(0, 2).map(({ path: filePath }) => filePath),
    serviceEnvironmentOverrides,
  };
}

function controlledEnvironment(sharedValues, ambient = process.env) {
  const environment = {};
  for (const key of HOST_ENVIRONMENT_KEYS) {
    if (ambient[key] !== undefined) environment[key] = ambient[key];
  }
  for (const [key, value] of Object.entries(sharedValues)) environment[key] = value;
  return environment;
}

export function buildComposeEnvironment(resolved, ambient = process.env) {
  const environment = controlledEnvironment(resolved.sharedValues, ambient);
  for (const [key, value] of Object.entries(resolved.descriptor.dockerTransportEnvironment ?? {})) environment[key] = value;
  return environment;
}

function quoteYaml(value) {
  return JSON.stringify(String(value));
}

export function buildServiceOverrideYaml(overrides) {
  const entries = Object.entries(overrides ?? {});
  if (entries.length === 0) return "services: {}\n";
  const lines = ["services:"];
  for (const [service, values] of entries.sort(([left], [right]) => left.localeCompare(right))) {
    lines.push(`  ${service}:`);
    if (values.image !== undefined) lines.push(`    image: ${quoteYaml(values.image)}`);
    if (values.environment && Object.keys(values.environment).length > 0) {
      lines.push("    environment:");
      for (const [key, value] of Object.entries(values.environment).sort(([left], [right]) => left.localeCompare(right))) {
        lines.push(`      ${key}: ${quoteYaml(value)}`);
      }
    }
  }
  return `${lines.join("\n")}\n`;
}

export function buildComposeArguments(descriptor, commandArguments, overridePath = null) {
  const args = ["--context", descriptor.dockerContext, "compose"];
  for (const filePath of descriptor.composeFiles) args.push("-f", path.isAbsolute(filePath) ? filePath : path.join(descriptor.composeRoot, filePath));
  for (const environmentFile of [descriptor.observabilityEnvFile, descriptor.productionEnvFile]) args.push("--env-file", environmentFile);
  args.push("--project-directory", descriptor.composeRoot, "--project-name", descriptor.project);
  if (overridePath) args.push("-f", overridePath);
  args.push(...commandArguments);
  return args;
}

function defaultProcessRunner(bin, args, options) {
  return spawnSync(bin, args, {
    encoding: "utf8",
    timeout: 900_000,
    maxBuffer: 64 * 1024 * 1024,
    stdio: ["ignore", "pipe", "pipe"],
    ...options,
  });
}

function processResultOutput(result) {
  if (typeof result === "string") return result;
  if (!result || result.status !== 0 || result.error) fail("Docker Compose command failed", "compose_failed");
  return result.stdout ?? "";
}

function withOverrideFile(resolved, callback) {
  const directory = mkdtempSync(path.join(os.tmpdir(), "harden-llm-production-config-"));
  const overridePath = path.join(directory, "service-overrides.yml");
  try {
    writeFileSync(overridePath, buildServiceOverrideYaml(resolved.serviceEnvironmentOverrides), { mode: 0o600 });
    chmodSync(overridePath, 0o600);
    return callback(overridePath);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

export function decodeComposeSerialization(value) {
  if (typeof value === "string") return value.replaceAll("$$", "$");
  if (Array.isArray(value)) return value.map(decodeComposeSerialization);
  if (isObject(value)) return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, decodeComposeSerialization(item)]));
  return value;
}

export function renderComposeConfiguration(resolved, run = defaultProcessRunner, ambient = process.env) {
  return withOverrideFile(resolved, (overridePath) => {
    const args = buildComposeArguments(resolved.descriptor, ["config", "--format", "json"], overridePath);
    const output = processResultOutput(run("docker", args, { env: buildComposeEnvironment(resolved, ambient) }));
    try {
      return decodeComposeSerialization(JSON.parse(output));
    } catch {
      fail("Docker Compose returned invalid configuration JSON", "compose_failed");
    }
  });
}

function environmentMap(environment) {
  if (Array.isArray(environment)) {
    return Object.fromEntries(environment.map((entry) => {
      const index = entry.indexOf("=");
      return index < 0 ? [entry, ""] : [entry.slice(0, index), entry.slice(index + 1)];
    }));
  }
  if (!isObject(environment)) return {};
  return Object.fromEntries(Object.entries(environment).filter(([, value]) => value !== null).map(([key, value]) => [key, String(value)]));
}

function normalizeCommand(command) {
  if (command === null || command === undefined) return null;
  return Array.isArray(command) ? command.map(String) : [String(command)];
}

function normalizeDuration(value) {
  if (typeof value === "number") return value;
  if (typeof value !== "string") return value ?? 0;
  const match = value.trim().match(/^([0-9]+(?:\.[0-9]+)?)(ns|us|µs|ms|s|m|h)$/);
  if (!match) return value;
  const multipliers = { ns: 1, us: 1_000, "µs": 1_000, ms: 1_000_000, s: 1_000_000_000, m: 60_000_000_000, h: 3_600_000_000_000 };
  return Math.round(Number(match[1]) * multipliers[match[2]]);
}

function normalizeDesiredMount(mount) {
  if (typeof mount === "string") {
    const [source, target, mode] = mount.split(":");
    return { type: source?.startsWith("/") ? "bind" : "volume", source, target, readOnly: mode?.includes("ro") ?? false };
  }
  if (!isObject(mount)) return null;
  return {
    type: mount.type ?? (String(mount.source ?? "").startsWith("/") ? "bind" : "volume"),
    source: mount.source,
    target: mount.target,
    readOnly: Boolean(mount.read_only ?? mount.readOnly),
  };
}

function normalizeVolumeName(source, project) {
  if (typeof source !== "string") return source;
  const prefix = `${project}_`;
  return source.startsWith(prefix) ? source.slice(prefix.length) : source;
}

function normalizeActualMount(mount, project) {
  return {
    type: mount.Type?.toLowerCase(),
    source: mount.Type?.toLowerCase() === "volume" ? normalizeVolumeName(mount.Name, project) : mount.Source,
    target: mount.Destination,
    readOnly: mount.RW === false,
  };
}

function sortedMounts(mounts) {
  return mounts.filter(Boolean).map((mount) => ({ ...mount, source: mount.type === "volume" ? mount.source : path.normalize(mount.source) })).sort((left, right) => `${left.target}:${left.type}:${left.source}`.localeCompare(`${right.target}:${right.type}:${right.source}`));
}

function desiredMounts(service, project) {
  return sortedMounts((service.volumes ?? []).map(normalizeDesiredMount).map((mount) => mount && ({ ...mount, source: mount.type === "volume" ? normalizeVolumeName(mount.source, project) : path.normalize(mount.source) })));
}

function actualMounts(container, project) {
  return sortedMounts((container.Mounts ?? []).map((mount) => normalizeActualMount(mount, project)));
}

function equalJSON(left, right) {
  return JSON.stringify(left) === JSON.stringify(right);
}

function desiredPorts(ports) {
  return (ports ?? []).map((port) => ({
    hostIp: port.host_ip ?? "",
    published: String(port.published ?? ""),
    target: Number(port.target),
    protocol: port.protocol ?? "tcp",
  })).sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
}

function actualPorts(container) {
  const bindings = container.HostConfig?.PortBindings ?? {};
  const ports = [];
  for (const [key, values] of Object.entries(bindings)) {
    const [target, protocol = "tcp"] = key.split("/");
    for (const value of values ?? []) ports.push({ hostIp: value.HostIp ?? "", published: String(value.HostPort ?? ""), target: Number(target), protocol });
  }
  return ports.sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
}

function desiredNetworks(networks) {
  return Object.keys(networks ?? {}).sort();
}

function actualNetworks(container) {
  return Object.keys(container.NetworkSettings?.Networks ?? {}).map((name) => name.replace(/^harden-llm_/, "")).sort();
}

function desiredHealthcheck(healthcheck) {
  if (!healthcheck) return null;
  return {
    test: healthcheck.test ?? [],
    interval: normalizeDuration(healthcheck.interval),
    timeout: normalizeDuration(healthcheck.timeout),
    retries: healthcheck.retries ?? 0,
    start_period: normalizeDuration(healthcheck.start_period),
  };
}

function actualHealthcheck(container) {
  const healthcheck = container.Config?.Healthcheck;
  if (!healthcheck) return null;
  return {
    test: healthcheck.Test ?? [],
    interval: normalizeDuration(healthcheck.Interval),
    timeout: normalizeDuration(healthcheck.Timeout),
    retries: healthcheck.Retries ?? 0,
    start_period: normalizeDuration(healthcheck.StartPeriod),
  };
}

function differenceKind(field) {
  const kind = field.split(".")[1] ?? field;
  return kind === "image-reference" ? "image" : kind;
}

function differenceLabel(field) {
  return field;
}

function digestPath(filePath) {
  const hash = createHash("sha256");
  const visit = (current, relative) => {
    const metadata = lstatSync(current);
    if (metadata.isSymbolicLink()) {
      hash.update(`link:${relative}:${realpathSync(current)}\n`);
      return;
    }
    if (metadata.isDirectory()) {
      hash.update(`directory:${relative}\n`);
      for (const entry of readdirSync(current).sort()) visit(path.join(current, entry), path.join(relative, entry));
      return;
    }
    hash.update(`file:${relative}:${metadata.mode & 0o777}:${metadata.size}\n`);
    hash.update(readFileSync(current));
  };
  visit(filePath, ".");
  return hash.digest("hex");
}

function compareMountContents(desired, actual, shouldCompare) {
  if (!shouldCompare) return true;
  const desiredBinds = new Map(desired.filter(({ type }) => type === "bind").map((mount) => [mount.target, mount.source]));
  const actualBinds = new Map(actual.filter(({ type }) => type === "bind").map((mount) => [mount.target, mount.source]));
  for (const [target, desiredPath] of desiredBinds) {
    const actualPath = actualBinds.get(target);
    if (!actualPath || !existsSync(desiredPath) || !existsSync(actualPath)) return false;
    if (digestPath(desiredPath) !== digestPath(actualPath)) return false;
  }
  return true;
}

export function compareServiceConfiguration(serviceName, desiredService, actualContainer, identity, project = "harden-llm") {
  const differences = [];
  const desiredEnvironment = environmentMap(desiredService.environment);
  const actualEnvironment = environmentMap(actualContainer.Config?.Env);
  const ignored = new Set(identity.ignoredEnvironmentKeys ?? []);
  // Image-provided defaults such as PATH, HOME and SSL_CERT_FILE are not part
  // of the Compose environment contract. Compare declared keys, while still
  // treating a missing declared key as drift.
  for (const key of Object.keys(desiredEnvironment).sort()) {
    if (!ignored.has(key) && desiredEnvironment[key] !== actualEnvironment[key]) differences.push(`${serviceName}.environment.${key}`);
  }
  const identityEnvironment = identity.identityEnvironment ?? {};
  for (const key of Object.keys(identityEnvironment).sort()) {
    if (actualEnvironment[key] !== identityEnvironment[key]) differences.push(`${serviceName}.identity.${key}`);
  }

  if (actualContainer.Image !== identity.expectedImage) differences.push(`${serviceName}.image`);
  if (desiredService.command !== null && desiredService.command !== undefined && !equalJSON(normalizeCommand(desiredService.command), normalizeCommand(actualContainer.Config?.Cmd))) differences.push(`${serviceName}.command`);
  if (desiredService.entrypoint !== null && desiredService.entrypoint !== undefined && !equalJSON(normalizeCommand(desiredService.entrypoint), normalizeCommand(actualContainer.Config?.Entrypoint))) differences.push(`${serviceName}.entrypoint`);
  const desiredMountList = desiredMounts(desiredService, project);
  const actualMountList = actualMounts(actualContainer, project);
  if (!equalJSON(desiredMountList, actualMountList)) differences.push(`${serviceName}.mounts`);
  else if (!compareMountContents(desiredMountList, actualMountList, identity.compareMountContents === true)) differences.push(`${serviceName}.mount-contents`);
  if (!equalJSON(desiredNetworks(desiredService.networks), actualNetworks(actualContainer))) differences.push(`${serviceName}.networks`);
  if (!equalJSON(desiredPorts(desiredService.ports), actualPorts(actualContainer))) differences.push(`${serviceName}.ports`);
  if (!equalJSON(desiredHealthcheck(desiredService.healthcheck), actualHealthcheck(actualContainer))) differences.push(`${serviceName}.healthcheck`);
  const desiredRestart = desiredService.restart ?? "no";
  const actualRestart = actualContainer.HostConfig?.RestartPolicy?.Name ?? "no";
  if (desiredRestart !== actualRestart) differences.push(`${serviceName}.restart`);
  return differences;
}

export function compareResolvedConfiguration(model, runtime, descriptor, services = descriptorServiceNames(descriptor), imageResults = {}) {
  if (!isObject(model?.services)) fail("resolved Compose model has no services", "compose_failed");
  const differences = [];
  const byName = new Map(runtime.map(({ service, container }) => [service, container]));
  for (const serviceName of services) {
    const identity = descriptor.services[serviceName];
    if (!identity) fail(`service ${serviceName} is not in the descriptor`);
    const actualContainer = byName.get(serviceName);
    if (!actualContainer) {
      differences.push(`${serviceName}.runtime.missing`);
      continue;
    }
    const desiredService = model.services[serviceName];
    if (!desiredService) fail(`service ${serviceName} is not in the Compose model`);
    if (Object.hasOwn(imageResults, serviceName) && imageResults[serviceName] !== descriptor.services[serviceName].expectedImage) {
      differences.push(`${serviceName}.image-reference`);
    }
    differences.push(...compareServiceConfiguration(serviceName, desiredService, actualContainer, identity, descriptor.project));
  }
  return differences.sort();
}

function inspectContainer(resolved, serviceName, run = defaultProcessRunner) {
  const identity = resolved.descriptor.services[serviceName];
  if (!identity) fail(`service ${serviceName} is not in the descriptor`);
  const args = ["--context", resolved.descriptor.dockerContext, "inspect", identity.container];
  let output;
  try {
    output = processResultOutput(run("docker", args, { env: buildComposeEnvironment(resolved) }));
  } catch {
    fail(`unable to inspect service ${serviceName}`, "runtime_unavailable");
  }
  let inspected;
  try {
    inspected = JSON.parse(output);
  } catch {
    fail(`service ${serviceName} inspection was invalid`, "runtime_unavailable");
  }
  if (!Array.isArray(inspected) || inspected.length !== 1) fail(`service ${serviceName} inspection was incomplete`, "runtime_unavailable");
  const container = inspected[0];
  const labels = container.Config?.Labels ?? {};
  if (labels["com.docker.compose.project"] !== resolved.descriptor.project || labels["com.docker.compose.service"] !== serviceName) {
    fail(`service ${serviceName} belongs to an unexpected Compose project`, "runtime_identity");
  }
  return container;
}

export function inspectRuntime(resolved, services = descriptorServiceNames(resolved.descriptor), run = defaultProcessRunner) {
  return services.map((service) => ({ service, container: inspectContainer(resolved, service, run) }));
}

function inspectDesiredImages(resolved, services, run = defaultProcessRunner) {
  const results = {};
  for (const service of services) {
    const identity = resolved.descriptor.services[service];
    const desiredImage = resolved.model.services[service].image;
    const args = ["--context", resolved.descriptor.dockerContext, "image", "inspect", "--format", "{{.Id}}", desiredImage];
    let result;
    try {
      result = processResultOutput(run("docker", args, { env: buildComposeEnvironment(resolved) })).trim();
    } catch {
      results[service] = null;
      continue;
    }
    results[service] = result || null;
    if (result !== identity.expectedImage) results[service] = result || "mismatch";
  }
  return results;
}

function inspectDesiredImageMetadata(resolved, services, run = defaultProcessRunner) {
  const results = {};
  for (const service of services) {
    const desiredImage = resolved.model.services[service].image;
    const args = ["--context", resolved.descriptor.dockerContext, "image", "inspect", "--format", "{{.Id}}|{{index .Config.Labels \"org.opencontainers.image.version\"}}", desiredImage];
    let output;
    try {
      output = processResultOutput(run("docker", args, { env: buildComposeEnvironment(resolved) })).trim();
    } catch {
      results[service] = null;
      continue;
    }
    const [imageId, version] = output.split("\n", 1)[0].split("|");
    results[service] = { imageId: imageId || null, version: version && version !== "<no value>" ? version : null };
  }
  return results;
}

function candidateReleaseEnvironment(values) {
  return Object.entries(values ?? {}).filter(([key]) => RELEASE_ENVIRONMENT_KEY.test(key));
}

function candidateDifferences(resolved, runtime, imageResults, imageMetadata, services, expectedRelease) {
  const differences = [];
  const desiredByName = new Map(resolved.model.services ? Object.entries(resolved.model.services) : []);
  const runtimeByName = new Map(runtime.map(({ service, container }) => [service, container]));
  for (const service of services) {
    const desiredService = desiredByName.get(service);
    const identity = resolved.descriptor.services[service];
    const actual = runtimeByName.get(service);
    const desiredReleases = [
      ...candidateReleaseEnvironment(desiredService?.environment),
      ...candidateReleaseEnvironment(identity?.identityEnvironment),
      ...candidateReleaseEnvironment(resolved.descriptor.serviceEnvironmentOverrides?.[service]),
    ];
    if (desiredReleases.length === 0) {
      differences.push(`${service}.candidate.release-environment`);
    } else {
      for (const [key, value] of desiredReleases) {
        if (value !== expectedRelease) differences.push(`${service}.candidate.release-environment.${key}`);
      }
    }

    const desiredImage = imageMetadata[service];
    if (!desiredImage || desiredImage.imageId !== identity.expectedImage) {
      differences.push(`${service}.candidate.image-unavailable`);
    }
    if (!desiredImage || desiredImage.version !== expectedRelease) {
      differences.push(`${service}.candidate.image-version`);
    }

    if (!actual) {
      differences.push(`${service}.candidate.runtime-missing`);
      continue;
    }
    const actualLabels = actual.Config?.Labels ?? {};
    if (actualLabels["org.opencontainers.image.version"] !== expectedRelease) {
      differences.push(`${service}.candidate.runtime-label`);
    }
    const actualReleases = candidateReleaseEnvironment(environmentMap(actual.Config?.Env));
    if (actualReleases.length === 0 || actualReleases.some(([, value]) => value !== expectedRelease)) {
      differences.push(`${service}.candidate.runtime-release`);
    }
    if (actual.State?.Status !== "running") differences.push(`${service}.candidate.runtime-state`);
    if (actual.State?.Health?.Status !== "healthy") differences.push(`${service}.candidate.runtime-health`);
    if (imageResults[service] !== identity.expectedImage) differences.push(`${service}.candidate.runtime-image`);
  }
  return differences.sort();
}

function candidateDesiredDifferences(differences) {
  return differences.filter((difference) => difference.includes("candidate.release-environment") || difference.includes("candidate.image-unavailable") || difference.includes("candidate.image-version"));
}

function candidateRuntimeDifferences(differences) {
  return differences.filter((difference) => difference.includes("candidate.runtime-"));
}

function verifyDescriptorImages(resolved, services, run = defaultProcessRunner) {
  const results = inspectDesiredImages(resolved, services, run);
  for (const service of services) {
    if (results[service] !== resolved.descriptor.services[service].expectedImage) {
      fail(`desired image for ${service} is unavailable or does not match the descriptor`, "image_mismatch");
    }
  }
}

function ensureFreshResolution(first, second) {
  if (!equalJSON(first.sourceDigests, second.sourceDigests)) fail("approved sources changed during the operation", "stale_resolution");
}

export function summarizeComparison(differences) {
  return {
    equivalent: differences.length === 0,
    differences: differences.map(differenceLabel),
  };
}

function printReport({ operation, resolved, differences, runtimeVerified, applied = false, services: selectedServices }) {
  const services = selectedServices ?? descriptorServiceNames(resolved.descriptor);
  console.log(`production-config ${operation}: ${differences.length === 0 ? "equivalent" : "differences found"}`);
  console.log(`project: ${resolved.descriptor.project} (${resolved.descriptor.dockerContext}); scoped services: ${services.length}`);
  for (const source of resolved.sources) console.log(`source: ${source.path}`);
  for (const conflict of resolved.conflicts) console.log(`source precedence: ${conflict.key} (${conflict.kind})`);
  console.log(`runtime: ${runtimeVerified ? "verified" : "unverified"}; applied: ${applied ? "yes" : "no"}`);
  if (differences.length > 0) {
    for (const field of [...new Set(differences)].sort()) console.log(`difference: ${differenceLabel(field)}`);
  }
  if (differences.length === 0 && runtimeVerified && !applied) console.log("action: no service recreation required");
  if (differences.length === 0 && !runtimeVerified) console.log("action: resolution only; runtime was not inspected");
}

function parseArguments(argv) {
  const operation = argv[0] && !argv[0].startsWith("-") ? argv.shift() : "check";
  const options = { operation, descriptorPath: DEFAULT_DESCRIPTOR_PATH, services: null, resolveOnly: false, expectedRelease: null };
  while (argv.length > 0) {
    const argument = argv.shift();
    if (argument === "--descriptor") {
      options.descriptorPath = argv.shift();
      continue;
    }
    if (argument === "--services") {
      options.services = (argv.shift() ?? "").split(",").filter(Boolean);
      continue;
    }
    if (argument === "--resolve-only") {
      options.resolveOnly = true;
      continue;
    }
    if (argument === "--expected-release") {
      options.expectedRelease = normalizeExpectedRelease(argv.shift());
      continue;
    }
    fail(`unknown argument ${argument}`, "invalid_arguments");
  }
  if (!["check", "apply"].includes(operation)) fail("operation must be check or apply", "invalid_arguments");
  if (operation === "apply" && (!options.services || options.services.length === 0)) fail("apply requires --services", "invalid_arguments");
  if (options.expectedRelease && options.resolveOnly) fail("--expected-release cannot be combined with --resolve-only", "invalid_arguments");
  return options;
}

export function runCheck(descriptor, { services = descriptorServiceNames(descriptor), resolveOnly = false, expectedRelease = null, run = defaultProcessRunner } = {}) {
  services = normalizeSelectedServices(descriptor, services);
  expectedRelease = normalizeExpectedRelease(expectedRelease);
  validateCandidateScope(services, expectedRelease, resolveOnly);
  const resolved = resolveConfiguration(descriptor);
  resolved.model = renderComposeConfiguration(resolved, run);
  if (resolveOnly) return { resolved, differences: [], runtimeVerified: false, services };
  const runtime = inspectRuntime(resolved, services, run);
  const imageResults = inspectDesiredImages(resolved, services, run);
  const baseDifferences = compareResolvedConfiguration(resolved.model, runtime, descriptor, services, imageResults);
  const imageMetadata = expectedRelease ? inspectDesiredImageMetadata(resolved, services, run) : {};
  const candidate = expectedRelease
    ? candidateDifferences(resolved, runtime, imageResults, imageMetadata, services, expectedRelease)
    : [];
  const differences = [...new Set([...baseDifferences, ...candidate])].sort();
  return {
    resolved,
    runtime,
    differences,
    candidate: expectedRelease ? { expectedRelease, desiredDifferences: candidateDesiredDifferences(candidate), runtimeDifferences: candidateRuntimeDifferences(candidate) } : null,
    runtimeVerified: true,
    services,
  };
}

export function runApply(descriptor, services, run = defaultProcessRunner, { expectedRelease = null } = {}) {
  services = normalizeSelectedServices(descriptor, services);
  expectedRelease = normalizeExpectedRelease(expectedRelease);
  validateCandidateScope(services, expectedRelease, false);
  for (const service of services) {
    if (!descriptor.services[service].manageable) fail(`service ${service} is not approved for application`, "scope_blocked");
  }
  const first = runCheck(descriptor, { services, expectedRelease, run });
  if (first.candidate?.desiredDifferences.length > 0) {
    fail("desired application images or release metadata do not match the expected release", "candidate_mismatch");
  }
  if (first.differences.length === 0) return { ...first, applied: false };
  for (const field of first.differences) {
    if (field.includes(".candidate.runtime-")) continue;
    const service = field.split(".")[0];
    const kind = differenceKind(field);
    if (!(descriptor.services[service].allowedDifferenceFields ?? ["environment"]).includes(kind)) {
      fail(`unapproved difference blocks ${service}`, "scope_blocked");
    }
  }
  verifyDescriptorImages(first.resolved, services, run);
  const second = runCheck(descriptor, { services, expectedRelease, run });
  ensureFreshResolution(first.resolved, second.resolved);
  if (!equalJSON(first.differences, second.differences)) fail("runtime changed during application review", "stale_resolution");
  if (second.candidate?.desiredDifferences.length > 0) {
    fail("desired application images or release metadata do not match the expected release", "candidate_mismatch");
  }
  withOverrideFile(second.resolved, (overridePath) => {
    const upArgs = buildComposeArguments(descriptor, ["up", "-d", "--no-build", "--no-deps", "--pull", "never", "--wait", "--wait-timeout", "300", ...services], overridePath);
    processResultOutput(run("docker", upArgs, { env: buildComposeEnvironment(second.resolved) }));
  });
  const final = runCheck(descriptor, { services, expectedRelease, run });
  if (final.differences.length > 0) fail("selected services did not converge after application", "post_apply_mismatch");
  return { ...final, applied: true };
}

async function main() {
  try {
    const options = parseArguments(process.argv.slice(2));
    const descriptor = loadDescriptor(options.descriptorPath);
    const result = options.operation === "apply"
      ? runApply(descriptor, options.services, defaultProcessRunner, { expectedRelease: options.expectedRelease })
      : runCheck(descriptor, { services: options.services ?? descriptorServiceNames(descriptor), resolveOnly: options.resolveOnly, expectedRelease: options.expectedRelease });
    printReport({ operation: options.operation, ...result });
    if (result.differences?.length > 0 && options.operation === "check") process.exitCode = 2;
  } catch (error) {
    const message = error instanceof ProductionConfigError ? error.message : "production configuration check failed";
    console.error(`production-config: ${message}`);
    process.exitCode = 1;
  }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) await main();
