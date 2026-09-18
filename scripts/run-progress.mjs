#!/usr/bin/env node
// TEST-257. Request-bound REST SSE reference client; never retries a POST.
import { readFile } from "node:fs/promises";
import { createSSEParser, observeBudget, redactedReport } from "./run-progress-core.mjs";

function option(name, fallback) {
  const index = process.argv.indexOf(name);
  return index < 0 ? fallback : process.argv[index + 1];
}

const endpoint = option("--url", process.env.HARDEN_LLM_URL);
const bodyFile = option("--body-file", process.env.HARDEN_LLM_BODY_FILE);
const readStdin = process.argv.includes("--body-stdin");
const caseHardMs = Number(option("--case-hard-ms", 60_000));
const suiteHardMs = Number(option("--suite-hard-ms", 0));
const softMs = Number(option("--soft-ms", 0));
if (!endpoint || (!bodyFile && !readStdin) || (bodyFile && readStdin) || process.argv.includes("--body")) {
  console.error("usage: run-progress.mjs --url URL --body-file PATH|--body-stdin [--soft-ms N] [--case-hard-ms N] [--suite-hard-ms N]");
  process.exit(2);
}
if (!Number.isSafeInteger(caseHardMs) || caseHardMs <= 0 || !Number.isSafeInteger(suiteHardMs) || suiteHardMs < 0 || !Number.isSafeInteger(softMs) || softMs < 0) {
  console.error("budget values must be nonnegative integers; case-hard-ms must be positive");
  process.exit(2);
}
let bodyText;
try {
  bodyText = bodyFile ? await readFile(bodyFile, "utf8") : await new Promise((resolve, reject) => {
    let value = "";
    process.stdin.setEncoding("utf8");
    process.stdin.on("data", (chunk) => { value += chunk; });
    process.stdin.on("end", () => resolve(value));
    process.stdin.on("error", reject);
  });
} catch {
  console.error("could not read request body");
  process.exit(2);
}
let body;
try { body = JSON.parse(bodyText); } catch { console.error("request body must be valid JSON"); process.exit(2); }

const startedAt = Date.now();
const controller = new AbortController();
const timer = setTimeout(() => controller.abort(), caseHardMs);
let report = { startedAt, terminal: false, functionalFailure: false, performanceOverrun: false, eventCount: 0 };
try {
  const response = await fetch(endpoint, {
    method: "POST",
    headers: {
      Accept: "text/event-stream", "Content-Type": "application/json",
      ...(process.env.HARDEN_LLM_TOKEN ? { Authorization: `Bearer ${process.env.HARDEN_LLM_TOKEN}` } : {}),
    },
    body: JSON.stringify(body), signal: controller.signal,
  });
  if (!response.ok || !response.body) throw new Error(`run request failed: HTTP ${response.status}`);
  const parser = createSSEParser();
  const reader = response.body.getReader();
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    for (const event of parser.push(value)) {
      const data = event.data?.data ?? event.data ?? {};
      report = {
        ...report,
        eventCount: report.eventCount + 1,
        runId: event.data?.runId ?? data.runId ?? report.runId,
        traceId: event.data?.traceId ?? data.traceId ?? report.traceId,
        lastStage: data.stage ?? report.lastStage,
        attemptsUsed: data.attemptsUsed ?? report.attemptsUsed,
        receivedBytes: data.receivedBytes ?? report.receivedBytes,
        outputBytes: data.outputBytes ?? report.outputBytes,
        outputCodePoints: data.outputCodePoints ?? report.outputCodePoints,
        lastActivity: data.lastActivity ?? report.lastActivity,
        stopReason: data.stopReason ?? report.stopReason,
        terminal: ["run.completed", "run.failed"].includes(event.event),
        functionalFailure: report.functionalFailure || event.event === "run.failed",
      };
      const budget = observeBudget({ startedAt, softMs, caseHardMs, suiteRemainingMs: suiteHardMs || Infinity, terminal: report.terminal });
      report = { ...report, ...budget, performanceOverrun: budget.softOverrun };
      if (!budget.continue) { controller.abort(); break; }
    }
    if (report.terminal) { await reader.cancel(); break; }
  }
  const finished = parser.finish();
  if (!report.terminal && finished.events.some((event) => ["run.completed", "run.failed"].includes(event.event))) report.terminal = true;
  if (!report.terminal) report.stopReason = report.stopReason ?? "missing_terminal";
} catch (error) {
  report.stopReason = report.stopReason ?? (error?.name === "AbortError" ? "case_hard_cap" : "transport_error");
} finally {
  clearTimeout(timer);
}
report.elapsedMs = Date.now() - startedAt;
report.remainingCaseMs = Math.max(0, caseHardMs - report.elapsedMs);
const safe = redactedReport(report);
console.log(JSON.stringify(safe));
process.exit(report.terminal && !report.functionalFailure && !report.performanceOverrun ? 0 : 1);
