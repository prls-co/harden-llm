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

P01 EVAL-002 is recorded separately from the P00 reference KER at
`plans/evidence/harden-llm/p01-fast-eval.json` (SHA-256
`8a70d8dab2ad801a670969147e7fc2094d4bfcf0ee7ef7ca1b3b6be0122133e3`). It
contains 3 cold and 5 warm parallel fast-lane samples on the same host, with
the manifest's four CPU slots, identical six-task selection, zero failures and
zero leaks. Warm p95 wall time was 8078 ms against the 20495.2 ms budget; the
maximum RSS across all samples was 486.2265625 MiB against the KER accepted
601.09 MiB RSS budget. The candidate manifest SHA is
`9256c998aaa9a80d3cd82fa92bcd1a907fccc4c9a6439e2df2113dd5c7ecda6f`; the
P00 reference hash remains the historical sequential baseline.

Only redacted aggregates belong in this directory. Raw output, command logs,
provider responses, credentials, cookies, request bodies, and process
environments are forbidden. A benchmark sample with a failed task or cleanup
error is failed evidence and contributes no timing budget.

The canonical execution entry point is:

```text
node scripts/run-test-tier.mjs --task fast
```

The benchmark/evidence adapter is:

```text
node scripts/benchmark-test-feedback.mjs --mode baseline --warm-samples 5 --cold-samples 3 --output plans/evidence/harden-llm/ptf-20260823/test-feedback-baseline.json
```

Threshold changes require a new measurement set and an amendment to
ADR-HLLM-015. The host fingerprint in the committed record is intentionally
specific; unlike-host results are reported, not silently compared as if they
were equivalent.
