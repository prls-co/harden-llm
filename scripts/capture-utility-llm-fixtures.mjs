#!/usr/bin/env node

import { createHash } from "node:crypto";
import { execFile as execFileCallback } from "node:child_process";
import { createRequire } from "node:module";
import { mkdir, readFile, readdir, rm, writeFile } from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";

const SCRIPT_VERSION = 7;
const execFile = promisify(execFileCallback);
const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const outputRoot = path.join(repositoryRoot, "fixtures", "parity");

function option(name, fallback = "") {
  const index = process.argv.indexOf(`--${name}`);
  return index >= 0 ? process.argv[index + 1] : fallback;
}

const sourceRoot = path.resolve(option("source", "/home/kirill/utility-llm"));
const sourceSHA = option("source-sha");
if (!/^[0-9a-f]{40}$/.test(sourceSHA)) {
  throw new Error("--source-sha must be a full lowercase Git commit");
}
const { stdout: sourceHead } = await execFile("git", ["-C", sourceRoot, "rev-parse", "HEAD"]);
if (sourceHead.trim() !== sourceSHA) {
  throw new Error(`source checkout HEAD ${sourceHead.trim()} does not match --source-sha ${sourceSHA}`);
}

const requireFromSource = createRequire(path.join(sourceRoot, "package.json"));
const packageJSON = requireFromSource(path.join(sourceRoot, "package.json"));
const usage = requireFromSource(path.join(sourceRoot, "src", "usage.js"));
const retry = requireFromSource(path.join(sourceRoot, "src", "retry-classifier.js"));
const schema = requireFromSource(path.join(sourceRoot, "src", "schema-normalizer.js"));
const responseParser = requireFromSource(path.join(sourceRoot, "src", "response-parser.js"));
const reasoning = requireFromSource(path.join(sourceRoot, "src", "reasoning-effort.js"));
const cache = requireFromSource(path.join(sourceRoot, "src", "operation-cache", "index.js"));
const profiles = requireFromSource(path.join(sourceRoot, "src", "model-profiles.js"));
const openaiProvider = requireFromSource(path.join(sourceRoot, "src", "providers", "openai.js"));
const genericProvider = requireFromSource(path.join(sourceRoot, "src", "providers", "generic.js"));
const googleProvider = requireFromSource(path.join(sourceRoot, "src", "providers", "google.js"));
const anthropicProvider = requireFromSource(path.join(sourceRoot, "src", "providers", "anthropic.js"));
const { buildRawProviderEnvelope } = requireFromSource(path.join(sourceRoot, "src", "providers", "operation-helpers.js"));
const { z } = requireFromSource("zod");

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

const acceptingSchema = {
  safeParse(value) {
    return { success: true, data: value };
  },
};
const repairCases = [
  { name: "numeric-string-normalization", raw: `{"answer":"ok","count":"42"}` },
  { name: "unquoted-keys-single-quotes-trailing-comma", raw: `{answer:'ok',count:2,}` },
  { name: "missing-comma", raw: `{"answer":"ok" "count":2}` },
  { name: "markdown-fence", raw: "```json\n{answer: 'ok'}\n```" },
  { name: "truncated-object", raw: `{"answer":"ok","items":[1,2` },
];
let repairFailure;
try {
  responseParser.parseWithRepair(`{"answer":"\\uZZZZ"}`, acceptingSchema);
  repairFailure = { succeeded: true };
} catch (error) {
  repairFailure = {
    succeeded: false,
    name: error.name,
    message: error.message,
    diagnostics: error.parseDiagnostics,
  };
}
await captureJSON("generated/structured-parser-cases.json", "schema", {
  repairCases: repairCases.map(({ name, raw }) => ({
    name,
    raw,
    parsed: responseParser.parseWithRepair(raw, acceptingSchema),
  })),
  repairFailure,
  geminiCases: [
    { name: "markdown-fence", raw: "```json\n{\"answer\":\"ok\"}\n```" },
    { name: "prose-extraction", raw: "Result follows: {\"answer\":\"ok\"} done." },
    { name: "literal-newline", raw: "{\"answer\":\"first\nsecond\"}" },
  ].map(({ name, raw }) => ({ name, raw, parsed: responseParser.tryParseGeminiJson(raw) })),
}, ["src/response-parser.js", "node_modules/jsonrepair/package.json"]);

function captureReasoningFailure(reasoningEffort, providerOptions) {
  try {
    reasoning.resolveReasoningEffortOptions({
      modelConfig: { reasoningEffortMap: { highest: { reasoning: { effort: "high" } } } },
      modelId: "fixture-model",
      reasoningEffort,
      providerOptions,
    });
    return { succeeded: true };
  } catch (error) {
    return { succeeded: false, name: error.name, code: error.code, message: error.message };
  }
}
await captureJSON("generated/reasoning-effort-cases.json", "providers", {
  contractedEfforts: reasoning.CONTRACTED_REASONING_EFFORTS,
  mapped: reasoning.resolveReasoningEffortOptions({
    modelConfig: { reasoningEffortMap: { highest: { reasoning: { effort: "high" }, budget: 9 } } },
    modelId: "fixture-model",
    reasoningEffort: "highest",
    providerOptions: { budget: 1, providerOption: true },
  }),
  nativeWithoutPortableEffort: captureReasoningFailure("", { thinking_budget: 10 }),
  unsupportedAlias: captureReasoningFailure("high", {}),
}, ["src/reasoning-effort.js"]);

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

const providerRequest = {
  systemPrompt: "Be exact.",
  userPrompt: "Answer the deterministic fixture.",
  additionalOptions: { max_tokens: 42, timeout: 5000, maxRetries: 9 },
  modelId: "fixture-model",
};
const structuredSchemaJSON = {
  type: "object",
  properties: {
    answer: { type: "string" },
    count: { type: "number" },
  },
  required: ["answer", "count"],
  additionalProperties: false,
};
const structuredSchema = z.object({ answer: z.string(), count: z.number() });
const structuredProviderRequest = {
  ...providerRequest,
  schema: structuredSchema,
  schemaJson: structuredSchemaJSON,
  enableRepair: true,
};
const providerCases = [
  {
    name: "openai-responses",
    provider: "openai",
    plan: openaiProvider.prepareTextOperation(
      { baseURL: "https://api.openai.com/v1", responses: { create: async () => ({}) } },
      { apiModelName: "gpt-5.4", provider: "openai", supportsTemperature: false, responsesTokensParam: "max_output_tokens", defaultOptions: { reasoning: { effort: "high" } } },
      providerRequest,
    ),
    structuredPlan: openaiProvider.prepareStructuredOperation(
      { baseURL: "https://api.openai.com/v1", responses: { create: async () => ({}) } },
      { apiModelName: "gpt-5.4", provider: "openai", supportsTemperature: false, responsesTokensParam: "max_output_tokens", defaultOptions: { reasoning: { effort: "high" } } },
      structuredProviderRequest,
    ),
    response: { output_text: "responses-ok", usage: { input_tokens: 10, output_tokens: 4, output_tokens_details: { reasoning_tokens: 1 } } },
    structuredResponse: { output_text: `{"answer":"ok","count":2}`, usage: { input_tokens: 10, output_tokens: 4 } },
  },
  {
    name: "openai-chat",
    provider: "openai",
    plan: openaiProvider.prepareTextOperation(
      { baseURL: "https://api.openai.com/v1", chat: { completions: { create: async () => ({}) } } },
      { apiModelName: "gpt-4.1", provider: "openai", supportsTemperature: true, tokensParam: "max_completion_tokens", defaultOptions: {} },
      { ...providerRequest, additionalOptions: { ...providerRequest.additionalOptions, useResponsesApi: false } },
    ),
    structuredPlan: openaiProvider.prepareStructuredOperation(
      { baseURL: "https://api.openai.com/v1", chat: { completions: { create: async () => ({}) } } },
      { apiModelName: "gpt-4.1", provider: "openai", supportsTemperature: true, tokensParam: "max_completion_tokens", defaultOptions: {} },
      { ...structuredProviderRequest, additionalOptions: { ...structuredProviderRequest.additionalOptions, useResponsesApi: false } },
    ),
    response: { choices: [{ finish_reason: "stop", message: { content: "chat-ok" } }], usage: { prompt_tokens: 3, completion_tokens: 2 } },
    structuredResponse: { choices: [{ finish_reason: "stop", message: { content: `{"answer":"ok","count":2}` } }], usage: { prompt_tokens: 3, completion_tokens: 2 } },
  },
  {
    name: "generic-openai-compatible",
    provider: "vendor",
    plan: genericProvider.prepareTextOperation(
      { baseURL: "https://api.vendor.example/v1", chat: { completions: { create: async () => ({}) } } },
      { apiModelName: "vendor/model", provider: "vendor", supportsTemperature: false, defaultOptions: { provider: { only: ["fast"] } } },
      { ...providerRequest, routingOptions: {} },
    ),
    structuredPlan: genericProvider.prepareStructuredOperation(
      { baseURL: "https://api.vendor.example/v1", chat: { completions: { create: async () => ({}) } } },
      { apiModelName: "vendor/model", provider: "vendor", supportsTemperature: false, defaultOptions: { provider: { only: ["fast"] } } },
      { ...structuredProviderRequest, routingOptions: {} },
    ),
    response: { choices: [{ finish_reason: "stop", message: { content: "vendor-ok" } }], usage: { prompt_tokens: 5, completion_tokens: 2 } },
    structuredResponse: { choices: [{ finish_reason: "stop", message: { content: `{"answer":"ok","count":2}` } }], usage: { prompt_tokens: 5, completion_tokens: 2 } },
  },
  {
    name: "gemini-generate-content",
    provider: "google",
    plan: googleProvider.prepareTextOperation(
      { baseURL: "https://generativelanguage.googleapis.com", chat: { completions: { create: async () => ({}) } } },
      { apiModelName: "models/gemini-2.5-flash", provider: "google", supportsTemperature: true, defaultOptions: {} },
      providerRequest,
    ),
    structuredPlan: googleProvider.prepareStructuredOperation(
      { baseURL: "https://generativelanguage.googleapis.com", chat: { completions: { create: async () => ({}) } } },
      { apiModelName: "models/gemini-2.5-flash", provider: "google", supportsTemperature: true, defaultOptions: {} },
      structuredProviderRequest,
    ),
    response: { candidates: [{ finishReason: "STOP", content: { parts: [{ text: "gemini-ok" }] } }], usageMetadata: { promptTokenCount: 5, candidatesTokenCount: 2, thoughtsTokenCount: 1 } },
    structuredResponse: { candidates: [{ finishReason: "STOP", content: { parts: [{ text: `{"answer":"ok","count":2}` }] } }], usageMetadata: { promptTokenCount: 5, candidatesTokenCount: 2 } },
  },
  {
    name: "anthropic-messages",
    provider: "anthropic",
    plan: anthropicProvider.prepareTextOperation(
      { baseURL: "https://api.anthropic.com/v1", anthropicVersion: anthropicProvider.DEFAULT_ANTHROPIC_VERSION },
      { apiModelName: "claude-sonnet-4-5", provider: "anthropic", supportsTemperature: true, defaultOptions: {} },
      providerRequest,
    ),
    structuredPlan: anthropicProvider.prepareStructuredOperation(
      { baseURL: "https://api.anthropic.com/v1", anthropicVersion: anthropicProvider.DEFAULT_ANTHROPIC_VERSION },
      { apiModelName: "claude-sonnet-4-5", provider: "anthropic", supportsTemperature: true, defaultOptions: {} },
      structuredProviderRequest,
    ),
    response: { content: [{ type: "text", text: "anthropic-ok" }], stop_reason: "end_turn", usage: { input_tokens: 5, output_tokens: 2 } },
    structuredResponse: { content: [{ type: "text", text: `{"answer":"ok","count":2}` }], stop_reason: "end_turn", usage: { input_tokens: 5, output_tokens: 2 } },
  },
];
await captureJSON("generated/provider-cases.json", "providers", {
  cases: providerCases.map(({ name, provider, plan, structuredPlan, response, structuredResponse }) => ({
    name,
    operation: plan.operation,
    normalized: plan.normalize(buildRawProviderEnvelope({ provider, protocol: plan.operation.protocol, response })),
    structuredOperation: structuredPlan.operation,
    structuredNormalized: structuredPlan.normalize(buildRawProviderEnvelope({
      provider,
      protocol: structuredPlan.operation.protocol,
      response: structuredResponse,
    })),
  })),
}, [
  "src/providers/openai.js",
  "src/providers/generic.js",
  "src/providers/google.js",
  "src/providers/anthropic.js",
  "tests/provider-operation-cache.behavior.test.js",
  "tests/provider-runtime.behavior.test.js",
]);

const copiedDirectories = [
  "fixtures/combinatorial",
  "fixtures/diagnostics",
  "fixtures/evals",
  "fixtures/examples",
  "fixtures/llm-stats-totals",
  "fixtures/models",
  "fixtures/providers",
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
const fixtureConsumers = {
  "generated/cache-identity.json": [
    { target: "internal/cachekey/cache_test.go", testFunction: "TestCacheIdentity", tests: ["TEST-011"], evidence: "cache-identity.json" },
  ],
  "generated/profile-catalog.json": [
    { target: "internal/profiles/profile_test.go", testFunction: "TestProfileParityRoundTripAndValidation", tests: ["TEST-017"], evidence: "profile-catalog.json" },
  ],
  "generated/provider-cases.json": [
    { target: "internal/providers/requests_test.go", testFunction: "TestProviderRequestParityCapturedSource", tests: ["TEST-012"], evidence: "provider-cases.json" },
    { target: "internal/providers/normalization_test.go", testFunction: "TestProviderNormalizationParityCapturedSource", tests: ["TEST-013"], evidence: "provider-cases.json" },
  ],
  "generated/reasoning-effort-cases.json": [
    { target: "internal/providers/requests_test.go", testFunction: "TestReasoningEffortParityCapturedSource", tests: ["TEST-012"], evidence: "reasoning-effort-cases.json" },
  ],
  "generated/retry-classification.json": [
    { target: "internal/retry/retry_test.go", testFunction: "TestRetryClassificationParityCapturedSource", tests: ["TEST-008"], evidence: "retry-classification.json" },
    { target: "internal/runtime/repair_backup_test.go", testFunction: "TestBackupEligibilityParityCapturedSource", tests: ["TEST-009"], evidence: "retry-classification.json" },
  ],
  "generated/schema-normalization.json": [
    { target: "internal/schema/schema_test.go", testFunction: "TestSchemaContract", tests: ["TEST-010"], evidence: "schema-normalization.json" },
  ],
  "generated/structured-parser-cases.json": [
    { target: "internal/schema/schema_test.go", testFunction: "TestStructuredParserParityCapturedSource", tests: ["TEST-010"], evidence: "structured-parser-cases.json" },
  ],
  "generated/usage-cases.json": [
    { target: "internal/pricing/usage_cost_test.go", testFunction: "TestUsageCostParity", tests: ["TEST-015"], evidence: "usage-cases.json" },
  ],
  "source/combinatorial/retry-decision-matrix.json": [
    { target: "internal/retry/retry_test.go", testFunction: "TestCurrentSourceRetryDecisionMatrixParity", tests: ["TEST-008"], evidence: "retry-decision-matrix.json" },
  ],
  "source/evals/cpa-gpt-5.4-mini-responses-call.json": [
    { target: "internal/providers/requests_test.go", testFunction: "TestCPAResponsesEvalRequestParityCapturedSource", tests: ["TEST-012"], evidence: "cpa-gpt-5.4-mini-responses-call.json" },
  ],
  "source/evals/jsonrepair-object-key-expected-incident.json": [
    { target: "internal/schema/schema_test.go", testFunction: "TestJSONRepairIncidentParityCapturedSource", tests: ["TEST-010"], evidence: "jsonrepair-object-key-expected-incident.json" },
  ],
  "source/providers/openai-responses-stream-retry-error.json": [
    { target: "internal/providers/normalization_test.go", testFunction: "TestProviderNormalizationParityClassifiesResponsesProviderRetryDirective", tests: ["TEST-013"], evidence: "openai-responses-stream-retry-error.json" },
  ],
};
for (const fixture of captured) {
  if (fixture.path.startsWith("source/llm-stats-totals/parity/")) {
    fixtureConsumers[fixture.path] = [
      { target: "internal/stats/parity_test.go", testFunction: "TestParityStatsTotals", tests: ["TEST-016"], evidence: "llm-stats-totals/parity/*.json" },
    ];
  }
}
const manifest = {
  manifestSchemaVersion: 2,
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
  fixtureConsumers,
  intentionalDifferences: [
    {
      id: "detailed-go-result",
      mode: "intentional-difference",
      tests: ["TEST-006"],
      fixtures: [
        "source/examples/basic-text-golden.json",
        "source/examples/basic-text-input.json",
        "source/examples/structured-golden.json",
        "source/examples/structured-input.json",
      ],
      adr: "ADR-HLLM-001",
      note: "Source direct output is compared to Result.Output; Go metadata derives from the same normalized call record.",
    },
    {
      id: "typed-root-surface",
      mode: "intentional-difference",
      tests: ["TEST-002"],
      fixtures: ["generated/source-contract.json"],
      adr: "ADR-HLLM-002",
      note: "The target exposes one typed Call path instead of the JavaScript export inventory.",
    },
    {
      id: "secure-provider-egress",
      mode: "intentional-difference",
      tests: ["TEST-012", "TEST-014", "TEST-017"],
      fixtures: ["generated/profile-catalog.json", "generated/provider-cases.json"],
      adr: "ADR-HLLM-001",
      note: "The Go deployment keeps provider normalization semantics while enforcing server-side egress policy and configured provider ownership.",
    },
    {
      id: "authenticated-credential-envelope",
      mode: "intentional-difference",
      tests: ["TEST-018"],
      fixtures: [],
      adr: "ADR-HLLM-001",
      note: "The Go service uses an authenticated versioned credential envelope instead of the source runtime's credential storage boundary.",
    },
    {
      id: "garage-artifact-projection",
      mode: "intentional-difference",
      tests: ["TEST-016", "TEST-019", "TEST-040"],
      fixtures: [
        "source/diagnostics/bundle-input.json",
        "source/diagnostics/summary-input.json",
        "source/examples/cache-golden.json",
        "source/traces/raw-response-input.json",
        "source/traces/trace-doc-input.json",
      ],
      adr: "ADR-HLLM-001",
      note: "The self-hosted service projects diagnostic and trace artifacts into Garage/Postgres storage while preserving the source diagnostic fields.",
    },
    {
      id: "provider-retry-directive",
      mode: "intentional-difference",
      tests: ["TEST-008", "TEST-012", "TEST-013"],
      fixtures: ["source/providers/openai-responses-stream-retry-error.json"],
      adr: "ADR-HLLM-001",
      note: "The Go transport maps the source provider-retry directive into its typed retry error while retaining the self-hosted provider boundary.",
    },
    {
      id: "profile-owned-model-catalog",
      mode: "intentional-difference",
      tests: ["TEST-015", "TEST-017", "TEST-024"],
      fixtures: ["source/models/model-registry-cases.json", "source/models/pricing-overrides.json"],
      adr: "ADR-HLLM-001",
      note: "The self-hosted service stores model discovery and pricing in owner-managed profiles instead of a package-global registry.",
    },
    {
      id: "postgres-resource-queries",
      mode: "intentional-difference",
      tests: ["TEST-020", "TEST-024"],
      fixtures: ["source/queries/query-helper-cases.json"],
      adr: "ADR-HLLM-001",
      note: "The self-hosted resource API implements owner-scoped PostgreSQL queries instead of source-specific query helpers.",
    },
    {
      id: "otel-telemetry-projection",
      mode: "intentional-difference",
      tests: ["TEST-016", "TEST-028", "TEST-031"],
      fixtures: ["source/telemetry/telemetry-cases.json"],
      adr: "ADR-HLLM-001",
      note: "The self-hosted runtime projects source telemetry semantics through OpenTelemetry and domain traces instead of source-specific process telemetry.",
    },
  ],
  fixtures: captured,
};
await writeFile(path.join(outputRoot, "manifest.json"), canonicalJSON(manifest), { mode: 0o644 });
console.log(`captured ${captured.length} fixtures from ${sourceSHA}`);
