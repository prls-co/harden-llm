// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-256 TEST-257
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { fileURLToPath } from "node:url";
import { test } from "node:test";
import { createSSEParser, observeBudget, redactedReport } from "../run-progress-core.mjs";

test("SSE parser handles split data and terminal events", () => {
  const parser = createSSEParser();
  assert.deepEqual(parser.push("event: run.progress\ndata: {\"stage\":"), []);
  const events = parser.push("\"original.generate\"}\n\n");
  assert.equal(events[0].event, "run.progress");
  parser.push("event: run.completed\ndata: {\"runId\":\"r\"}\n\n");
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
  assert.equal(redactedReport({ ...decision, terminal: false, runId: "run", traceId: "trace" }).runId, "run");
});

test("a delivered run.failed terminal remains a functional failure", () => {
  const report = redactedReport({ terminal: true, functionalFailure: true, runId: "run", traceId: "trace" });
  assert.equal(report.terminal, true);
  assert.equal(report.functionalFailure, true);
});

test("headless CLI requires a machine token and sends it as the bearer credential", async t => {
  const server = createServer((request, response) => {
    assert.equal(request.headers.authorization, "Bearer fixture-api-token");
    response.writeHead(200, { "Content-Type": "text/event-stream" });
    response.end('event: run.completed\ndata: {"runId":"run-test","traceId":"trace-test"}\n\n');
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  t.after(() => new Promise(resolve => server.close(resolve)));
  const script = fileURLToPath(new URL("../run-progress.mjs", import.meta.url));
  const endpoint = `http://127.0.0.1:${server.address().port}/api/v1/run`;
  const execute = (environment, args) => new Promise((resolve, reject) => {
    const child = spawn(process.execPath, [script, ...args], { env: environment, stdio: ["pipe", "pipe", "pipe"] });
    let stdout = "", stderr = "";
    child.stdout.setEncoding("utf8").on("data", chunk => { stdout += chunk; });
    child.stderr.setEncoding("utf8").on("data", chunk => { stderr += chunk; });
    child.on("error", reject);
    child.on("close", code => resolve({ code, stdout, stderr }));
    child.stdin.end('{"profileId":"fixture","userPrompt":"fixture","callType":"text"}');
  });

  const missingTokenEnvironment = { ...process.env };
  delete missingTokenEnvironment.HARDEN_LLM_API_TOKEN;
  const missing = await execute(missingTokenEnvironment, ["--url", endpoint, "--body-stdin"]);
  assert.equal(missing.code, 2);
  assert.match(missing.stderr, /HARDEN_LLM_API_TOKEN is required/);

  const accepted = await execute({ ...process.env, HARDEN_LLM_API_TOKEN: "fixture-api-token" }, ["--url", endpoint, "--body-stdin"]);
  assert.equal(accepted.code, 0, accepted.stderr);
  assert.match(accepted.stdout, /"terminal":true/);
});
