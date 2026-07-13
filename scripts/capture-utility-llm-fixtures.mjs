#!/usr/bin/env node

import { createHash } from "node:crypto";
import { createRequire } from "node:module";
import { mkdir, readFile, readdir, rm, writeFile } from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

const SCRIPT_VERSION = 1;
const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const outputRoot = path.join(repositoryRoot, "fixtures", "parity");

function option(name, fallback = "") {
  const index = process.argv.indexOf(`--${name}`);
  return index >= 0 ? process.argv[index + 1] : fallback;
}

const sourceRoot = path.resolve(option("source", "/home/kirill/p/utility-llm"));
const sourceSHA = option("source-sha");
if (!/^[0-9a-f]{40}$/.test(sourceSHA)) {
  throw new Error("--source-sha must be a full lowercase Git commit");
}

const requireFromSource = createRequire(path.join(sourceRoot, "package.json"));
const packageJSON = requireFromSource(path.join(sourceRoot, "package.json"));
const usage = requireFromSource(path.join(sourceRoot, "src", "usage.js"));
const retry = requireFromSource(path.join(sourceRoot, "src", "retry-classifier.js"));
const schema = requireFromSource(path.join(sourceRoot, "src", "schema-normalizer.js"));
const cache = requireFromSource(path.join(sourceRoot, "src", "operation-cache", "index.js"));
const profiles = requireFromSource(path.join(sourceRoot, "src", "model-profiles.js"));

function canonical(value) {
  if (Array.isArray(value)) return value.map(canonical);
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonical(value[key])]));
  }
  return value;
}

function canonicalJSON(value) {
  return `${JSON.stringify(canonical(value), null, 2)}\n`;
}

function sha256(bytes) {
  return createHash("sha256").update(bytes).digest("hex");
}

const captured = [];

async function captureJSON(relativePath, fixtureClass, value, sourcePaths = []) {
  const bytes = Buffer.from(canonicalJSON(value));
  const absolute = path.join(outputRoot, relativePath);
  await mkdir(path.dirname(absolute), { recursive: true });
  await writeFile(absolute, bytes, { mode: 0o644 });
  captured.push({
    path: relativePath.split(path.sep).join("/"),
    class: fixtureClass,
    schemaVersion: 1,
    sourcePaths,
    sha256: sha256(bytes),
  });
}

async function copySourceJSON(relativeSourcePath) {
  const sourceBytes = await readFile(path.join(sourceRoot, relativeSourcePath));
  const parsed = JSON.parse(sourceBytes.toString("utf8"));
  const fixtureClass = relativeSourcePath.split(path.sep)[1];
  const targetPath = path.join("source", ...relativeSourcePath.split(path.sep).slice(1));
  await captureJSON(targetPath, fixtureClass, parsed, [relativeSourcePath]);
  captured.at(-1).sourceSHA256 = sha256(sourceBytes);
}

async function sourceJSONFiles(directory) {
  const result = [];
  const absolute = path.join(sourceRoot, directory);
  for (const entry of await readdir(absolute, { withFileTypes: true })) {
    const relative = path.join(directory, entry.name);
    if (entry.isDirectory()) result.push(...await sourceJSONFiles(relative));
    if (entry.isFile() && entry.name.endsWith(".json")) result.push(relative);
  }
  return result.sort();
}

await rm(outputRoot, { recursive: true, force: true });
await mkdir(outputRoot, { recursive: true });

await captureJSON("generated/source-contract.json", "contract", {
  sourceGitSHA: sourceSHA,
  packageName: packageJSON.name,
  packageVersion: packageJSON.version,
  profileSchemaVersion: profiles.PROFILE_SCHEMA_VERSION,
  apiInferenceTypes: profiles.API_INFERENCE_TYPES,
  endpointCredentialScopes: profiles.ENDPOINT_CREDENTIAL_SCOPES,
  defaultDiscoveryTimeoutMs: profiles.DEFAULT_DISCOVERY_TIMEOUT_MS,
  defaultRunTimeoutMs: profiles.DEFAULT_RUN_TIMEOUT_MS,
  operationSchemaVersion: cache.OPERATION_SCHEMA_VERSION,
  operationCacheRecordSchemaVersion: cache.OPERATION_CACHE_RECORD_SCHEMA_VERSION,
  defaultOperationCacheVersion: cache.DEFAULT_OPERATION_CACHE_VERSION,
}, ["package.json", "src/model-profiles.js", "src/operation-cache/index.js"]);

const usageInput = usage.usageFromCounts({
  input: 100,
  cacheReadInput: 25,
  cacheCreationInput: 10,
  output: 20,
  reasoningOutput: 5,
}, {
  input_cost_per_token: 0.000001,
  cache_read_input_token_cost: 0.0000001,
  cache_creation_input_token_cost: 0.00000125,
  output_cost_per_token: 0.000002,
  output_cost_per_reasoning_token: 0.000003,
});
await captureJSON("generated/usage-cases.json", "usage", {
  cases: [
    { name: "zero", normalized: usage.normalizeUsage(null), summary: usage.summarizeUsage(null) },
    { name: "priced", normalized: usage.normalizeUsage(usageInput), summary: usage.summarizeUsage(usageInput) },
  ],
}, ["src/usage.js", "src/pricing-fields.js"]);

const retryCases = [
  ["network", Object.assign(new Error("socket hang up"), { code: "ECONNRESET" }), {}],
  ["rate-limit", Object.assign(new Error("rate limited"), { status: 429, headers: { "retry-after": "2" } }), {}],
  ["server", Object.assign(new Error("unavailable"), { status: 503 }), {}],
  ["refusal", Object.assign(new Error("content_filter refusal"), { status: 503 }), {}],
  ["timeout", retry.createTimeoutAbortError("deadline"), {}],
  ["parse-disabled", Object.assign(new Error("invalid JSON"), { parseDiagnostics: { category: "parse_error" } }), {}],
  ["parse-enabled", Object.assign(new Error("invalid JSON"), { parseDiagnostics: { category: "parse_error" } }), { parseError: true }],
];
await captureJSON("generated/retry-classification.json", "retry", {
  cases: retryCases.map(([name, error, policy]) => ({
    name,
    policy,
    classification: retry.classifyRetryability(error, policy),
    fallbackEligible: retry.isFallbackEligible(error, policy),
  })),
}, ["src/retry-classifier.js", "src/response-parser.js"]);

const schemaCases = [
  { name: "shorthand", input: { answer: "string", confidence: "number" } },
  { name: "json-schema", input: { type: "object", properties: { answer: { type: "string" } }, required: ["answer"], additionalProperties: false } },
  { name: "array-shorthand", input: { values: ["integer"] } },
];
await captureJSON("generated/schema-normalization.json", "schema", {
  cases: schemaCases.map(({ name, input }) => ({
    name,
    input,
    isJSONSchema: schema.isStructuredOutputJsonSchema(input),
    normalized: schema.normalizeStructuredOutputSchema(input),
  })),
}, ["src/schema-normalizer.js"]);

const operation = {
  schemaVersion: cache.OPERATION_SCHEMA_VERSION,
  protocol: "openai.responses",
  endpoint: { identity: "https://api.openai.com:443", method: "post", path: "/v1/responses" },
  model: "gpt-test",
  payload: { input: [{ role: "user", content: "deterministic fixture" }], model: "gpt-test" },
  semanticHeaders: { "openai-beta": "responses=v1" },
  responseProjection: { provider: "openai", kind: "responses", version: "v1" },
};
await captureJSON("generated/cache-identity.json", "cache", {
  operation: cache.normalizeOperation(operation),
  cacheVersion: cache.DEFAULT_OPERATION_CACHE_VERSION,
  stablePayload: cache.stableStringify({ operation: cache.normalizeOperation(operation), cacheVersion: cache.DEFAULT_OPERATION_CACHE_VERSION }),
  operationHash: cache.buildOperationHash({ operation }),
  modes: [undefined, "off", "cache", "refresh"].map((input) => ({ input: input ?? null, output: cache.resolveOperationCacheMode(input) })),
}, ["src/operation-cache/index.js"]);

const baseProfile = {
  schemaVersion: 1,
  provider: "openai",
  apiInferenceType: "responses",
  endpointCredentialScope: "global",
  baseUrl: "https://api.openai.com/v1",
  pricing: {
    input_cost_per_token: 0.000001,
    output_cost_per_token: 0.000002,
    cache_read_input_token_cost: 0,
    cache_creation_input_token_cost: null,
    output_cost_per_reasoning_token: null,
  },
  supportsTemperature: false,
  supportsContractedStructuredOutput: true,
  tokensParam: null,
  responsesTokensParam: "max_output_tokens",
  defaultOptions: { max_tokens: 512, temperature: 0 },
};
const profileInput = {
  Primary: { ...baseProfile, llmProfile: "Primary", modelId: "gpt-primary", backupProfiles: ["Backup"] },
  Backup: { ...baseProfile, llmProfile: "Backup", modelId: "gpt-backup", backupProfiles: [] },
};
const normalizedProfiles = profiles.normalizeProfileCatalogJson(profileInput, {
  now: () => "2026-05-13T05:00:00.000Z",
});
await captureJSON("generated/profile-catalog.json", "profiles", {
  input: profileInput,
  normalized: normalizedProfiles,
  serialized: profiles.serializeProfileCatalogJson(normalizedProfiles),
}, ["src/model-profiles.js", "tests/model-profiles.behavior.test.js"]);

const copiedDirectories = [
  "fixtures/diagnostics",
  "fixtures/evals",
  "fixtures/examples",
  "fixtures/llm-stats-totals",
  "fixtures/models",
  "fixtures/queries",
  "fixtures/telemetry",
  "fixtures/traces",
];
for (const directory of copiedDirectories) {
  for (const relativePath of await sourceJSONFiles(directory)) {
    await copySourceJSON(relativePath);
  }
}

captured.sort((left, right) => left.path.localeCompare(right.path));
const manifest = {
  manifestSchemaVersion: 1,
  source: {
    repository: "prls-co/utility-llm",
    gitSHA: sourceSHA,
    packageVersion: packageJSON.version,
  },
  capture: {
    script: "scripts/capture-utility-llm-fixtures.mjs",
    scriptVersion: SCRIPT_VERSION,
    nodeVersion: process.version,
    canonicalJSON: true,
    liveCredentialsUsed: false,
  },
  fixtures: captured,
};
await writeFile(path.join(outputRoot, "manifest.json"), canonicalJSON(manifest), { mode: 0o644 });
console.log(`captured ${captured.length} fixtures from ${sourceSHA}`);
