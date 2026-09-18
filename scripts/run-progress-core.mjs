// TEST-256 TEST-257. Dependency-free SSE and observation-budget primitives.

export function createSSEParser() {
  let buffer = "";
  let events = [];
  let terminal = false;
  const decoder = new TextDecoder();

  function consume(block) {
    const fields = { data: [] };
    for (const line of block.replaceAll("\r\n", "\n").split("\n")) {
      if (line === "" || line.startsWith(":")) continue;
      const separator = line.indexOf(":");
      const name = separator < 0 ? line : line.slice(0, separator);
      const value = separator < 0 ? "" : line.slice(separator + 1).replace(/^ /, "");
      if (name === "data") fields.data.push(value);
      else if (name === "event" || name === "id") fields[name] = value;
    }
    if (fields.data.length === 0) return;
    const payload = JSON.parse(fields.data.join("\n"));
    const event = { id: fields.id ?? null, event: fields.event ?? "message", data: payload };
    events.push(event);
    if (["run.completed", "run.failed"].includes(event.event)) terminal = true;
  }

  return {
    push(chunk) {
      buffer += typeof chunk === "string" ? chunk : decoder.decode(chunk, { stream: true });
      const blocks = buffer.split(/\r?\n\r?\n/);
      buffer = blocks.pop() ?? "";
      for (const block of blocks) consume(block);
      return events.splice(0);
    },
    finish() {
      buffer += decoder.decode();
      if (buffer.trim() !== "") consume(buffer);
      const result = events.splice(0);
      return { events: result, terminal, complete: terminal && buffer.trim() === "" };
    },
  };
}

export function observeBudget({ startedAt, now = Date.now(), softMs = 0, caseHardMs, suiteRemainingMs = Infinity, terminal = false }) {
  const elapsedMs = Math.max(0, now - startedAt);
  const remainingCaseMs = Math.max(0, caseHardMs - elapsedMs);
  const suiteExceeded = elapsedMs > suiteRemainingMs;
  const softOverrun = softMs > 0 && elapsedMs > softMs;
  const hardExceeded = remainingCaseMs === 0 && !terminal;
  return {
    continue: !hardExceeded && !suiteExceeded,
    reason: hardExceeded ? "case_hard_cap" : suiteExceeded ? "suite_hard_cap" : terminal ? "terminal" : softOverrun ? "soft_overrun_observe" : "within_budget",
    elapsedMs,
    remainingCaseMs,
    softOverrun,
    suiteExceeded,
  };
}

export function redactedReport(report) {
  return {
    schemaVersion: 1,
    runId: report.runId ?? null,
    traceId: report.traceId ?? null,
    lastStage: report.lastStage ?? null,
    elapsedMs: report.elapsedMs ?? null,
    remainingCaseMs: report.remainingCaseMs ?? null,
    attemptsUsed: report.attemptsUsed ?? null,
    receivedBytes: report.receivedBytes ?? null,
    outputBytes: report.outputBytes ?? null,
    stopReason: report.stopReason ?? null,
    terminal: Boolean(report.terminal),
    functionalFailure: Boolean(report.functionalFailure),
    performanceOverrun: Boolean(report.performanceOverrun),
  };
}
