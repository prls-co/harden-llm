// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-400 PLAN-HLLM-PROXY-REFERENCE-001
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const read = (relativePath) => readFile(path.join(repositoryRoot, relativePath), "utf8");

const expectedTaskByTest = new Map([
  ["TEST-400", "go-static"],
  ["TEST-401", "go-unit"],
  ["TEST-402", "go-unit"],
  ["TEST-403", "go-unit"],
  ["TEST-404", "go-unit"],
  ["TEST-405", "frontend-deterministic"],
  ["TEST-406", "frontend-deterministic"],
  ["TEST-407", "frontend-reference-integration"],
  ["TEST-408", "go-static"],
  ["TEST-409", "go-compose"],
]);

test("profile-free OpenAI contract is traceable and assigned to the right existing tiers", async () => {
  const [fixtureText, testSpecification, requirementSpecification, traceability, manifestText] = await Promise.all([
    read("test/fixtures/proxy-reference-contract.json"),
    read("plans/from_utility-llm/harden-llm-self-hosted-test-spec.md"),
    read("plans/from_utility-llm/harden-llm-self-hosted-implementation-plan.md"),
    read("docs/requirements-traceability.md"),
    read("test/test-tiers.json"),
  ]);
  const fixture = JSON.parse(fixtureText);
  const manifest = JSON.parse(manifestText);
  const frontendSpecification = await read(fixture.frontendSpecificationPath);

  assert.equal(fixture.schemaVersion, 1);
  assert.equal(fixture.basePath, "/v1");
  assert.equal(fixture.authorizationScheme, "Bearer");
  assert.equal(fixture.apiKeyEnvironment, "HARDEN_LLM_TOKEN");
  assert.equal(fixture.upstreamCredentialEnvironment, "CPA_API_KEY");
  assert.equal(fixture.modelListPath, "/v1/models");
  assert.equal(fixture.chatCompletionsPath, "/v1/chat/completions");
  assert.equal(fixture.responsesPath, "/v1/responses");
  assert.deepEqual(fixture.chatRequestFields, ["model", "messages", "response_format", "tools", "tool_choice", "stream"]);
  assert.deepEqual(fixture.responsesRequestFields, ["model", "input", "instructions", "text.format", "tools", "tool_choice", "stream"]);
  assert.deepEqual(fixture.extensions, ["upstream", "cache", "timeout_ms", "recovery", "diagnostics"]);
  assert.equal(fixture.stateless, true);
  assert.equal(fixture.historyOwner, "reference-application");
  assert.equal(fixture.streaming, "buffer-final-hardened-outcome");
  assert.deepEqual(fixture.rejectedStatefulFields, ["previous_response_id", "background", "conversation"]);
  assert.deepEqual(fixture.limits, {
    requestBytes: 262144,
    upstreamResponseBytes: 16777216,
    outcomeBytes: 16777216,
    diagnosticsBytes: 65536,
  });
  assert.deepEqual(new Set(fixture.requestCases.map(({ path: requestPath }) => requestPath)),
    new Set(["/v1/chat/completions", "/v1/responses"]));
  assert.ok(fixture.requestCases.some(({ body }) => body.messages?.some((message) => message.tool_call_id === "call_fixture")));
  assert.ok(fixture.requestCases.some(({ body }) => body.input?.some((item) => item.type === "function_call_output")));

  const registeredTaskIds = new Map();
  for (const task of manifest.tasks) {
    for (const testId of task.testIds ?? []) {
      if (!expectedTaskByTest.has(testId)) continue;
      assert.equal(registeredTaskIds.has(testId), false, `${testId} has more than one runner owner`);
      registeredTaskIds.set(testId, task.id);
    }
  }
  for (const [testId, expectedTaskId] of expectedTaskByTest) {
    assert.equal(registeredTaskIds.get(testId), expectedTaskId, `${testId} runner owner`);
    assert.ok(new RegExp("^\\| " + testId + " \\|", "m").test(testSpecification), `${testId} canonical test definition`);
  }

  for (const testId of fixture.frontendTestIds) {
    assert.ok(new RegExp(`\\| ${testId} \\|`).test(frontendSpecification), `${testId} canonical frontend definition`);
  }

  for (let requirement = 400; requirement <= 415; requirement += 1) {
    const requirementId = `REQ-${requirement}`;
    assert.ok(requirementSpecification.includes(requirementId), `${requirementId} canonical requirement`);
    assert.match(traceability, new RegExp(`\\b${requirementId}\\b`), `${requirementId} traceability row`);
  }
  const databaseTask = manifest.tasks.find(({ id }) => id === "frontend-reference-integration");
  assert.ok(databaseTask, "existing integration runner task is registered");
  assert.equal(databaseTask.workingDirectory, "frontend");
  assert.equal(databaseTask.servicePool?.services?.length, 1);
  assert.equal(databaseTask.servicePool.services[0].name, "harden-postgres");
});
