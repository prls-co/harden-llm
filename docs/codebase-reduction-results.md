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

## P05 — Final verification and measured outcome

### P05.1 Applicable gates and complete diff

- The refactor source is at `c8d33d5ee3d1176cb62aa400044510d104764d6f`,
  pushed to `origin/feat/codebase-reduction`. The full diff from the P03
  refactor baseline is limited to the frozen size manifest, the result ledger,
  the profile widget and its tests, the two runtime execution owners and their
  callback regression, the plan/test catalog records, and test-tier
  registration. No public Go export, OpenAPI, stored schema, migration,
  provider behavior, or browser hook changed.
- `git diff --check`, Go formatting, and the test-tier JSON parser passed.
- The accepted FAST report recorded under P04.4 covers all 10 current T0–T2
  tasks. G-RUNTIME, G-STREAM, N-PROGRESS, and G-RACE also passed on this source.
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
- **Follow-up:** rerun the exact `make test-release` gate after host pressure
  has dropped and only one managed runner is active. If runner-contracts fails
  again under a quiet sample, investigate the first concrete failure before
  changing a test or resource limit.

### P05.2 Frozen size and context result

The official measurement is `tmp/codebase-reduction/after-p04.4.json`,
SHA-256 `9aaff335c2cb36ab930c2c6996d68bd386f651a6b27fe122fa0e6f9980863878`.
It measures clean refactor source `c8d33d5ee3d1176cb62aa400044510d104764d6f`
against the P03 baseline `81206faa09d3da2289d956716475e20cccb9bd0a`, using the
unchanged tracked-path classifier. Tokens remain unmeasured; these byte counts
are a reproducible proxy, not an exact GPT-5.6 Luna token estimate.

| Representative maintenance context | Baseline bytes / lines | P04.4 bytes / lines | Delta |
| --- | ---: | ---: | ---: |
| Profile option or recovery control | 252,359 / 7,253 | 257,544 / 7,398 | +5,185 / +145 |
| Workspace draft or history behavior | 506,057 / 14,792 | 503,947 / 14,724 | -2,110 / -68 |
| Runtime progress or accounting | 278,837 / 6,806 | 298,291 / 7,236 | +19,454 / +430 |

The maintained application category fell from 1,317,088 bytes / 38,084 lines
to 1,313,973 / 38,013 (-3,115 bytes / -71 lines). Tests grew by 27,782 bytes /
647 lines and documents grew by 26,580 bytes / 243 lines; contracts and
tooling were unchanged. Summed maintained categories therefore grew by 51,247
bytes / 819 lines. These measurements do not establish a whole-codebase or
context-window reduction.

The workspace context is smaller because its shared profile component lost
2,110 bytes. The profile context grew from its required WEB-TEST-044 assertions,
and the runtime context grew from TEST-284. CR-A04 is **not met**: one context
became smaller while two became larger through required tests. Keep those tests;
do not mark the stated context objective achieved. The selected candidate
inventory is exhausted, so any further work aimed at net task-context reduction
needs a new high-yield candidate review rather than a claim that this plan met
its size goal.

### P05.3 Delivery state and remaining evidence

- Current verified refactor checkpoint: `c8d33d5ee3d1176cb62aa400044510d104764d6f`
  on `feat/codebase-reduction`, pushed to GitHub. It has not been promoted to
  `main`, published, or deployed.
- Production remains pending an approved encrypted off-host destination and
  isolated restore host. The runbook in `docs/self-hosting.md` requires
  failure-domain backups and an actual restore on another host before upgrade.
- Browser layout and public-provider behavior were not run under repository
  policy. No production component image or post-deployment identity exists for
  this refactor.
- No confirmed serious code defect was found in the five P02 review boundaries.
  The current blockers are the unmet CR-A04 context criterion, incomplete
  browser-free release certification, and missing off-host restore selection
  and proof.
