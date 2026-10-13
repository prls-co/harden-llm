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
