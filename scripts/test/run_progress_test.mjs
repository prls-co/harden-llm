// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-256 TEST-257
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { fileURLToPath } from "node:url";
import { test } from "node:test";
import { createSSEParser, observeBudget, redactedReport } from "../run-progress-core.mjs";

test("TEST-256 parses split standard Responses SSE events and completion", () => {
  const parser = createSSEParser();
  assert.deepEqual(parser.push("event: response.output_text.delta\ndata: {\"delta\":"), []);
  const events = parser.push("\"hello\",\"type\":\"response.output_text.delta\"}\n\n");
  assert.equal(events[0].event, "response.output_text.delta");
  parser.push("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_fixture\"}}\n\n");
  assert.equal(parser.finish().terminal, true);
});

test("incomplete EOF is not success and heartbeat does not advance progress", () => {
  const parser = createSSEParser();
  parser.push(": heartbeat\n\n");
  const result = parser.finish();
  assert.equal(result.terminal, false);
  assert.equal(observeBudget({ startedAt: 0, now: 11_000, softMs: 10_000, caseHardMs: 30_000, terminal: false }).reason, "soft_overrun_observe");
});

test("hard caps stop observation without extending the case", () => {
  const decision = observeBudget({ startedAt: 0, now: 31_000, softMs: 10_000, caseHardMs: 30_000, suiteRemainingMs: 60_000 });
  assert.equal(decision.continue, false);
  assert.equal(decision.reason, "case_hard_cap");
  assert.equal(redactedReport({ ...decision, terminal: false, responseId: "resp", traceId: "trace" }).responseId, "resp");
});

test("TEST-257 a delivered Responses failure terminal remains a functional failure", () => {
  const report = redactedReport({ terminal: true, functionalFailure: true, runId: "run", traceId: "trace" });
  assert.equal(report.terminal, true);
  assert.equal(report.functionalFailure, true);
});

test("headless CLI requires HARDEN_LLM_TOKEN and sends one standard Responses request", async t => {
  const server = createServer((request, response) => {
    assert.equal(request.headers.authorization, "Bearer fixture-api-token");
    assert.equal(request.method, "POST");
    assert.equal(request.url, "/v1/responses");
    assert.equal(request.headers.accept, "text/event-stream");
    let body = "";
    request.setEncoding("utf8");
    request.on("data", chunk => { body += chunk; });
    request.on("end", () => {
      const parsed = JSON.parse(body);
      assert.equal(parsed.model, "fixture-model");
      assert.equal(parsed.input, "fixture");
      assert.equal(parsed.stream, true);
    });
    response.writeHead(200, { "Content-Type": "text/event-stream" });
    response.end('event: response.completed\ndata: {"type":"response.completed","response":{"id":"resp-test","status":"completed","harden":{"execution_id":"exec-test","trace_id":"trace-test"}}}\n\n');
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  t.after(() => new Promise(resolve => server.close(resolve)));
  const script = fileURLToPath(new URL("../run-progress.mjs", import.meta.url));
  const endpoint = `http://127.0.0.1:${server.address().port}/v1/responses`;
  const execute = (environment, args) => new Promise((resolve, reject) => {
    const child = spawn(process.execPath, [script, ...args], { env: environment, stdio: ["pipe", "pipe", "pipe"] });
    let stdout = "", stderr = "";
    child.stdout.setEncoding("utf8").on("data", chunk => { stdout += chunk; });
    child.stderr.setEncoding("utf8").on("data", chunk => { stderr += chunk; });
    child.on("error", reject);
    child.on("close", code => resolve({ code, stdout, stderr }));
    child.stdin.end('{"model":"fixture-model","input":"fixture","stream":true}');
  });

  const missingTokenEnvironment = { ...process.env };
  delete missingTokenEnvironment.HARDEN_LLM_TOKEN;
  const missing = await execute(missingTokenEnvironment, ["--url", endpoint, "--body-stdin"]);
  assert.equal(missing.code, 2);
  assert.match(missing.stderr, /HARDEN_LLM_TOKEN is required/);

  const accepted = await execute({ ...process.env, HARDEN_LLM_TOKEN: "fixture-api-token" }, ["--url", endpoint, "--body-stdin"]);
  assert.equal(accepted.code, 0, accepted.stderr);
  assert.match(accepted.stdout, /"terminal":true/);
  assert.match(accepted.stdout, /"responseId":"resp-test"/);
  assert.match(accepted.stdout, /"executionId":"exec-test"/);
});
