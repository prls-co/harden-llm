# 1. Workflow coverage acceptance

- Plan: `PLAN-HLLM-WORKFLOW-COVERAGE-001`; issue #122.
- Implementation PRs: #123, #124 (insufficient fixture warm-up), #125 (schema lifecycle correction).
- Application branch: `main`; closeout branch: `docs/workflow-coverage-closeout`.
- Baseline: `821116a1c6ae18f678b040172fce89c2da39ebe3`.
- Completion: P00–P05 complete; exact-source release and production acceptance verified.

## 2. Retained phase evidence

| Phase | Evidence and actual boundary |
| --- | --- |
| P00 | `registration-red.log` / `registration-green.log`; three focused Node cases and Go tier policy passed. |
| P01 | `native-workflow-first-failure.json` retains the original-view CAS mistake. Initial corrected T3 run `1791956993560-435089-7a55d5ea26f4401a` passed all 80 preset runs and six-stage routing. Final strict oracle includes metadata-only prompt and missing-default mutations. |
| P02 | Focused LiveView admission/ordering/coalescing tests passed; late selection also checks final persisted state. |
| P03 | Capability, option, schema and recovery decision cases passed, including all 500 native finite interactions. Original selected target and actual producer remain separately asserted. |
| P04 | PostgreSQL integration run `1791959127537-656482-b97e3a7fc1c1841f` passed. The fast missing-confirmation case reproduced the message defect and passed unchanged after the minimal production fix. Final transport run `1791982376061-2944295-e01ea0043eb7743d` passes with actual upstream cancellation and exact dispatch/record counts. Corrected race run `1791982671288-3015333-ca76e16777e98427` passes after clearing the observation snapshot, with the same overlap/CAS assertions. |

## 3. Final local gates

`fast-pass.json` retains all ten fast tasks passing, no cleanup issues.
`workflows-pass.json` retains the existing T3 task passing all 80 reloaded preset
calls, six-stage routing and both actual transport cuts. The owned failure cleanup
was then tightened to release fixture barriers before closing the listener; exact
merged-source certification also checks that final source.

`integration-race-pass.json` retains all integration packages passing with the
race detector, no cleanup issues. The failed preceding observation is retained
in `integration-race-red.json`, with its root cause in the RCA.

## 4. Release and deployed identity

First exact-main release `38568845136` at `b361d2344ea2b47797b3d05b0432d62e62efaeaf`
failed during existing stateful fixture setup, before assertions. The RCA explains
the bounded pool-readiness correction and retains `release-first-failure.json`.
Production was not changed. The corrected fixture passes the focused integration
and integration-race gates; `pool-readiness-integration-pass.json` and
`pool-readiness-race-pass.json` retain the results with no cleanup issues.

Second exact-main release `38572926810` at `2e65c8094aaa9289c5d97356ec8a0ca0c3889ea6`
failed inside schema setup even with a ready connection. `release-second-failure.json`
and TEST-466 RED/GREEN are retained. The RCA explains the administrative schema
budget correction and removal of the ineffective fixture Ping. Neither failed
candidate was deployed. `schema-budget-fast-pass.json`,
`schema-budget-integration-pass.json` and `schema-budget-race-pass.json` retain
the corrected local gates with no cleanup issues. The final test-only tightening
reserves a connection for SQL reset observation and preserves an already-expired
caller error; the focused deadline and race runs include it. Hosted exact-source
checks remain the final candidate evidence.

Corrected application source `4ad4072425a0e67ad48b3c5170008a1a362004e6` is merged through PR #125.
[Release 38575382378, attempt 1](https://github.com/prls-co/harden-llm/actions/runs/38575382378/attempts/1) passed all
29 tasks, plus its retained nested integration/race reports, with zero cleanup
errors/warnings. Exact-source main fast and all three CodeQL analyses also passed
(runs 38575358133 and 38575357448). `accepted-release.json` records the job/steps;
`release-accepted-report.json` and `go-integration*-accepted-report.json` retain
the executed gates. Standards techniques do not imply formal conformance.

Clean exact-source local builds produced:

- Gateway: `sha256:2ddcc9f7f2b76bc58a27129540471628844373d5612804e801e384050c66707a`.
- Frontend: `sha256:86b15b12186922785099fcbb742d7e51d4034a5661b7094f4b549f5216ffe8e6`.
- Both immutable tags: `harden-llm-<component>:release-4ad4072425a0e67ad48b3c5170008a1a362004e6`.
- Runtime: `https://harden-llm.prls.co/`, both healthy with zero restarts.

The reviewed descriptor changed only the ten application image/release fields.
The existing apply ran the accepted schema image successfully before both apps;
scoped and full seven-service checks are equivalent. TEST-435 passed health,
readiness, bearer/scope rejection, models and feature status without inference
or stored-record reads. Portal login returns 200. All 40 Git preset definitions
and starting inputs match API readback; 30 have deployment credentials. The
other ten still require OpenAI/Anthropic keys. This is configuration acceptance,
not a new live-provider qualification.

Shared/telemetry/identity/ingress container identities and protected environment/
metadata digests are unchanged. Schema-compatible previous images and a private
mode-0600 descriptor checkpoint are retained. The owned build worktree was removed.
`deployment-acceptance.json` owns exact identity/comparison evidence. Subsequent
docs-only commits do not rebuild or relabel these application images.

The temporary preset-readback script initially expected a Save-style envelope
for GET detail. OpenAPI defines a direct `ProfileDraft`; correcting that script
completed the read-only comparison with the same exact-value oracle. No product
change or mutation/replayed inference was made for that operator error.

## 5. Scope and limits

No additional paid provider calls or browsers ran. Deterministic providers are
owned local HTTP/TLS servers with synthetic keys; PostgreSQL uses isolated leases.
No production row, secret, session or provider response becomes a fixture.
The existing release task count and fast task count remain 29 and 10.
The [RCA](../../../../docs/rca/workflow-test-coverage-and-recording-uncertainty.md)
records the exact production change and corrected test assumptions. No data model, framework or service was added. The schema command gains its own
30 s administrative budget; request/inference/history limits are unchanged. The
KER/ADR/plan amendments explicitly record that correction.

The deployment runbook also contained a stale pre-decoupling `audit-artifacts`
step for a command explicitly retired by the current CLI contract. Its current
reference-wrapper check now names TEST-435 feature readiness and unchanged
shared-storage identities. It explicitly does not claim exhaustive live-object
integrity; artifact-storage changes still require their distinct integrity gates.
No executable test oracle or required release task was removed.

`cleanup-acceptance.json` verifies zero containers, volumes or networks across
all eleven task-owned local pool projects. Fourteen redacted local reports are
retained here; redundant runner copies were cleared. Test-owned directories and
the build worktree are absent; unrelated resources and operational recovery
checkpoints/images are preserved. Disposable operator scratch is removed after
GitHub closeout, separately from protected production settings/checkpoints.
