# 1. Workflow coverage acceptance

- Plan: `PLAN-HLLM-WORKFLOW-COVERAGE-001`; issue #122.
- Branch: `test/workflow-coverage`.
- Baseline: `821116a1c6ae18f678b040172fce89c2da39ebe3`.
- Completion: pending final gates and scoped application deployment.

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

Pending corrected exact merged-SHA browser-free release, affected application image construction,
scoped apply and full identity/HTTP verification. Baseline production equivalence
was verified read-only across all seven descriptor services before any mutation.

## 5. Scope and limits

No additional paid provider calls or browsers ran. Deterministic providers are
owned local HTTP/TLS servers with synthetic keys; PostgreSQL uses isolated leases.
No production row, secret, session or provider response becomes a fixture.
The existing release task count and fast task count remain 29 and 10.
The [RCA](../../../../docs/rca/workflow-test-coverage-and-recording-uncertainty.md)
records the exact production change and corrected test assumptions. No data model, framework or service was added. The schema command gains its own
30 s administrative budget; request/inference/history limits are unchanged. The
KER/ADR/plan amendments explicitly record that correction.
