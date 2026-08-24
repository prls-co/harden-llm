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

P02 EVAL-003 is recorded separately at
`plans/evidence/harden-llm/p02-phoenix-async-eval.json` (SHA-256
`582282d778cca0e19da803a3eed7eee4cd51fa23864df0466a73c8f92c478271`). It
contains one warm deterministic frontend run for each of ten fixed seeds, with
the final manifest SHA
`86ac58bc384021eb1e064dc794412dd9896ceef7922bcbd1b07678cb90394599`. All
ten runs passed with zero Req ownership errors, zero leaked messages/processes,
and exactly two documented serial exceptions. Warm p50/p95/max wall time was
`3699/4125/4125 ms`; p95 RSS was `321.63 MiB`; p95 CPU was `18500 ms`.
The p95 was below the `10033 ms` sequential frontend reference. The first
rejected attempt exposed an ownership cleanup race at seed `181081`; it was
not accepted as timing evidence, and the final run used explicit LiveView
allowances and teardown.

P03 EVAL-004 is recorded separately at
`plans/evidence/harden-llm/client-core-eval.json` (SHA-256
`18d5145ab24007a32da0a67c8128756b02f787306ef94c0ddfc4291ce82d5885`). It
contains 30 warm samples of the dependency-free Node client-core task under
manifest SHA
`82fa6a792f45022125f8cdbe6008a3219dc84d709f2efb59f8d0b8d9275b46f5`. Warm
p50/p95/max wall time was `416/495/522 ms`; maximum RSS was `120.3984375 MiB`;
failures and cleanup leaks were zero. The comparator verified the 2-second
p95 limit, the accepted KER RSS budget, zero package installs, and zero
network attempts. The accidental baseline-mode invocation caused by the
original unqualified command was interrupted and is not evidence.

P04 EVAL-005 is recorded separately at
`plans/evidence/harden-llm/browser-canary-eval.json` (SHA-256
`11d7ed867aac8e73e9501c44b1a720539849e1060c1ee4ef2d54e3f9a9c295f2`). It
contains five warm samples and no cold samples under manifest SHA
`53e31dc061da4bf5ace6d8b3f3c6fe3ac68cb87442d8b7f05570ca6240088f38`.
Each sample ran exactly two ordinary browser features:
`widget_canary_test.exs` and `authenticated_workflow_canary_test.exs`; the
Compose feature remains separate. Wall time p50/p95/max was
`39589/39621/39621 ms`; peak RSS p50/p95/max was
`386.25/403.08/403.08203125 MiB`; the maximum coefficient of variation was
`0.0337`. Failures, cleanup leaks, screenshots, and remaining browser
containers were all zero. The browser image was
`harden-llm-browser-test:local` with host networking, the Docker socket, and
2 GiB shared memory. The comparator checked the exact ordinary-feature
inventory, two-session count, unchanged P00 p95/RSS budgets, screenshot
absence, and browser cleanup. Browser task CPU is retained as informational
runner output only because the Docker-client measurement is not an equivalent
Chromium-process measurement.

P05 EVAL-006 is recorded separately at
`plans/evidence/harden-llm/integration-pool-eval.json` (SHA-256
`bc7371b53497467754a1897deac27ca425b3638019eda343cf503563f46922a4`). It
contains 48 real service samples: five warm and three cold samples for each of
six normal/race package-cap candidates. Each ordinary task invocation started
one exact randomized service pool, allocated unique Postgres/Garage leases,
and cleaned the project and mutable namespaces. All 48 samples passed with
zero failures, zero leaked resources, 144 ordinary service-pool starts, and
zero overlap with the dedicated Garage restart lane. The selected caps are
three normal package slots and two race package slots. Selected normal warm
`go-integration` p95 was `14159 ms` with maximum RSS `474.375 MiB`; selected
race warm p95 was `36039 ms` with maximum RSS `716.41015625 MiB`.

The normal comparison uses the P00 integration p95 and accepted integration
RSS budget. P00 did not contain a race-specific integration baseline, so the
race comparator uses the accepted full-system warm wall/RSS ceilings as a
conservative instrumented budget. That preserves a safety bound and does not
change the original KER thresholds. Any future race-budget change requires a
new evidence set and an ADR-HLLM-015 amendment.

P06 EVAL-007 is the complete release-candidate measurement. It must execute
the manifest's `release` selector through `node scripts/run-test-tier.mjs`,
covering deterministic backend/frontend gates, pooled integration and race,
audits, ordinary browser canaries, and Compose certification without a public
network task. Its accepted record is written only after every selected task
passes, cleanup/leak counts are zero, and the bounded result is redacted.
The release candidate is the required bridge between the local hierarchy and
the later merged/deployed P07 certification; a pending or failed EVAL-007 is
not release evidence.

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
