# Test-feedback hierarchy KER

`KER-HLLM-TEST-FEEDBACK-001` records the redacted reference-host evidence for
the resource-aware test hierarchy in
`PLAN-HARDEN-LLM-TEST-FEEDBACK-002`. The machine-readable record is
[`baseline.json`](baseline.json).

The committed record is `executionStatus: "measured"` from the reference-host
run recorded in `reference`. It was promoted only after every accepted sample
passed, the host fingerprint matched, process/resource cleanup was zero, all
lane metrics had coefficient of variation at most 20%, and the raw evidence
was written only below the ignored `plans/evidence/` tree. It contains
redacted aggregates and 25% warm-budget headroom; it does not contain raw
test output.

The final evidence includes a deterministic post-run re-aggregation note because
the first green run exposed an instrumentation defect in lane wall/CPU
aggregation. The underlying task observations and test commands were unchanged;
the accepted hash is the corrected aggregate artifact recorded in `baseline.json`.

Only redacted aggregates belong in this directory. Raw output, command logs,
provider responses, credentials, cookies, request bodies, and process
environments are forbidden. A benchmark sample with a failed task or cleanup
error is failed evidence and contributes no timing budget.

The benchmark entry point is:

```text
node scripts/benchmark-test-feedback.mjs --mode baseline --warm-samples 5 --cold-samples 3 --output plans/evidence/harden-llm/ptf-20260823/test-feedback-baseline.json
```

Threshold changes require a new measurement set and an amendment to
ADR-HLLM-015. The host fingerprint in the committed record is intentionally
specific; unlike-host results are reported, not silently compared as if they
were equivalent.
