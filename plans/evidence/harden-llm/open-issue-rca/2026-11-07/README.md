# Open-issue RCA, 2026-11-07

## 1. Scope

Analysis of all eight open Harden issues at source `3f8a5173086ea245841551fe988f6a3b03cff7f8`: [#96](issue-96.md), [#107](issue-107.md), [#113](issue-113.md), [#137](issue-137.md), [#138](issue-138.md), [#139](issue-139.md), [#142](issue-142.md), [#149](issue-149.md). Each report includes pinned code references, evidence limits, options, recommendation and blockers/user needs.

This branch contains information and isolated experiment evidence only. Main/application source, deployed configuration, issue state, browsers and live inference were unchanged. The experiment source files are retained as text, outside the test catalog. They assert current gaps and are not release acceptance tests.

## 2. Fresh verification

- Go owner-layer witnesses: include rejection, lost public search output, structured citation loss, Run schema rejection, encoded size measurements. Existing request-budget and provider search controls passed. See `experiments/go-witnesses-final.log`.
- Core recovery witness: rate-limit retry uses generation twice despite a configured rerun target, honoring one-second Retry-After. See `experiments/recovery-witness.log`.
- Existing classification/waiting and gateway Retry-After controls passed: `experiments/analytics-controls.log`, `experiments/retry-after-control.log`.
- Existing overlap/cancellation/timeout controls: 3 Node tests passed in `experiments/node-controls.log`.
- Importing-parent SIGTERM experiment: six descendants survived beyond the parent's task timeout, then all live descendants exited on owned barrier release. See `experiments/reproduction.json` and its three source scripts. PID/start identities are evidence for that run, not permission to signal those PIDs later.
- Read-only GitHub API checks revalidated both old CodeQL queued/no-runner jobs, current successful four-language scan, current default setup, and current consumer issue status. Reports retain the exact run/issue references; no settings/reruns were mutated.
- Artifact mutation is retained consumer evidence, not a fresh signed-download round trip. Exact effective Cloudflare configuration is still unknown.

Initial /tmp checkout/build setup hit the host's per-user temporary disk quota; /var/tmp checkout and GOTMPDIR resolved it. Early experiment setup errors (required task metadata and a non-exported citation alias) were corrected before the recorded final successful runs. No product behavior was changed to make witnesses pass.

## 3. Reproduction

Create an isolated checkout at the recorded base SHA. Copy each `experiments/*-rca_test.go.txt` to the corresponding package as `rca_experiment_test.go` (`gateway-httpapi` → `internal/gateway/httpapi`, `providers-providers` → `internal/providers`, `runtime-runtime` → `internal/runtime`). These files depend on existing package test fixtures.

```sh
go test ./internal/gateway/httpapi ./internal/providers -run '^TestRCA|^TestRequestBodyBudget|^TestSearchEvidenceAndToolFailures$' -v
go test ./internal/runtime -run '^TestRCA' -v
go test ./stateful/batch -run '^TestAttemptClassificationSeparatesQuotaAccountAndOrdinaryFailures$|^TestPoolWaitingProjectionUsesActualGateAndNoInventedETA$' -v
go test ./internal/gateway/httpapi -run '^TestRetryAfterSurvivesGateway$' -v
node --test --test-name-pattern='enforces resource slots|cancels|terminates a task' scripts/test/run_test_tier_test.mjs
```

For the standalone parent-death experiment, copy parent.mjs/worker.mjs/reproduce.py into a **fresh private directory**, update parent.mjs's runner import to your checkout, then run `python3 reproduce.py`. The controller intentionally SIGTERMs its owned importing parent and releases/signals only descendants bound to recorded start identities. It runs no Docker/browser/provider. Do not rerun in the evidence directory with stale fixture files. The original incident's exact interruption signal is not established by this sufficient-path reproduction.

## 4. Triage recommendation

Start with #139 if the existing ingress owner can inspect the Cloudflare rules; #137 is the substantive API correctness work. #107 has a reproduced bounded harness defect. Reconcile #138 after the closed NER-Go #3 Responses migration. #142 can be disposed of as no-build absent an unanswered operational need; #113 can be documented as recovered external incident or escalated for platform attribution. #96 needs an explicit rate-limit fallback policy/target and separate credential before enablement. #149 retains the accepted 256 KiB decision while larger-request research remains unqualified.
