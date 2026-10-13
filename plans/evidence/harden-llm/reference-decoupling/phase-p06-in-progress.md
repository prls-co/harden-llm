# P06 release qualification and deployment log

## 1. Candidate review

P00–P05 are pushed through `b4d270d11b64056074788b92eb8ec68807578164`.
`ab8f8963c972ae2ce709011196b93f7189d32bf9` adds TEST-435, the explicit
browser-free deployed acceptance command. It was compiled, not run against
production before deployment. PR #101 targets main; production is unchanged.

PR CodeQL review found two capacity-arithmetic allocation alerts (6 and 7)
in provider payload and response chunk construction. Removed the optional
preallocation arithmetic, preserving the existing append loops and wire output.
Focused provider/HTTP tests and the ten-task fast gate pass; see
`p06-allocation-tests.log` and `p06-allocation-fast.json` (accepted, zero cleanup
errors or warnings). Hosted checks will rerun on the resulting commit.

Alert 8 (`go/weak-sensitive-data-hashing`) was reviewed and dismissed as a
false positive on the specific alert, without suppressing the query. SHA-256
only derives a storage namespace after `auth.MatchBearer` validates the raw
bearer; the digest is not an authentication verifier or password hash. TEST-425
proves a digest cannot authenticate, consistent with ADR-HLLM-033. The review
comment is recorded on GitHub alert 8.

The release gate remains an explicit successful main-branch `suite=release`
workflow attempt at the exact accepted SHA. No browser or paid inference check
is authorized. Shared service provisioning and production apply remain pending.

## 2. First hosted release attempt (not accepted)

PR #101 merged at `70e879683804689b7778e8b85f3892f7e52f8d88`.
Explicit release run 38371244757 attempt 1 built the production Phoenix image,
but its 28-task release selection stopped at the `go-static` 120-second command
budget. The runner records that task as timed out; lint/build/integration and
runner contracts were cancelled, not passing. Cleanup had zero errors/warnings.
The full log reports an empty Go build cache. No production mutation occurred.

A separate cold-cache `make test-static` measurement on two CPU cores passed
unchanged in 91.289 seconds, peak command RSS 659972 KiB (not total host RAM).
Its original budget left less than 29 seconds for concurrent cold compilation.
The release starts four CPU tasks plus an integration compiler; `go-build` was
also still compiling when cancellation occurred at 120 seconds. Align the two
compilation command budgets (`go-static`, `go-build`) with the existing ten-minute
`go-lint` budget. Keep all commands, task selection, concurrency, assertions,
provider/SQL deadlines and cancellation behavior unchanged. This is a bounded
execution allowance for cold CI, not a relaxed behavioral assertion or acceptance
of the failed attempt. The next release requires a new exact-SHA dispatch.

Retained evidence: `p06-release-attempt1-reports/`,
`p06-release-attempt1-failed.log`, `p06-static-cold.json`,
`p06-static-cold-metrics.txt`. Hosted release remains required after this fix.

Independent `node scripts/verify-test-tiers.mjs` additionally found a stale
literal command: P05 added `-v` to preserve the composed measurement output,
but this standalone validator still expected the older spelling. Updated its
single shared `composeCommand` expectation; it still requires the exact raw
Compose test, count=1, managed lifecycle and browser-free boundary. The first
validation failed and is not counted as passing; the subsequent command result
is the acceptance evidence. No application behavior changed.

## 3. Security refresh after the second release attempt

Run 38373119904 attempt 1 at `1e450b8eb87545cbbe902a29591fb40be56a16c8`
passed static/build/lint, unit/parity/API/observability, Compose contracts and
real PostgreSQL integration, then failed `go-vulnerability`. Remaining tasks
were cancelled; this is not release acceptance. No cleanup issues occurred.
The scan reported 11 reachable standard-library advisories, including
GO-2026-6617 also affecting `golang.org/x/net` v0.58.0.

Verified the Go release history and official vulnerability record; moved the
module, CI and three builder pins to Go 1.26.9 and the official Alpine 3.23
manifest `sha256:6750308561953d451210ed1a8086d36c8c8bdcbf672d38d9d0d424b8a7a0216c`.
Updated x/net to v0.60.0 and its required x/* dependency chain using `go get` and
`go mod tidy`. No source behavior or vulnerability assertions were suppressed.
ADR-HLLM-011 records the security refresh and browser opt-in boundary.

`go tool govulncheck ./...` now reports no vulnerabilities. The complete local
fast gate passes all ten tasks with zero cleanup issues (`p06-security-fast.json`).
A new exact-SHA hosted release, immutable builds and deployment remain required.
Production and shared services are unchanged.

## 4. Race-instrumented history timing resources

Release run 38374777830 attempt 1 at `415aaee0e87dbe8d09ac015caceffd35aa72ee53`
passes the vulnerability scan and ordinary integration, but the real database
race test stores the exact 17 MiB record in 2.535426566 s, exceeding its unchanged
two-second assertion. All other nonzero started tasks have SIGTERM cancellation;
there is no additional hidden failure. Cleanup has zero errors/warnings.

Inspection confirms one canonical payload decode, with no duplicate large-body
validation to remove. The existing two-CPU race benchmark measures 460.373062 ms
per canonical validation, dominated by race instrumentation. A focused real-PG
race invocation on two CPUs passes all `TestHistoryStore` cases, including exact
17 MiB insert 665.220487 ms and detail 532.782229 ms, zero cleanup issues.
The temporary harness initially reversed report-writer arguments after cleanup;
that reporting error is retained and is not accepted evidence. The corrected
invocation's actual result is `p06-history-race-exclusive.json`.

Use the runner's existing exclusive `release` resource for `go-integration-race`
to prevent independent compiler/CPU-task overlap during instrumented SQL timing.
Keep the same command, race flag, two package slots, real service pool, payload
checks and SQL/timing assertions. TEST-008's cheap resource invariant fails on
the old manifest (`p06-race-resources-red.log`) and passes after the one-field
change (`p06-race-resources-green.log`). ADR-HLLM-015 records the named host-CPU
measurement resource and evidence; no application optimization or scheduler was
introduced. Full hosted certification is still required after this correction.

## 5. Certified release and production comparison correction

Run 38377471293 attempt 1 at `bbdb5d77bc76424804427ca30a7bf9ddc453d0cf`
passes all 28 release tasks, including nested `make verify`; all three uploaded
runner reports are accepted with zero cleanup errors/warnings. The operator
build helper initially rejected the attempt URL because `gh run view --attempt`
returns `/attempts/1`; no build had started. Corrected the exact expected attempt
URL in the helper and canonical build procedure, retaining exact run/attempt/SHA
checks. Both immutable images then built from a clean checkout at that SHA.

The initial production apply successfully ran the exact gateway-image schema
command and converged both healthy applications, but its final check reported
`harden-llm-gateway.networks`. The checker incorrectly compared Compose's logical
`shared-postgres` key with Docker's physical `compose_default` name. Inspection
confirmed the intended networks. TEST-234 reproduces this false mismatch, then
checks wrong, extra and missing memberships. The correction compares resolved
`model.networks[*].name` directly with Docker names and rejects incomplete model
metadata; it removes the old hardcoded project-prefix stripping. All 29 focused
Node deployment tests pass, and the real application check has zero differences.
A repeated canonical apply is a no-op: no second schema or application restart.
This deployment-tool correction does not change the certified application images.
The initial failed postcheck remains recorded in `p06-initial-apply.json`.
