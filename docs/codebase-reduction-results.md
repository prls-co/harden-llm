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

### Outcome and production handoff

No confirmed serious code defect was found in the five reviewed boundaries, and P02 can close. The review is limited to the source paths and named local cases above. Do not infer a whole-product security certification.

The user explicitly declined pre-upgrade backups and restore rehearsals,
accepting persistent-data loss. The deployed-to-candidate comparison
(`6887fcd` to `cf14628`) has no application migration, OpenAPI, or Postgres
storage-code change. No backup destination, snapshot system, or restore host is
needed for this code release. Langfuse receives gateway observability traces;
HardLLM's consumer history remains stored in Postgres/Garage and would be lost
if those data stores are lost. Langfuse does not restore the widget history.

## P01.1 — Exact source and certification review (2026-09-23)

The original web fix is present in
`40d19dc5bb5962e5e1c31905e4dbd5dc69fa7667` and its formatting follow-up
`2106948150513bac272c70f0c95487a7b61414b8`. Both are ancestors of the earlier
certified candidate `6887fcd8146961dc64598dd7a236e7a9fc522c9c`, which is also an
ancestor of the current `main`. Exact run
[35726391122](https://github.com/prls-co/harden-llm/actions/runs/35726391122),
attempt 1, used that candidate on `main`; the browser-free `make test-release`
job succeeded and emitted `accepted=true`, 28 tasks, no failure, and no cleanup
errors or warnings. Its runner JSON remained on the hosted runner; the run log
records path `runner-1790080351197-2539-54823f5c30851e92.json`. The separate
publisher artifact is not used as frontend evidence, and GHCR is retired from
the current image path.

The selected full-tree source for delivery is the later
`bafa62255d0a5b111253b3baf18d27d0091161b4`, because it includes the web fix and
the completed code reduction. It passed hosted FAST run
[35821407815](https://github.com/prls-co/harden-llm/actions/runs/35821407815)
and exact-source browser-free release run
[35821617071](https://github.com/prls-co/harden-llm/actions/runs/35821617071).
The latter accepted all 28 tasks and its separate integration and
integration-race selectors. Browser tests remain excluded by repository
policy. The source was promoted by fast-forward on 2026-09-23: `bafa622` is
the application source ancestor of documentation-only main commit
`e988207a939d28087b888adddf69e2273aa63a5e`. The main push then passed hosted
FAST run [35823822733](https://github.com/prls-co/harden-llm/actions/runs/35823822733)
at exact documentation tip `e988207`; all 10 tasks were accepted with no
failure or cleanup issue. Its artifact
`harden-llm-runner-reports-35823822733-1-fast` has digest
`sha256:b9152679c520ebca286ab3fac776d044bdc16ba529d4a6c8e3cb0e76633f29d5`, and
report `runner-1790142548649-2631-357b099f8eaf18f8.json` has SHA-256
`a5af8a46c02924e1bd244ecab5997ea8c46fab0b424efb8b7235c8775e19fb31`.

The complete web diff from running web release
`3201fd249f86031292be1c47acf64e0eb8a4540b` to candidate `6887fcd` changes only
`frontend/lib/harden_llm_web/live/profile_widget_component.ex` and its focused
test file; `frontend/Dockerfile` and `frontend/mix.lock` are unchanged. The
recovery policy and routes are present in the candidate's OpenAPI contract,
and the deployed gateway source is that same `6887fcd` release. The additional
P04 production changes at `bafa622` include the profile widget and the Go
runtime snapshot helper; public OpenAPI and `internal/gateway` source are
unchanged relative to `6887fcd`. However, `go list -deps ./cmd/harden-llm-gateway`
confirms the gateway binary transitively includes `internal/runtime` through
the root package. Both web and gateway images therefore need the candidate
release. The current production release identities and rollback images are
recorded in the P00 table above.

P01.1's exact-source, certification, and compatibility review is complete, and
the candidate is promoted to `main`. On 2026-09-23, the user explicitly
declined pre-upgrade backups and restore rehearsals, accepting loss of
persistent data. The exact deployed-to-candidate comparison (`6887fcd` to
`cf14628`) shows no application migration, OpenAPI, or Postgres storage-code
change; the earlier migration-based backup gate did not apply to this release.
No dedicated snapshot system or restore host will be provisioned.

HardLLM's consumer history/trace API reads product trace and run records from
Postgres and artifact bodies from Garage. Langfuse receives gateway
observability traces, but the consumer widget does not read its data. A normal
code deployment leaves the current history store in place; a database/object
store loss will also lose the widget history, even if Langfuse retains its
telemetry traces. Profile export/import is separate and does not export run
history. No production image was built and no descriptor or running service
was changed.

## P03 — Frozen size and context baseline

### P03.1 Source and method

- `refactor_base`: `81206faa09d3da2289d956716475e20cccb9bd0a` (the clean P02 checkpoint).
- The baseline was measured from that committed snapshot before this measurement manifest was added. Tracked paths were read with `git ls-files -z`; each UTF-8 file has a raw-byte count, `str.splitlines()` physical-line count, nonblank-line count, and SHA-256. Binary files contribute exact bytes and a binary label.
- The path manifest is [codebase-reduction-manifest.json](codebase-reduction-manifest.json). SHA-256: `a5894afa72340d7dbcfe75bb6958f422bc6449211006d851fe294f0cea8e0634`. It classifies 501 tracked files once across application, tooling, tests, contracts/configuration, and documents. It explicitly excludes only vendored `frontend/assets/vendor/heroicons.js` and `frontend/assets/vendor/topbar.js` from maintained totals. The later manifest itself is measurement metadata and is excluded from before/after totals; its 29,215 bytes / 595 lines are reported separately.
- The dependency-free measurement helper is in ignored scratch at `tmp/codebase-reduction/measure.py`, SHA-256 `347c9bb4c24218eda09f594a57f665919678ef25ffcc89f302bf4a65c92b01f8`; it is 12,880 bytes / 298 lines and is not shipped. The complete per-file baseline is `tmp/codebase-reduction/baseline-81206fa.json`, SHA-256 `589a6894673ccf668d1cd7f6870b59cce2a4b3129f35bb34972a8207e11e3755`.
- The pinned environment has no offline `tiktoken` installation. Token counts are unmeasured; bytes are a reproducible proxy, not an exact GPT-5.6 Luna context estimate. No tokenizer dependency or API call was added.

### P03.1 Baseline totals

| Category | Files | Bytes | Physical lines | Nonblank lines | Binary files |
| --- | ---: | ---: | ---: | ---: | ---: |
| Application | 131 | 1,317,088 | 38,084 | 34,768 | 1 |
| Tooling | 33 | 463,193 | 10,688 | 10,020 | 0 |
| Tests and fixtures | 182 | 1,659,176 | 40,598 | 37,007 | 0 |
| Contracts/configuration | 66 | 301,023 | 7,317 | 7,082 | 0 |
| Documents and archived evidence | 89 | 2,599,693 | 33,153 | 28,421 | 1 |

### P03.2 Representative context sets

The exact relative paths and their dependency notes are frozen in the manifest.
After-snapshots must add any new helper, contract, or test file used by the same
maintenance task.

| Maintenance task | Files | Bytes | Physical lines | Nonblank lines | Source/tooling/tests bytes |
| --- | ---: | ---: | ---: | ---: | --- |
| Profile option or recovery control | 12 | 252,359 | 7,253 | 6,331 | 184,354 / 0 / 68,005 |
| Workspace draft or history behavior | 20 | 506,057 | 14,792 | 12,724 | 336,629 / 0 / 169,428 |
| Runtime progress or accounting | 19 | 278,837 | 6,806 | 6,382 | 132,174 / 2,906 / 143,757 |

Context totals are whole-file byte proxies. Overlapping files across tasks are
counted in each task context because each set models an independent maintenance
request.

### P03.3 Candidate deletion inventory

Inventory was completed against `refactor_base` before source edits. Existing
behavioral assertions are named below; each selected task will add only the
missing observable contract before changing its owner.

| Candidate / owner / exact repeated or unreachable symbols | Required behavior and intentional differences | Existing callers/tests / uncovered invariant | Proposed replacement / old code to delete | Expected size direction / dependency overhead | Focused check / distinct boundary | Disposition |
| --- | --- | --- | --- | --- | --- | --- |
| P04.1 `ProfileWidgetComponent.profile_editor/1`: four `.input` blocks for `maxTokens`, `temperature`, `topP`, `topK`; three capability checkbox blocks | Preserve field ID/name/label/order, number type, `min=0`, only decimal `temperature`/`topP` having `step=any`, placeholders, `profile-draft-change` target, and core checkbox hidden-false plus checked-true pair. Leave the saved-profile `supportsWebSearch` hidden control and all credential/recovery controls alone. | WEB-TEST-037 opens the editor and checks all four numeric IDs/placeholders. Before P04.1, no case asserted the exact matrix, ordering, number constraints/event target, or each hidden checkbox pair; WEB-TEST-044 and WEB-TEST-090 now cover the shared editor in profile-definition, workspace, and nested recovery render paths. | Fixed atom-keyed numeric and checkbox descriptors with local `:for` loops; remove duplicated control markup only. | Expected decrease: four numeric structures and three checkbox structures contain repeated HEEx/attrs. Descriptor literals/helper markup are added; measure combined component and any helper after implementation. No dependency. | F-WIDGET; F-WORKSPACE if the shared widget rendering changes. | Selected. |
| P04.2 `ProfileWidgetComponent.profile_editor/1` and `models_for/5`: the same model list expression is evaluated for combobox options, definition datalist, and displayed count | Preserve host catalog precedence, default/profile values, ordering, custom current-model retention, and count. Recompute from current assigns on every render; no stateful cache. | `ProfileWidgetState.model_options/3` tests ordering/default/current inclusion; WEB-TEST-037 and WEB-TEST-043 cover their existing boundaries. WEB-TEST-044 now binds all three rendered consumers to the same ordered IDs across selected profiles, host-catalog changes, custom current models, and separate editor renders. | Compute one local `editor_model_options` value from current assigns and use it for all three consumers; remove repeated `models_for/5` argument blocks. | Production owner decreased 484 bytes / 22 lines. The required rendered matrix added 2,424 test bytes / 68 lines, so the frozen profile context grew 1,940 bytes / 46 lines in this phase. No dependency or persistent state. | F-WIDGET; component/profile-definition suites passed 31 tests. Full FAST needs a clean-host retry after unrelated runner-contract resource failures canceled the frontend task. | Implemented and retained for simpler single-source rendering; it does not reduce the complete context set on its own. |
| P04.3 `ProfileWidgetComponent.assign_fold_state/2` and `boolean_assign/3`: six ordered socket assignments | Explicit booleans including `false` replace current values; absent, nil, or invalid input preserves them. Keep the existing assignment order, `fold_disabled`, separate target-config ownership, and event/notification behavior. | WEB-TEST-044 now directly calls the LiveComponent update callback twice: full initial booleans, then a partial update with false, nil, invalid, and omitted fields. It checks retained values and the separate target config map. | A fixed ordered incoming-key/socket-key mapping reduced through one assignment function; the old repeated calls and `boolean_assign/3` were removed. | Production owner decreased 418 bytes / 17 lines. The required direct update regression added 1,407 test bytes / 46 lines, so the frozen profile context grew 989 bytes / 29 lines in this phase. No dependency. | F-WIDGET and embedding/profile-definition suites passed 34 tests. Full FAST remains open after the P04.2 runner-contract contention failure. | Implemented; exact partial-update semantics are covered, but this phase also increases the full context set. |
| P04.4 `runtime.Execute` in `internal/runtime/execute.go` and `executeRecoveryPlan` in `internal/runtime/recovery_execute.go`: duplicated progress snapshot field construction | Preserve per-call sequence/callback guards, deep-enough attempt/accounting/timeout copies, live stream counter addition, origin, deadline facts, and every `config.Now()` observation order. Preserve simple-path `run.started` before preflight, explicit-path `run.started` only once work is prepared, and omitted events when explicit work is nil. | No `ProgressSnapshot` callback tests existed in `internal/runtime`; provider normalization covers only `PreparedOperation.StreamProgress`. G-STREAM/N-PROGRESS cover different HTTP/public boundaries. Explicit recovery has distinct stage/branch/profile/reasoning identity and early returns before a work-backed terminal event. | Added TEST-284 public `Execute` callback assertions, then extracted only snapshot construction to a private helper, keeping sequence, guards, active stream, event transitions, and clock sampling with each caller. | Production owners decreased by 1,005 bytes / 3 lines; TEST-284 added 20,459 bytes / 433 lines. The fixed runtime context grew 19,454 bytes. No dependency. | TEST-284, full runtime package, focused runtime race, N-PROGRESS, and accepted FAST all passed. | Implemented with exact event differences preserved; production source is smaller, but the complete runtime task context is larger. Do not count as a context-size win. |
| P04.5 root `client.go`: public attempt and accounting-ledger conversions in progress and final result | Preserve empty attempts, optional progress accounting, copy behavior, wire field names, and progress nil-origin vs final zero-value origin semantics. | `publicAttempt` and `publicAccountingLedger` are already shared by both projections. `publicOrigin` is used by progress while final results inline the same eight-field mapping; this is the only material duplicate found in the selected symbols. | Compare a value-returning private origin mapper plus pointer wrapper against the current inline mapper and `publicOrigin`; retain only if exact tests and the combined helper/call sites are smaller. | At most a small source decrease; no dependency. Test additions may outweigh production deletion in total-maintained-code terms. | Root client tests, G-RUNTIME, and FAST wire/static checks. | Rejected: the shared attempt/accounting conversions already eliminate the major repetition; origin-only factoring has too little projected saving for the extra abstraction and test surface. |
| P04.6 only a specific proven unreachable private symbol/dependency | Keep all exported APIs, runtime compatibility paths, migrations, provider branches, HEEx callbacks/hooks, build-tag paths, fixtures, and harness entrypoints. | No dead private symbol has been identified in the inspected P04.1–P04.4 owners. `staticcheck` is not installed in the pinned environment; no browser/provider-only reachability claim is inferred. | No deletion until a concrete candidate has complete static and dynamic-boundary evidence. | No justified size direction; broad automated deletion would risk supported entrypoints and does not establish reachability. | Owner-specific tests and FAST; broader boundary only if an exact candidate requires it. | Rejected: no proven unreachable residue is in the inventory. |

### P03.4 Focused-context re-review

The P04.4 measurement missed CR-A04. Preserve its frozen measurements. The
runtime context grew 19,454 bytes after TEST-284 added a 433-line callback
contract to `internal/runtime/repair_test.go`; the production owners together
shrank 1,005 bytes. A bounded follow-up candidate is to put TEST-284 and its
fixture/assertion helpers in `internal/runtime/progress_snapshot_test.go`.
This changes test ownership only. It is selected only as a task-context
organization candidate: the assertion oracle, test count, and whole-repository
size claim must remain honest, and the focused progress context must show a
measured decrease after its helper dependencies are included. If it does not,
revert and record the evidence.

## P04 — Bounded reductions

### P04.1 Profile option markup

- **Change:** replaced the four numeric option blocks and three capability
  checkbox blocks in `ProfileWidgetComponent.profile_editor/1` with fixed
  atom-keyed descriptors and local loops. `nil` `step` remains absent, decimal
  inputs retain `step="any"`, and the existing separate hidden web-search
  control remains. No general form engine or dependency was added.
- **Behavioral coverage:** added a descriptor matrix against the profile
  definition renderer; the existing workspace and nested rerun target tests
  apply the same matrix to those contexts. The assertions cover exact names,
  order, labels, placeholders, type/min/step, `profile-draft-change` targets,
  and checkbox hidden-false/checked-true pairs.
- **Focused evidence:** pinned `mix format --check-formatted` passed for the
  changed Elixir files. `mix test` for profiles, profile widget, and embedding
  LiveViews passed 32 tests with seed `104729`.
- **FAST evidence:** the first full run (`runner-1790125567550-368569-3f367c1d1070825e.json`)
  stopped on runner-contract `TEST-271`: its fake Docker identity command
  returned no identity before resource creation. No assertion was changed.
  The exact test passed in isolation with
  `node --test --test-name-pattern='TEST-271 registers a private durable receipt before service creation' scripts/test/test_resource_lifecycle_test.mjs`
  (1/1). The host showed load
  average 51.24 at follow-up while a separate `go test -race ./...` process was
  active; this is a plausible contention cause, not a proven one. A fresh full
  `make test-fast` with the pinned Elixir/OTP PATH then passed all 10 tasks:
  `runner-1790126281405-601579-53661f5ec060ad77.json`, with no cleanup errors
  or warnings. Browser tests were not run.
- **Measured source:** `profile_widget_component.ex` changed from 120,426 bytes /
  3,400 lines to 119,218 bytes / 3,371 lines (-1,208 bytes / -29 lines). Its
  baseline SHA-256 was `29dcc7bf2bfed45ec65d0926e2d4660eacfb96157c9f7de81edfd723123df12f`;
  the post-change SHA-256 is `8d3e2fee07842db8930b6d1c88a63d7a9bcf416c1104aa00ab2a459096149b54`.
- **Measurement artifact:** canonical report for clean commit
  `747cb977eb063ed313e94864ad126505739120a3` is ignored at
  `tmp/codebase-reduction/after-p04.1.json`, SHA-256
  `b46c3cba0b5576028d9ebb9cabd330c0e4d1f2ef342be2816ca89e84c6be9939`.
- **Test/context cost:** `profile_widget_component_test.exs` grew by 3,464
  bytes / 99 lines. The frozen profile maintenance context therefore grew from
  252,359 bytes / 7,253 lines to 254,615 bytes / 7,323 lines (+2,256 bytes /
  +70 lines), including the necessary test coverage. Application bytes in that
  context fell by 1,208; test bytes rose by 3,464. This phase reduces production
  source but does not reduce the complete context set by itself.
- **Risks/follow-up:** verify that later selected profile-context reductions
  offset the measured test overhead before making a net context-size claim.
  The first FAST failure remains recorded as an unresolved transient Docker
  identity preflight, even though the isolated case and fresh complete gate
  passed.

### P04.2 Calculate the model list once per render

- **Change:** `profile_editor/1` calculates `editor_model_options` once from the
  current profile form, profiles, extra options, and host catalog. The combobox,
  profile-definition datalist, and displayed count use that same list. It is
  render-local; there is no cross-render cache.
- **Behavioral coverage:** WEB-TEST-044 checks exact ordered IDs and count in
  all three consumers for two selected profiles, a refreshed host catalog,
  retained custom current models, a stale extra option under host-catalog
  precedence, and separate renders with different catalogs.
- **Focused evidence:** pinned formatting completed. `mix test
  test/harden_llm_web/live/profile_widget_component_test.exs
  test/harden_llm_web/live/profiles_live_test.exs --seed 104729` passed 31
  tests without warnings.
- **Measured source:** `profile_widget_component.ex` fell from 119,218 bytes /
  3,371 lines after P04.1 to 118,734 bytes / 3,349 lines (-484 bytes / -22
  lines). Its hash changed from
  `8d3e2fee07842db8930b6d1c88a63d7a9bcf416c1104aa00ab2a459096149b54` to
  `fa107bb06ab0dee06882ee712d41a5b28b03efd15caf793b8e12c04a2c0c6d31`.
- **Measurement artifact:** canonical report for clean commit
  `68db5da2c640eea126c6b02958d70bf3e0e1061c` is ignored at
  `tmp/codebase-reduction/after-p04.2.json`, SHA-256
  `55c0e6cf5a7c1055146b86e8caee06e5f3607e59c2c047f98158911ed972e0d5`.
- **Test/context cost:** the component test grew from 39,652 to 42,076 bytes
  (+2,424) and from 1,070 to 1,138 lines (+68). The frozen profile context
  grew from 254,615 to 256,555 bytes (+1,940) and from 7,323 to 7,369 lines
  (+46). P04.2 reduces production source but is not a net context reduction.
- **FAST trouble:** `make test-fast` report
  `runner-1790127276857-928582-df8b1b601093fa7c.json` was not accepted. Go
  static, unit, parity, API, observability, and client-core tasks passed. The
  `runner-contracts` task had three failures among 46 Node tests: a synthetic
  dependency-ordering child exceeded its timeout; TEST-271 timed out waiting
  for the local Docker daemon lock after 32.5 seconds; and TEST-272's fallback
  cleanup case was rejected. The coordinator then canceled the deterministic
  frontend task with SIGTERM. The same focused frontend suites passed
  separately. Afterward `docker info` succeeded with daemon ID
  `2758d8cf-d2a7-4223-b620-75d23efa27d5`, Docker 29.1.3; host load averages
  were `10.99, 45.40, 47.17`. Contention is plausible but not proven. No test
  oracle was changed. A complete accepted FAST run is still required.
- **Risks/follow-up:** reassess net bytes across all P04 changes at P05 and
  report honestly if the selected work does not reduce the actual task
  context. A later accepted FAST run covers this change and P04.1–P04.3; see
  P04.4 for its exact report identity.

### P04.3 Reduce repeated fold assignments

- **Change:** replaced six repeated socket assignment blocks and
  `boolean_assign/3` with a fixed, ordered mapping of incoming attributes to
  socket keys and one reducer. Only boolean values are applied; missing, nil,
  and invalid values leave current state intact. No client-controlled key is
  converted to an atom.
- **Behavioral coverage:** the WEB-TEST-044 callback regression mounts the
  component, applies all six folds as true, then supplies explicit false values
  alongside nil, invalid, and missing values. It checks retained fields,
  deterministic false replacement, and that the separate target config map is
  untouched.
- **Focused evidence:** `mix test
  test/harden_llm_web/live/profile_widget_component_test.exs
  test/harden_llm_web/live/profiles_live_test.exs
  test/harden_llm_web/live/embedding_live_test.exs --seed 104729` passed 34
  tests without warnings.
- **Measured source:** `profile_widget_component.ex` fell from 118,734 bytes /
  3,349 lines after P04.2 to 118,316 bytes / 3,332 lines (-418 bytes / -17
  lines). Its hash changed from
  `fa107bb06ab0dee06882ee712d41a5b28b03efd15caf793b8e12c04a2c0c6d31` to
  `b1cd09e6758f341264fad4f60dc456298db566c066b04e9f25716a7b1cd1ccdf`.
- **Test/context cost:** the component test grew from 42,076 to 43,483 bytes
  (+1,407) and from 1,138 to 1,184 lines (+46). The frozen profile context
  grew from 256,555 to 257,544 bytes (+989) and from 7,369 to 7,398 lines
  (+29). This phase reduces production source but not the full context.
- **Risks/follow-up:** the final accepted FAST gate now covers the current
  source. Compare complete frozen contexts at P05 before claiming that this
  work met the context-reduction objective.

### P04.4 Progress callback equivalence and outcome

| Boundary | Simple `Execute` path | Explicit recovery path | Invariants for tests and extraction |
| --- | --- | --- | --- |
| No progress callback | Guard returns before sequence increment or callback. | Guard returns before sequence increment or callback. | No global sequence or observer; callbacks remain per call. |
| Preflight failure / no planned work | `run.started` is emitted before context, credentials, prepare, and cache checks; deferred `run.terminal` still follows. | `emit` ignores nil `work`; prepare failure or initial expired context can produce no snapshots, including no terminal. | Preserve intentionally different start and terminal boundaries. |
| Before first provider attempt | No extra pre-attempt progress event; `run.started` precedes preflight and cache lookup. | After planned work is ready, `run.started` precedes cache lookup and a `run.progress` event precedes each attempt. | Preserve event names, order, sequence, and active work identity. |
| Stream and completed attempt | Stream callback adds its current counters to accumulated diagnostics; completed attempt is appended before the next progress snapshot. | Same counter rule; stage, branch, profile, and reasoning come from active planned work. | Snapshot attempt slices, optional accounting, and timeout values are copies; stream counters do not alias record totals. |
| Cache hit | `run.started`, then deferred `run.terminal`; no attempts. | With prepared work, `run.started`, then deferred `run.terminal`; loop progress is skipped. | Preserve zero-attempt snapshots and cache event placement. |
| Deadline | Expired context after start emits terminal through the defer. | Expired context detected before `startedAt` and defer emits no event; later expiry after planned work emits terminal. | Keep path-specific boundary and exact `config.Now` sampling order for elapsed, activity, and remaining time. |
| Terminal failure | Deferred terminal is emitted after stop reason and diagnostics are finalized. | Deferred terminal is emitted only when planned work exists; otherwise nil-work guard suppresses it. | Keep sequence advancement, callback guards, transitions, scheduling, and error ownership in each loop. |

**Baseline coverage record:** public `Execute` callers had no direct
`ProgressSnapshot` callback assertions in `internal/runtime`. Existing provider
normalization covers `PreparedOperation.StreamProgress`; SSE and Node progress
tests cover later boundaries only. Do not infer callback parity from REST
serialization tests.

**Change and coverage:** introduced package-private
`buildProgressSnapshot` and replaced only the two duplicated snapshot field
builders. Both callers still own event guards, per-call sequence, active stream,
event identity, and callback timing. TEST-284 observes public `Execute`
callbacks for both paths, including their intentional preflight and nil-work
differences, stream and completed-attempt counters, cache hits, terminal
failure, deadline sampling, and copy isolation. The test is in
`internal/runtime/repair_test.go`; no production API or wire contract changed.

**Focused evidence:**

- `go test ./internal/runtime -run '^TestProgressSnapshotContracts$' -count=1` — passed.
- `go test ./internal/runtime -count=1` — passed.
- `go test . ./internal/runtime ./internal/retry ./internal/cachekey -count=1`
  (G-RUNTIME) — passed.
- `go test ./internal/gateway/httpapi -run '^TestSSE' -count=1 -timeout=60s`
  (G-STREAM) — passed.
- `node --test scripts/test/run_progress_test.mjs` — 4/4 passed.
- `go test -race ./internal/runtime ./internal/gateway/httpapi -count=1`
  (G-RACE) — both packages passed.
- Pinned-toolchain `make test-fast` — all 10 tasks passed, exit 0, with zero
  cleanup errors or warnings. Report:
  `tmp/test-feedback/runner-1790129522551-1696759-d18f83710c4b2523.json`,
  SHA-256 `e613f1e85cd9486c0facdac55be87afa361dbaf7a8046b6e6943d833130aa984`.
  It includes `go-static`, `runner-contracts`, `go-unit`, `go-parity`, `go-api`,
  `go-observability`, `frontend-deterministic`, `client-core`,
  `run-progress-client`, and `widget-traceability`.

**Measured size direction:** from the P04.3 source, the two production owners
fell by 1,005 bytes and 3 lines combined. TEST-284 added 20,459 bytes and 433
lines to `repair_test.go`. The frozen runtime progress/accounting context grew
from 278,837 to 298,291 bytes (+19,454): application source fell by 1,005
bytes, while tests grew by 20,459. This phase reduces production code but
increases the complete task context. CR-A04 cannot be claimed from this result.

**Risk / follow-up:** preserve the exact path differences documented above.
The sharing is a small production-source reduction and is not a context-size
win when its regression tests are included. P05 must report this as a tradeoff;
do not remove the regression coverage to manufacture a smaller context.

### P04.7 Progress test context isolation

**Selected after P03.4:** `TEST-284` has a dedicated, self-contained callback
contract but currently shares `repair_test.go` with retry-repair behavior. The
P03 baseline `repair_test.go` is 33,555 bytes; the P04.4 file is 54,014 bytes.
Move the TEST-284 test and fixtures into `progress_snapshot_test.go` without
removing test cases or assertions. Keep the original whole-context P03/P04.4
measurement unchanged. Separately measure the focused progress task using the
runtime production owners, baseline `repair_test.go` versus the dedicated
progress test plus any required helper files. The candidate is useful only if
that scoped byte total falls after dependencies are counted.

**Risks:** Go test helpers are package-wide, so a standalone test file can
silently depend on unrelated `repair_test.go`, recovery-plan, or telemetry
fixtures. Make those dependencies explicit and keep local fixture behavior
equivalent. Preserve TEST-284 and every subtest name/assertion. Re-run runtime,
race, FAST, and release gates on the final tree; the prior release gate only
certifies the P04.4 source layout.

**Implementation and context result:** moved the test and its support types to
`internal/runtime/progress_snapshot_test.go`. The test body is byte-for-byte
identical after mapping the two fixture names (`planProfiles` to
`progressProfiles`, `telemetryCache` to `progressContractCache`); all six named
subtests and their assertions remain. The new executor, profiles, usage, and
cache fixtures are local to the focused file, so it no longer depends on
`repair_test.go`, `recovery_plan_test.go`, or `telemetry_test.go`.

The separate focused progress-task measurement includes the eight shared
runtime/retry/accounting/cachekey source files plus the relevant test file. It
replaces the 33,555-byte baseline `repair_test.go` with the dedicated test file.
The measured set fell from 141,599 bytes / 3,629 lines to 128,789 bytes / 3,509
lines (-12,810 bytes / -120 lines) on source/test-layout commit `a843390`
(`a8433900952a281774c32761e7905b9b34f6b66f`). Raw measurement:
`tmp/codebase-reduction/focused-progress-after-p04.7.json`, SHA-256
`808f3e441d1a5f4d8c04f3e3781afd7580fa78e5327c73d905771ef98902098f`; the
reproducible measurement script is
`tmp/codebase-reduction/measure-focused-progress.py`. This is a narrower
progress-maintenance context result. The original frozen whole-runtime context
still grew and remains reported as such.

**Checks:** `gofmt`, the focused TEST-284 run, the full runtime package, and
`go test -race ./internal/runtime -count=1` passed. The pinned-toolchain
`make test-fast` attempt was rejected under shared-host load. Report
`tmp/test-feedback/runner-1790134603183-3436339-c8c1d8dfd90dc031.json`, SHA-256
`a047a8b79505ecbcfcc7a8ce08cdff75efecf34b0c12e12587da35e48c786b68`, records
a runner-contract test's fake child timing out after 62.2 seconds and `go-static`
timing out after 188.2 seconds; the runner then canceled `go-unit`, `go-parity`,
and remaining tasks. The observed host load was 245.37 / 155.46 / 70.82; cleanup
errors/warnings were zero. No assertion, timeout, or runner policy changed.
The isolated hosted FAST run also failed and is under diagnosis before P04.7
can close. Run 35815208694 at source SHA
`a8433900952a281774c32761e7905b9b34f6b66f` completed the 10-task FAST selector;
`frontend-deterministic` reported 245/247 passed, 5 excluded, and 2 failures.
The redacted artifact `harden-llm-runner-reports-35815208694-1-fast` has digest
`87729ccaac63c4d239be7f42f997d71cea24c53835a43a6f9a6924ef7e8e4950`; local
report `tmp/codebase-reduction-fast-35815208694/harden-llm-runner-reports-35815208694-1-fast/runner-1790134913998-2467-925e551546c9c7c9.json`
has SHA-256 `b145ad603cabf743b6f21e72407f5a732ea88ce80602957766ce95c45aaa8ac5`.
`HistoryTraceTest` failed waiting for the deliberately blocked page request's
start signal at its default 100 ms receive timeout. The affected blocked-request
handshakes in `history_trace_test.exs` and `workspace_live_test.exs` now use an
explicit 1,000 ms bound, matching their existing bounded process-release
helpers; no expected message, rendered state, or request-count assertion changed.
`WorkspaceLiveTest` also failed to find the refreshed history row after a
successful run; the captured HTML still showed the initial workspace loading
state. The focused test passed alone and the two relevant Phoenix modules passed
69/69 with `--max-cases 8` after the synchronization change, so that second
failure has not reproduced locally. Keep its hosted failure recorded and do not
claim the change fixed that symptom without another full hosted FAST result.
The hosted commit changed only the TEST-284 Go test layout; neither Phoenix
application code nor frontend tests changed in that commit. The earlier release
gate predates the test move.

**Post-change checks:** pinned-toolchain `mix format --check-formatted` passed.
The four affected history/run tests passed with seed `104729`, and the complete
`HistoryTraceTest` and `WorkspaceLiveTest` modules passed 69 tests with seed
`104729` and `--max-cases 8`. The updated tree then passed hosted FAST run
[35818091415](https://github.com/prls-co/harden-llm/actions/runs/35818091415)
at source SHA `a6a7bdd832d77644869feb0a0d27ef3061a5fa92`: all 10 tasks accepted,
no first failure, and zero cleanup errors or warnings. The frontend suite passed
247 tests with 5 excluded. Its redacted artifact
`harden-llm-runner-reports-35818091415-1-fast` has digest
`8d1e1f6c965ad1b6d22ae40bea9ac78e8f830d1396e329db00564e668ca33072`; report
`tmp/codebase-reduction-fast-35818091415/runner-1790137514745-2622-3a03e82a101450e3.json`
has SHA-256 `b1466daa58b23591482d4175c5467decf6dcf4144fcc8d60686845c4bbcae932`.
The separate stale-loading failure did not recur. It remains recorded as an
unexplained transient; this passing run does not establish its cause.

After the FAST pass, browser-free hosted release certification completed
successfully on source SHA `a6a7bdd832d77644869feb0a0d27ef3061a5fa92` in run
[35818416126](https://github.com/prls-co/harden-llm/actions/runs/35818416126).
The `release` selector accepted all 28 tasks; its separate `go-integration` and
`go-integration-race` selectors were also accepted. There was no first failure,
timeout, cleanup error, or cleanup warning. The browser-free frontend suite
passed 247 tests with 5 excluded. The redacted artifact
`harden-llm-runner-reports-35818416126-1-release` has digest
`sha256:c2d332d6202c0127535805b3e58692abd4610d8e2550a00bf0ec9a26b1006384`.
Release report `runner-1790138475446-2410-a321407d20a2c58e.json` has SHA-256
`bc97bc894998ef3b5a687296624b039c7c6e8b2495b634bf522e93a0dab8a935`; the
`go-integration` and `go-integration-race` reports have hashes
`9675f78439af7a3e952c2fd0f0ffbb59360556272b25e298e676bc4311dbc6fc` and
`699561cef6dd3de636d6f58ab92eb9a7765cd9de34f1941cbe47dc79acd307b6`.
This certifies the P04.7 tree only; P04.8 began afterward.

The receive-bound change is recorded in
[`ker/timeouts/rca/2026-09-22-test-liveview-request-start.json`](../ker/timeouts/rca/2026-09-22-test-liveview-request-start.json).
The hosted failure expired at 100 ms. Eleven positive request-start samples
from the local max-cases-8 run ranged from 0.001 to 0.593 ms; one additional
sample was below the timer's microsecond resolution and was excluded. The
explicit 1,000 ms bound therefore has measured local headroom, while the
separate stale-loading failure remains unexplained and must be observed on the
updated hosted run.


### P04.8 Profile editor contract context

The broad profile component test file had grown to 43,483 bytes. Three adjacent,
self-contained component tests were moved to
`frontend/test/harden_llm_web/live/profile_widget_editor_contract_test.exs`:
rendered numeric/capability controls, model consumers across fresh renders,
and partial parent fold assignments. The three test bodies, their editor-only
helpers, and the numeric assertion matrix were compared with the pre-change
`a6a7bdd` source. Test bodies and helper assertions are unchanged; the existing
`APIFixtures.profile_state/0` data is still the source for the shared `profile/2`
fixture. The assertion matrix now lives once in
`frontend/test/support/profile_widget_test_assertions.ex`. The extracted module
uses `ExUnit.Case, async: true` and no longer needs ConnCase or a Req stub.
The old and new component modules together still contain 17 test cases.

The focused task measurement includes the six production files from the frozen
profile owner set plus actual fixture, test-helper, and module dependencies. It
replaces the monolithic profile component suite and its host/test setup with the
focused contract module and its assertion helper. The measured context fell from
362,449 bytes / 10,551 lines / 14 files to 200,661 bytes / 5,814 lines / 10
files: -161,788 bytes and -4,737 lines. Raw data is
`tmp/codebase-reduction/focused-profile-editor-after-p04.8.json`, SHA-256
`43e20b9e07f58525559fedf07358cbf8500b46de5156195f82ecbdfdf8616bf2`; the
measurement script is `tmp/codebase-reduction/measure-focused-profile-editor.py`,
SHA-256 `6d47efe2cdc1423c48d0591a86e0d7166a67599d06b2f51e1c4edaa93e7d37f4`.

The frozen broad profile task was recomputed with both new files included. It
increased from 257,544 bytes / 7,398 lines / 12 files at `a6a7bdd` to 258,251
bytes / 7,417 lines / 14 files (+707 bytes / +19 lines). Raw data is
`tmp/codebase-reduction/profile-broad-after-p04.8.json`, SHA-256
`61cce754780d0c9d885beb7891262bc1283a5390be87e549d6981a6746fe3630`; its
script is `tmp/codebase-reduction/measure-profile-broad-after-p04.8.py`,
SHA-256 `235c2bf824190d5e628e040b9087ad983ac5464d2202d59983fd38ed74d3b17b`.
This phase improves the editor-specific task context but adds a small amount to
the broad context; it is not a total code-size reduction and does not satisfy
CR-A04.

**Checks:** pinned-toolchain `mix format --check-formatted` passed. The focused
component and editor modules passed 17/17 tests with seed `104729` and
`--max-cases 8`, with no warnings. A local full `make test-fast` run was not
accepted: after 311 seconds, `go-static`, `runner-contracts`, and `go-parity`
timed out; `go-unit` was terminated and the remaining tasks were canceled after
the first failure. At the failure, host load averages were 105.22 / 117.43 /
65.21. The report recorded zero cleanup errors and warnings, and no test
assertion failure. It is preserved at
`tmp/test-feedback/runner-1790139495362-676242-872bdd2a0cd902a7.json`, SHA-256
`167e653c918c48ddafb855e342cf0840040cf1d6df1a9a020b37635b9d491017`. This local
run is a failure, not a pass. The exact P04.8 source
`bafa62255d0a5b111253b3baf18d27d0091161b4` then passed hosted FAST run
[35821407815](https://github.com/prls-co/harden-llm/actions/runs/35821407815):
all 10 tasks accepted, no first failure, and zero cleanup errors or warnings.
The frontend suite passed 247 tests with 5 excluded. Artifact
`harden-llm-runner-reports-35821407815-1-fast` has digest
`sha256:dceeeeaf7c1d1ff70af5b8a59bf093d682176df8e147a5b6fda3e961d8ea4d7e`;
report `runner-1790140426802-2438-76307ac9c5d2bbdb.json` has SHA-256
`f1c4cecd2ead0c6bb5254c1625cfb95f8e6f8a3ba3f76699d4979458aef9f35c`.
Browser-free release run 35821617071 completed successfully on 2026-09-23.
Hosted [run 35821617071](https://github.com/prls-co/harden-llm/actions/runs/35821617071)
used exact source `bafa62255d0a5b111253b3baf18d27d0091161b4`. The browser-free
`make test-release` selector accepted all 28 tasks; the separate
`go-integration` and `go-integration-race` selectors were also accepted. There
were no first or preflight failures, timed-out tasks, cleanup errors, or cleanup
warnings. The frontend portion passed 247 tests with 5 excluded. Artifact
`harden-llm-runner-reports-35821617071-1-release` has GitHub digest
`sha256:600bfbbb4fb1fdf2ff5e80c66f6def3d13a7ce36640db9c1e677d5e6ff20fb0d`.
The release report `runner-1790141408827-2707-7cc51fd45bc7c097.json` has
SHA-256 `c3d8d20223cb79e625cdfd015b1fb1817df6e62127f13c093a47b4ad5bed0c8e`;
the `go-integration` report has SHA-256
`c2d5605550720da76224258db46890ac2ea8836ff52ece798ee6ff9cbb0d2dfa`, and the
`go-integration-race` report has SHA-256
`e9f5ee3379d77711bb1465cb9b711631882a05147d5c8da5562c2c07eb019ec8`. Browser
checks remain unrun by policy.

**Risk / aftercare:** the extracted module proves component rendering and direct
server-side update semantics. It does not prove endpoint/session routing,
LiveSocket behavior, or browser layout. Keep routed workspace/embed tests in the
original suite. For later changes to those host paths, run the profile component
suite and the affected workspace/embedding suite along with the focused editor
module.

### P04.9 Contracted schema owner and workspace context

The post-P04.8 review found the contracted JSON Schema shorthand conversion and
validator embedded in `WorkspaceLive`, separate from workspace draft and
History ownership. The new `HardenLlmWeb.WorkspaceSchema` module owns
shorthand conversion, contracted-subset validation, keyword/type tables, and
schema-specific diagnostics. `WorkspaceLive` retains schema decoding for
workspace state, status presentation, event handling, persistence, and Run
lifecycle. This keeps draft restore/save independent of the new validator.

`WEB-TEST-057` and the existing invalid-local-JSON LiveView test moved to
`frontend/test/harden_llm_web/live/workspace_schema_live_test.exs`. Their test
bodies and assertions are unchanged; only the stub helper name and minimal
module-local fixture setup changed. The canonical frontend test spec now points
WEB-TEST-057 to the focused module. The combined workspace plus schema command
passed 57 tests after the final state-decoder ownership adjustment; the
canonical schema-only command passed 2 tests. The pinned-toolchain formatter
check passed after the final ownership adjustment.

The workspace LiveView fell from 79,167 bytes / 2,427 lines to 69,655 bytes /
2,132 lines (-9,512 bytes / -295 lines), with a new 9,651-byte / 308-line
schema module. The application source in these files therefore grows by 139
bytes / 13 lines. The general workspace test file fell from 115,192 bytes /
3,323 lines to 112,205 bytes / 3,216 lines (-2,987 bytes / -107 lines), while
the focused schema test file adds 4,488 bytes / 158 lines. These changed source
and test files grow by 1,640 bytes / 64 lines combined. This phase improves
task boundaries; it is not a whole-repository byte reduction.

For the frozen draft/history task, the schema helper module and focused
schema-only test module are excluded: state decoding, draft persistence, and
History lifecycle stay in `WorkspaceLive`, and the moved LiveView assertions
cover schema editing and Run gating as a separate maintenance concern. With
that dependency rationale, the workspace context fell from 506,057 bytes /
14,792 lines to 491,960 bytes / 14,336 lines (-14,097 bytes / -456 lines).
The profile context remains +5,892 bytes and the runtime context +20,745 bytes
from P03 because of required regression coverage. The full context artifact is
`tmp/codebase-reduction/contexts-after-p04.9.json`, SHA-256
`7f797a42164e2bc5f87fc7f17e9d022ea807933db763085e6a1154b48d7b84bf`; the P03
manifest and baseline remain unchanged.
CR-A04 is still not met.

**Checks:** the original `mix test
test/harden_llm_web/live/workspace_live_test.exs` baseline passed 57 tests. The
final combined command
`mix test test/harden_llm_web/live/workspace_schema_live_test.exs
test/harden_llm_web/live/workspace_live_test.exs` passed 57 tests. The final
`mix format --check-formatted` and `git diff --check` passed.

A local `make test-fast` attempt on the first P04.9 candidate was rejected; it
is not acceptance evidence for the final tree. Redacted report
`tmp/test-feedback/runner-1790167851782-145909-aa51595e868c041e.json`, SHA-256
`785e2eced0e1078abd20a7b00e3828696c92f6856c503a686b15564e9fe01bfd`, records
`runner-contracts` failure/timeout after 181,144 ms. Its Node child tests had
runner cancellation/abort timing failures and TEST-271 receipt cleanup could
not establish the fake daemon identity; the coordinator then canceled
`frontend-deterministic` with SIGTERM after 119,568 ms. The other eight fast
tasks passed. During the run, a separate long `go test -p=1
./internal/watch -run '^TestWatchMemoryEnvelope$'` was active; immediately after
the run, host load was 608.57 / 385.57 / 164.38. This strongly suggests shared
host contention but does not prove it caused every failure. No runner test was
changed. A second full FAST attempt and its outcome are recorded below.
Browser/provider checks remain unrun by policy.

A second full P04.9 `make test-fast` attempt also failed and is not acceptance
evidence. Report
`tmp/test-feedback/runner-1790169118728-533565-858a8c432beb71c4.json`, SHA-256
`1ca40eaf47f8f56c1aaf2bcf76d381bfa034fe9e7e4fea433382f2c97019a304`, records
9 of 10 FAST tasks passing. `runner-contracts` passed 45 of 46 cases; TEST-273's
nested managed-runner case timed out its child after 12,725 ms and then failed
the unchanged `result.accepted` assertion. Cleanup also reported `ENOTEMPTY`
while removing the canceled frontend Mix build's Hackney directory. At 06:14
PT just after the run, load averages were 16.99 / 32.92 / 67.28 and a separate
clustering 10,000-item resumed-pipeline capacity cohort was active. This is
evidence of shared-host activity, not proof that it caused the nested timeout.
The prior accepted local FAST report
`tmp/test-feedback/runner-1790123518326-3801368-1d3470aa955088db.json` passed
the same nested case in 5,515 ms, below its unchanged 10-second task deadline.
No test assertion, timeout, or runner behavior was changed. Do not retry while
that workload is active; rerun full FAST after a stable idle window, then
require hosted FAST and browser-free release success on the exact final source.
The 06:21 PT host-readiness recheck still showed 12 available CPUs, load
averages 21.51 / 22.20 / 48.53, CPU PSI `some avg10=15.64`, only 2.8 GiB
available memory, and all 8 GiB of swap in use. The clustering cohort was still
running. Wait for that activity to clear and host pressure to settle before the
next full run; do not kill or restart that separate workload.
At the 06:43 PT recheck, load had climbed to 429.97 / 509.18 / 367.27. A
read-only process sample showed the clustering cohort alongside unrelated
dataset-host test-worker and service-probe activity, including I/O-waiting
processes. No single cause is established; this is not a safe verification
window.
The exact P04.9 commit `cf14628eef80312fe0b03fefd3bed2d3e301860c` then passed
hosted FAST run [35870390854](https://github.com/prls-co/harden-llm/actions/runs/35870390854)
on the isolated feature branch in 2m17s. Browser-free RELEASE run
[35870717879](https://github.com/prls-co/harden-llm/actions/runs/35870717879)
also completed successfully on that same source; its `make test-release` step
ran for 12m42s. Redacted report artifacts were uploaded by the workflows:

- FAST report
  `tmp/test-feedback/hosted-p04.9/runner-1790171831961-2645-f775719601ca3f16.json`:
  accepted, all 10 tasks passed; SHA-256
  `12ad352bb511700d0841bd40e6b886b8c175a5b558d99f64242ea0f15e14584c`.
- RELEASE report
  `tmp/test-feedback/hosted-p04.9/runner-1790172663629-2475-1551ef444807cfde.json`:
  accepted, all 28 tasks passed; SHA-256
  `24490f0c3262a513854ef0217844d2ba2cf5974c40cbf8ef7a6f12b0b80517d2`.
- INTEGRATION report
  `tmp/test-feedback/hosted-p04.9/runner-1790172568505-56196-3730f1a0dbc090ac.json`
  and INTEGRATION-RACE report
  `tmp/test-feedback/hosted-p04.9/runner-1790172613727-58687-2efcd1494d3b1f79.json`:
  each accepted its single task; SHA-256 values are respectively
  `2916ceaf68b06a340bde5b18c4992c9220906377c96cb25220875be9edddcda3` and
  `82bb3f977c0a2a03c48a77fc293d93c638050913573b3f93f55632dd609bfb85`.

These are hosted validations; the two failed local runs remain recorded above
and were not reclassified as passes. Browser tests and deployment have not run.

## P05 — Final verification and measured outcome

### P05.1 Applicable gates and complete diff

- The final refactor application source is
  `bafa62255d0a5b111253b3baf18d27d0091161b4`, now on `main` and
  `feat/codebase-reduction`; documentation-only commit
  `e988207a939d28087b888adddf69e2273aa63a5e` is the current promotion tip. The
  full diff from the P03
  refactor baseline is limited to the frozen size manifest, the result ledger,
  the profile widget and its tests, the two runtime execution owners and their
  callback regression, the plan/test catalog records, and test-tier
  registration. No public Go export, OpenAPI, stored schema, migration,
  provider behavior, or browser hook changed.
- `git diff --check`, Go formatting, and the test-tier JSON parser passed.
- The accepted FAST report recorded under P04.4 covers all 10 T0–T2 tasks for
  the P04.4 source. G-RUNTIME, G-STREAM, N-PROGRESS, and G-RACE also passed on
  that source. Later P04.7 and P04.8 source changes have since passed hosted
  FAST and browser-free release gates as recorded below.
- The required browser-free `make test-release` attempt was not accepted. Report
  `tmp/test-feedback/runner-1790130239806-1960763-3097fd512a71ed97.json`,
  SHA-256 `d5f5f6c68d26a0a83579ef737b3f5dc7bfdf6f12fa11db0456109b993d0caf7e`,
  records one causal `runner-contracts` failure: TEST-272 cleanup-reporting and
  process-group cases plus TEST-273 same-daemon serialization failed while
  their child-process/event handling used 5-second bounds; an observed host
  load sample reached 110.51. The
  manager then canceled the race and frontend compile tasks with SIGTERM and
  canceled later tasks. `go-integration`, `go-unit`, `go-api`, and other
  completed tasks passed; cleanup errors/warnings were zero. The release gate
  remains incomplete. No test assertion, timeout, or worker policy was changed.
- A second diagnostic full selector run used the runner's explicit
  `--candidate-slots 1` option:
  `node scripts/run-test-tier.mjs --task release --candidate-slots 1`.
  Report `tmp/test-feedback/runner-1790131042644-2197525-72d1bc0350c5fcad.json`,
  SHA-256 `941b9ce43f300579685bc59268bb25c5db44dbe2c346dadd437c6c87e24f70ba`.
  It was also rejected. `runner-contracts` passed in 89.5 seconds and ordinary
  integration passed, but `go-integration-race` failed in
  `TestResourceRoutes` at `internal/gateway/resource_routes_test.go:256` when
  `GarageStore.Put`'s existence `HEAD` request timed out. The runner canceled
  `go-race` and later tasks; cleanup errors/warnings were zero. During that run,
  `go-integration-race` (resource class `service`) and `go-race` (`cpu`) ran at
  the same time, and the observed one-minute host load sample reached 248.20.
  `--candidate-slots 1` limits CPU-class tasks only, so it does not prevent this
  cross-class overlap. This run does not distinguish Garage behavior under
  load from a Garage defect; the focused integration-race check has not yet
  been repeated without the concurrent Go race task.
- The isolated hosted browser-free release workflow then passed on
  [`1b75728`](https://github.com/prls-co/harden-llm/commit/1b75728b374fef0dd664d8e66eb16af2479ef783)
  (source code is unchanged from `c8d33d5`; the intervening commit contains
  documentation only). Run [35811904032](https://github.com/prls-co/harden-llm/actions/runs/35811904032)
  selected `suite=release`; its `browser-free release` job completed
  `make test-release` with all 28 tasks accepted, exit status zero, no
  timeouts, no first failure, and zero cleanup errors/warnings. The separately
  emitted `go-integration` and `go-integration-race` selector reports were also
  accepted, including the Garage-backed route test that timed out locally.
  The redacted artifact `harden-llm-runner-reports-35811904032-1-release` has
  GitHub SHA-256 digest
  `5faafecbb5b737d4ab0d5fe9d63682ca6b3fa364392b2f2d256d0cc066053803`; its
  release report is
  `runner-1790132693667-2625-9c91943a20aa5825.json`, SHA-256
  `0b3733892d59a196b73f551defc5134344f221a9cd7e976f3625fa479957c96d`. The
  integration reports are recorded beside it with hashes
  `65260b4e483c9858dd49ce3bf9a16b65ff0490bc21ab520bf0a257a86c3e6921` and
  `b1b5e59f30573ceee3cdc15a6c45ced42e813b9d137eeb2e42bb6b8da5ba164b`.
- Pushing the P04.7 test-layout commit triggered hosted FAST run
  [35815208694](https://github.com/prls-co/harden-llm/actions/runs/35815208694)
  at source SHA `a8433900952a281774c32761e7905b9b34f6b66f`. The selector ran all
  10 tasks, but `frontend-deterministic` failed 2 of 247 included tests
  (`HistoryTraceTest` and `WorkspaceLiveTest`; 5 excluded). The detailed failure
  evidence, artifact/report hashes, and unresolved diagnosis are recorded under
  P04.7 above. At that point the P04.7 test layout and final verification were still open;
  the subsequent FAST and release runs below closed the P04.7 gates.
- The updated tree then passed hosted FAST run
  [35818091415](https://github.com/prls-co/harden-llm/actions/runs/35818091415)
  at `a6a7bdd832d77644869feb0a0d27ef3061a5fa92`. All 10 tasks were accepted,
  including 247 frontend tests with 5 excluded, and there were no cleanup
  errors or warnings. The artifact and report hashes are recorded under P04.7.
  The separate stale-loading failure did not recur; its cause remains unknown.
- Browser-free hosted release certification completed on the P04.7 tree in
  [run 35818416126](https://github.com/prls-co/harden-llm/actions/runs/35818416126)
  at `a6a7bdd`; the accepted 28-task result and report hashes are recorded under
  P04.7 above. At that point P05.1 remained open because P04.8 subsequently
  changed tests and test support; the P04.8 gates now pass as recorded below.
- The hosted release pass closes P04.7 and does not retroactively pass any
  local failures. Preserve all failed local reports in the record. Do not change
  TEST-272/273 timing assertions, TEST-279 resource policy, or Garage timeouts
  based on those contaminated local runs. The hosted selector explicitly
  skipped browser and live-provider checks.
- The final P04.8 source passed hosted FAST run
  [35821407815](https://github.com/prls-co/harden-llm/actions/runs/35821407815)
  and browser-free release run
  [35821617071](https://github.com/prls-co/harden-llm/actions/runs/35821617071).
  The latter accepted all 28 release tasks and both separate integration
  selectors. Exact artifact and report digests are recorded in the P04.8
  section above. These passes close the required browser-free gates; they do
  not erase the failed local contention reports. The hosted workflow skipped
  browser and live-provider checks as required by repository policy.
- Documentation-only main tip `e988207a939d28087b888adddf69e2273aa63a5e`
  then passed hosted FAST run
  [35823822733](https://github.com/prls-co/harden-llm/actions/runs/35823822733):
  all 10 tasks accepted, no first/preflight failure, and zero cleanup errors or
  warnings. It contains the same application source `bafa622`; the exact-source
  browser-free release pass above remains applicable. CodeQL also completed
  successfully on `e988207` in run
  [35823822677](https://github.com/prls-co/harden-llm/actions/runs/35823822677).

### P05.2 Frozen size and context result

The original P04.4 snapshot is preserved at
`tmp/codebase-reduction/after-p04.4.json`, SHA-256
`9aaff335c2cb36ab930c2c6996d68bd386f651a6b27fe122fa0e6f9980863878`. The
updated P04.8 category snapshot is `tmp/codebase-reduction/after-p04.8-worktree.json`,
SHA-256 `cec3d500ccbcd9a08190ece97c1bb782b0f353a2d6b7f566e458e0620828d5db`;
it uses the unchanged tracked-path classifier against the P03 baseline
`81206faa09d3da2289d956716475e20cccb9bd0a` and records source reference
`bafa62255d0a5b111253b3baf18d27d0091161b4`. Its document category is a
pre-final-reconciliation snapshot; the final hosted evidence recorded above
adds more documentation. Tokens remain unmeasured; byte counts are a
reproducible proxy, not an exact GPT-5.6 Luna token estimate.

| Representative maintenance context | P03 baseline bytes / lines | P04.8 bytes / lines | Delta |
| --- | ---: | ---: | ---: |
| Profile option or recovery control | 252,359 / 7,253 | 258,251 / 7,417 | +5,892 / +164 |
| Workspace draft or history behavior | 506,057 / 14,792 | 504,459 / 14,738 | -1,598 / -54 |
| Runtime progress or accounting | 278,837 / 6,806 | 299,582 / 7,281 | +20,745 / +475 |

At that snapshot, maintained application files fell from 1,317,088 bytes /
38,084 lines to 1,313,973 / 38,013 (-3,115 bytes / -71 lines). Tests grew by
29,864 bytes / 711 lines and documents grew by 56,299 bytes / 665 lines;
contracts/configuration and tooling were unchanged. Summed maintained
categories grew by 83,048 bytes / 1,305 lines before the final evidence entries
were added. The updated category counts are 131 application files, 185 test
files, 66 contract/configuration files, 33 tooling files, and 90 document
files. These measurements do not establish a whole-codebase reduction.

The separately measured profile-editor task context fell from 362,449 bytes /
10,551 lines / 14 files to 200,661 bytes / 5,814 lines / 10 files
(-161,788 bytes / -4,737 lines). Its raw report and script hashes are recorded
in the P04.8 section. The final frozen context comparison is also available at
`tmp/codebase-reduction/contexts-after-p04.8.json`, SHA-256
`e658e051014c3e9532337fda99a9fad51b8cb9c0ed8f1dbf3d615c7fb41a2237`.

The workspace context is smaller because its shared profile component and test
context changed. The profile and runtime contexts grew after required tests.
CR-A04 is **not met**: one frozen context became smaller while two became
larger through required dependencies. The editor-specific task context is much
smaller, but it does not satisfy the frozen broad-context criterion. Keep the
tests; do not claim that the plan achieved its broad context-size goal. The
selected candidate inventory is exhausted, so further net reduction needs a
new high-yield candidate review.

#### P04.9 final application-source measurement

The clean measurement tree is documentation-only descendant `4ab077810590e6270719235e3875302f3fdf91a6` of application commit
`cf14628eef80312fe0b03fefd3bed2d3e301860c`. The tracked-file category snapshot
is `tmp/codebase-reduction/after-p04.9-final.json`, SHA-256
`90b5043cf1d20304e560f36bb8f22ca45f03561f57780c456dfd2fd86aebb3d9`; it uses
the unchanged P03 manifest and standard-library measurement helper. Its
document category predates this final P05.2/P05.3 reconciliation, as did the
P04.8 snapshot above; the source and context measurements are unaffected by
these later evidence notes.
Tokens remain unmeasured.

| Category | P03 files / bytes / lines | Snapshot files / bytes / lines | Delta bytes / lines |
| --- | ---: | ---: | ---: |
| Application | 131 / 1,317,088 / 38,084 | 132 / 1,314,112 / 38,026 | -2,976 / -58 |
| Tooling | 33 / 463,193 / 10,688 | 33 / 463,193 / 10,688 | 0 / 0 |
| Tests and fixtures | 182 / 1,659,176 / 40,598 | 186 / 1,690,541 / 41,360 | +31,365 / +762 |
| Contracts/configuration | 66 / 301,023 / 7,317 | 66 / 301,023 / 7,317 | 0 / 0 |
| Documents and archived evidence | 89 / 2,599,693 / 33,153 | 90 / 2,679,284 / 34,148 | +79,591 / +995 |
| Total maintained | 501 / 6,340,173 / 129,840 | 507 / 6,448,153 / 131,539 | +107,980 / +1,699 |

The P04.9-adjusted context comparison is
`tmp/codebase-reduction/contexts-after-p04.9-final.json`, SHA-256
`8ca5b0c61d8fd7b3a9efe994f0bcb5310b2aacf2ab0a5628eccf44ded132166b`; each
after-file hash was rechecked against committed application source
`cf14628`.

| Representative maintenance context | P03 baseline bytes / lines | Final bytes / lines | Delta bytes / lines |
| --- | ---: | ---: | ---: |
| Profile option or recovery control | 252,359 / 7,253 | 258,251 / 7,417 | +5,892 / +164 |
| Workspace draft or history behavior | 506,057 / 14,792 | 491,960 / 14,336 | -14,097 / -456 |
| Runtime progress or accounting | 278,837 / 6,806 | 299,582 / 7,281 | +20,745 / +475 |

P04.9 adds a 9,651-byte / 308-line application helper while removing 9,512
bytes / 295 lines from `WorkspaceLive`; its source pair grows 139 bytes / 13
lines. The moved test file and new focused schema suite together add 1,501
bytes / 51 lines. Thus the phase narrows the workspace draft/history context
through ownership separation, while the broad frozen-context goal remains
unmet.

### P05.3 Delivery state and remaining evidence

- Current P04.9 application source:
  `cf14628eef80312fe0b03fefd3bed2d3e301860c` passed hosted FAST and
  browser-free RELEASE on `feat/workspace-schema-context`; it is now included
  on GitHub `main` (first promotion tip
  `42c64d80e094a6c591eca3fc81ca30500f86f554`). Main FAST run
  [35874387028](https://github.com/prls-co/harden-llm/actions/runs/35874387028)
  passed all 10 tasks with no first/preflight failure or cleanup errors; its
  redacted report
  `tmp/test-feedback/hosted-main-42c64d8/runner-1790173835363-2556-1ef9701f382a2bb6.json`
  has SHA-256
  `4f73d95d7c532ec740ce918665310367e639b18f52027860a7effc966d817519`.
  CodeQL run
  [35874386762](https://github.com/prls-co/harden-llm/actions/runs/35874386762)
  passed on the same main SHA. The main branch-environment workflow
  [35874713324](https://github.com/prls-co/harden-llm/actions/runs/35874713324)
  completed without deploying a branch preview or production service. No image
  publication is active for this repository; the retired GHCR publisher is
  not used. The P04.9 refactor has not been deployed.
- Backup policy, revised 2026-09-23: the user declined any pre-upgrade backup
  or restore rehearsal and accepts loss of persistent data. The deployed-to-
  candidate diff confirms no database migration or Postgres storage-code
  change. Langfuse keeps observability traces; HardLLM's widget history remains
  in Postgres/Garage and is not recoverable from Langfuse after data-store loss.
  No backup destination or restore host is a release prerequisite.
- Browser layout and public-provider behavior were not run under repository
  policy. No production component image or post-deployment identity exists for
  this refactor.
- No confirmed serious code defect was found in the five P02 review boundaries.
  CR-A04 remains unmet, so the broad-context reduction goal is still an
  unresolved limitation. Browser-free gates passed on P04.9 application source.
  Production still needs image preparation, descriptor review, authorized
  apply, and runtime verification. No backup or restore proof is required under
  the user's data-loss decision.

## Follow-up — Approved legacy cleanup (2026-09-23)

Implementation and reasoning are recorded in
[`plans/legacy-code-cleanup-plan.md`](../plans/legacy-code-cleanup-plan.md).
The follow-up removes the obsolete `repairInvalidOutput` runtime/UI/API path
while retaining bounded `jsonRepair`, requires schema v3 for profile catalog
and bundle imports, and removes active node-data backup/snapshot instructions.
It preserves historical database migrations, the v3 profile export/import
round trip, Langfuse, the consumer history widget, and secret-config rollback
guidance. No node backup command or service existed in the source inventory.

- The changed application-source and OpenAPI files have 93 added and 215
  removed lines (`git diff --numstat`), a net reduction of 122 lines. This is
  not a whole-repository reduction count; regression tests and the requested
  plan add lines.
- `make test-fast` passed all 10 tasks. `make verify` passed, including Docker
  integration, integration race, API, observability, full Go race, and
  `govulncheck`. The scanner found no vulnerabilities reachable by the code;
  it reported three vulnerabilities in required modules that the code does not
  call.
- The browser-free release selector ultimately accepted all 28 tasks, with no
  timeout, failure, cleanup error, or warning. It includes the Compose smoke,
  frontend release checks, and a final `make verify`. The retained report is
  `tmp/test-feedback/runner-1790207427332-3776603-5de85572d0348ab7.json`.
- The release run needed lockfile-pinned Elixir dependencies in the isolated
  worktree. Docker's pool was full because an empty, test-owned
  `harden-llm-smoke-4167581-1790110522106137767_harden-private` network from
  2026-09-22 remained without endpoints or containers; only that network was
  removed. A first parallel retry hit the existing two-second `TEST-049`
  fixture-start wait, which passed when run alone in 256 ms. The accepted full
  release rerun used one CPU candidate slot and did not change test assertions,
  deadlines, or coverage.
- Test fixes preserved their assertions: current v3 fixtures replaced stale v2
  profile fixtures, and the embedding DOM-ID regex now matches the actual
  `id` attribute rather than the suffix of `phx-value-node-id`.
- The first integration setup could not allocate a Docker network. Host
  inventory showed all observed default bridge ranges allocated and one empty,
  old `harden-llm-test-*` network with no endpoints or matching resource
  receipt. Only that test network was removed; application containers and
  networks were not changed. The next complete `make verify` passed.
- Browser and live-provider checks were not run under repository policy. This
  follow-up did not change production, secrets, Langfuse configuration, or
  persistent application data. V2 bundles needed after deployment must be
  re-exported through the prior importer before the v3-only importer ships.

### Exact-source release correction — 2026-09-23

The first exact-main hosted release attempt, [workflow run
35936283153](https://github.com/prls-co/harden-llm/actions/runs/35936283153),
failed `frontend-deterministic` on cleanup merge SHA
`8ee2dc89136163ea6525193ea049881e6363cb40`: 247/248 tests passed. The failed
test clicked `#model-config-toggle` while the control was disabled by
`state_save_in_flight`. In the pinned Phoenix LiveView 1.2.9 helper,
`render_async/2` snapshots the currently registered async PIDs when invoked.
Finishing one state save can start a coalesced pending save in
`WorkspaceLive.handle_async/3`, so the original wait did not cover that second
save. The UI's lock is correct; the test had assumed one `render_async/2` call
drained the entire chain.

The follow-up changes only the test: it waits up to five seconds for the fold
control to become enabled, waiting for each current async save, then performs
the same click and keeps its existing assertions. It neither changes the
product save behavior nor relaxes the test oracle.

- Focused failing test, `ERL_FLAGS='+S 4:4'` / `max_cases: 8`: 1 passed.
- Full deterministic Phoenix suite at the same scheduler setting: 248 passed,
  five opt-in cases excluded.
- `make test-fast`: all 10 tasks accepted, with no runner cleanup errors or
  warnings. Report:
  `tmp/test-feedback/runner-1790209557973-658548-92c13938231ce1fd.json`.
- The corrected source then passed the complete local browser-free release
  selector: `node scripts/run-test-tier.mjs --task release --candidate-slots
  1`; all 28 tasks passed with no timeouts, failures, cleanup errors, or
  warnings. Report:
  `tmp/test-feedback/runner-1790210903236-814076-372cf4620d82df8c.json`.
- The cleanup is not yet production-deployed. Repeat the browser-free release
  workflow on the corrected exact-main SHA after PR merge, then follow the
  local-image ADR and verify production health and runtime identities. No
  browser or provider request was made.
