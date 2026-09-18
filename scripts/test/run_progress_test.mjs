// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-256 TEST-257
import assert from "node:assert/strict";
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
