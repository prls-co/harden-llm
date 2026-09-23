# Codebase reduction execution results

## Status and authorization

Execution of `PLAN-HLLM-CODEBASE-REDUCTION-001` is in progress. The user
authorized all plan phases, required tests, non-force delivery to GitHub
`main`, supported publication, and production deployment. Work is on
`feat/codebase-reduction`, based on `main` at
`004bf5042040c673c10b691bd57a3c384ef9b9ff` plus the plan-only commit
`26166c978154fe96bfbe383a369c0cad3a8ba5bb`. No application source is changed
at this checkpoint.

## P00 — Refreshed baseline (2026-09-22)

### Source and certification

- Starting `main` was clean at `004bf5042040c673c10b691bd57a3c384ef9b9ff`.
- Exact web candidate: `6887fcd8146961dc64598dd7a236e7a9fc522c9c`. Both frontend
  commits `40d19dc5bb5962e5e1c31905e4dbd5dc69fa7667` and
  `2106948` are ancestors of this candidate. `git diff --name-status`
  found no `frontend/` changes from this candidate through the current `main`
  source, so it contains the latest frontend inputs on that release line.
- GitHub Actions run [35726391122](https://github.com/prls-co/harden-llm/actions/runs/35726391122)
  is workflow dispatch attempt 1 on the exact candidate SHA. Its
  `browser-free exact-source release certification` job completed successfully,
  including `make test-release`; the separate gateway image publication job
  also completed successfully at the time. The private package has since been
  retired, so this is test certification, not a usable web artifact receipt.
- Current `main` is 89 commits ahead of `dev`; it is the correct implementation
  base because `dev` does not contain the reviewed current release line.

### Production identities and read-only checks

Production descriptor checks used the approved Docker context `default` and
reported runtime verified, with no changes applied. Selected container fields:

| Service | Container | Running release | Image ID | Health | Restarts |
| --- | --- | --- | --- | --- | --- |
| Web | `harden-llm-harden-llm-web-1` | `3201fd249f86031292be1c47acf64e0eb8a4540b` | `sha256:299439921295e6037a0cc01e1b1893e5bd1c2e441b740021479ca6666c1e5276` | healthy | 0 |
| Gateway | `harden-llm-harden-llm-gateway-1` | `6887fcd8146961dc64598dd7a236e7a9fc522c9c` | `sha256:036d82749a1e5a29e36c848d5fca955c4b416858c9ea272f2cf1b4428210905b` | healthy | 0 |

Both service-specific `production-config check --expected-release` commands
matched those running releases. The web container's version and release
environment identify `3201fd2`; it has no OCI revision label. The gateway
reports revision, version, and release as `6887fcd`. Runtime platform is
`linux/x86_64`. No credentials, environment values, or session data were
recorded.

### Finding F-001: inherited recovery policy remains undelivered

- **Observed:** production web is still at `3201fd2`, which predates the
  inherited retry controls and owner-navigation action in frontend commits
  `40d19dc5bb5962e5e1c31905e4dbd5dc69fa7667` and `2106948`.
- **Impact:** users of nested recovery widgets do not see the effective
  inherited retry settings or the intended owner navigation.
- **Remedy:** P01 builds the exact certified web source above with the current
  local-image process and applies only `harden-llm-web`, preserving the current
  gateway and session volume. F-001 stays open until runtime identity and
  service probes confirm the delivery.
- **Fresh local checks:** F-WIDGET passed 31 tests; F-WORKSPACE passed 83 tests
  (both seed `104729`, pinned Elixir 1.20.2/OTP 28.4.3). The expected
  `:simulated_task_exit` task logs appeared in two passing workspace tests.
- **Retained versus new evidence:** run 35726391122 is retained exact-source
  release certification; the two focused test commands above were run locally
  during this execution. No browser, provider, profile-save, or application
  release operation was run during P00.

### P02 boundaries at the initial baseline

The following boundaries remain unreviewed. P00 establishes no conclusion about
their safety or correctness:

1. Authentication and owner isolation.
2. Credentials, origin binding, and diagnostics.
3. Retry budgets, cancellation, and completion.
4. Cache isolation, admission, and accounting.
5. Artifact publication, deletion, and crash convergence.

These five boundaries were unreviewed when P00 was recorded. Their completed
reviews and evidence are in the P02 section below. P01 still needs a verified
rollback image and descriptor checkpoint before any production apply.

## P02 — Critical-boundary review (2026-09-22)

### Review records

| Boundary | Source path / symbols | Invariant and evidence | Result and remaining uncertainty |
| --- | --- | --- | --- |
| Authentication and owner isolation | `internal/gateway/httpapi/httpapi.go`, `internal/gateway/httpapi/resources.go`, `internal/gateway/auth/service.go`, `internal/gateway/profile_service.go`, `internal/postgres/resources.go`; Phoenix auth/session/controller entrypoints | Protected routes authenticate before handlers; handlers take `OwnerID` from the authenticated principal; owner predicates continue through SQL/resource operations. G-AUTH, F-AUTH, and managed INTEGRATION passed, including `TestAuthProfileContract` and `TestResourceRoutes`. | No cross-owner defect found in the reviewed routes. Review does not cover every application path or certify the whole product. |
| Credentials, origin binding, and diagnostics | `internal/profiles/credentials.go`, `internal/gateway/runtime_profiles.go`, `internal/gateway/profile_service.go`, `internal/gateway/shared_runtime.go`, `internal/providers/router.go`, `internal/providers/endpoint.go`, `internal/diagnostics/diagnostics.go`, `frontend/lib/harden_llm_web/session_vault.ex`, profile credential staging handlers | AES-GCM associated data binds owner, credential ID, and normalized HTTPS origin; shared adapters check request-local owner; provider auth is set after sanitized custom headers and redirects are denied. F-WIDGET, F-AUTH, G-AUTH, diagnostics/providers tests, and managed TEST-022 passed. WEB-TEST-035 covers staged-key cancellation; TEST-022 covers saving without a replacement credential. | No secret exposure found in the reviewed paths. Actual native-browser hook behavior is outside LiveViewTest; browser layout was not checked. The real provider path was not exercised. |
| Retry budgets, cancellation, and completion | `internal/runtime/execute.go`, `internal/runtime/recovery_execute.go`, `internal/retry/retry.go`, `internal/gateway/run_service.go`, `internal/gateway/httpapi/resources.go`, `frontend/lib/harden_llm_web/harden_api.ex`, `scripts/run-progress-core.mjs` | One policy budget covers generation/repair/rerun and transport retries; preparation and enabled credential checks happen before dispatch; retry waits use the run context; client streaming requires a valid terminal event and does not auto-retry. G-RUNTIME, G-STREAM, N-PROGRESS, G-RACE, and managed INTEGRATION passed. | No bounded-budget, cancellation, or duplicate-terminal defect reproduced. Provider disconnect and hosted behavior remain beyond these local checks. |
| Cache isolation and accounting | `internal/gateway/run_service.go` (`ownerCacheStore`), `internal/postgres/cache.go`, `internal/cachekey/cache.go`, `internal/runtime/cache.go`, `internal/runtime/execute.go`, `internal/runtime/recovery_execute.go`, `client_cache_test.go` | SQL cache operations carry owner and version; operation hashes reject credential-like semantic headers; replay validates producer/target, output, search, and accounting; provider and result accounting stay distinct. G-RUNTIME and managed INTEGRATION passed, including `TestCacheConcurrency` and cache replay/admission cases. | No cross-owner read/delete, stale-admission, or accounting defect found in reviewed paths. The SQL concurrency test covers the configured Postgres boundary; no production workload was inspected. |
| Artifact publication, deletion, and convergence | `internal/gateway/artifact_coordinator.go`, `internal/postgres/artifact_lifecycle.go`, `internal/artifacts/garage.go`, `internal/gateway/command/artifact_inventory.go` | Journaled publication and deletion are idempotent; the existing 30-second grace is retained; owner scope is applied to object operations; reconciliation verifies canonical metadata and inventory reports truncation/failure. Managed INTEGRATION passed, including `TestArtifactCoordinatorCrashConvergence`, `TestGarageArtifactStore`, and `TestArtifactInventoryAudit`. | No crash-convergence or ownership defect found. No destructive production cleanup or production Garage mutation was run. |

### Verification and trouble record

- `go test ./internal/gateway/auth ./internal/profiles ./internal/gateway/httpapi ./internal/gateway -count=1` — passed.
- `PATH=/home/kirill/.local/elixir-1.20.2/bin:/home/kirill/.local/otp-28.4.3/bin:$PATH mix test test/harden_llm_web/live/profiles_live_test.exs --seed 104729` — 15 passed.
- `go test . ./internal/runtime ./internal/retry ./internal/cachekey -count=1` — passed.
- `go test ./internal/gateway/httpapi -run '^TestSSE' -count=1 -timeout=60s` — passed.
- `node --test scripts/test/run_progress_test.mjs` — 4 passed.
- `go test ./internal/diagnostics ./internal/providers -count=1` — passed.
- `go test -race ./internal/runtime ./internal/gateway/httpapi -count=1` — passed.
- `make test-integration` — passed on the managed runner in 59.2 seconds; all package tests passed and the runner reported no cleanup errors or warnings. This includes the updated TEST-022 credential-preservation assertion.
- `PATH=/home/kirill/.local/elixir-1.20.2/bin:/home/kirill/.local/otp-28.4.3/bin:$PATH make test-fast` — all 10 tasks passed. Runner report: `tmp/test-feedback/runner-1790123518326-3801368-1d3470aa955088db.json`; cleanup errors/warnings: zero.
- A direct tagged `go test` invocation was first attempted without the runner-owned `HARDEN_LLM_TEST_POSTGRES_ENDPOINT`; it stopped before tests executed and is not counted as test evidence. The supported `make test-integration` runner was used.
- The first managed integration attempt could not create its Compose network because Docker reported `all predefined address pools have been fully subnetted`. Inspection found an empty, week-old network with the exact `harden-llm-test` Compose project label and zero attached containers. Removed only `harden-llm-test-exclusive-b422909e3691_default`; no container, volume, or application data was removed. The managed lane then passed. Host follow-up: review Docker default address-pool capacity and stale empty test-network cleanup if this recurs; do not restart Docker or broadly prune resources as a test workaround.
- During initial assertion development, the LiveView test briefly expected the credential ID from a closed drawer. The rendered form intentionally omits that field; the regression now asserts only that a canceled secret is absent from the save request. Backend preservation is separately asserted by TEST-022 against real Postgres.

### Outcome and open production prerequisite

No confirmed serious code defect was found in the five reviewed boundaries, and P02 can close. The review is limited to the source paths and named local cases above. Do not infer a whole-product security certification.

P01 production delivery is still not eligible to apply: the repository's production-upgrade procedure requires a tested off-host restore, and no approved target/procedure has been identified in this checkout or the inspected host records. The user was asked which approved restore target to use and shown the available choices. Continue independent code reduction; keep production application/deployment and F-001 closure pending that answer and completed restore evidence.
