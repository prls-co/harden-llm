# Harden-LLM delivery completion, correctness review, and code reduction plan

## 1. Purpose and execution entrypoint

- Document ID: `PLAN-HLLM-CODEBASE-REDUCTION-001`.
- Date: 2026-09-22.
- Status: **In progress.**
- Planning source: `004bf5042040c673c10b691bd57a3c384ef9b9ff` on `main`.
- Objective: finish the missing frontend delivery, resolve confirmed serious
  defects in the reviewed boundaries, and reduce the amount of maintained code
  needed to understand ordinary changes without changing supported behavior.
- Intended executor: a coding model working on one bounded task at a time,
  including a smaller model such as GPT-5.6 Luna. No delegation is required.

The user has since explicitly authorized execution of the complete plan,
including tests, GitHub `main` delivery, publication where this repository has
an active publication path, and production deployment. That authorization
covers P01 web delivery and later exact-source release promotion after its
required checks. Keep retired publication mechanisms retired; use the current
local-image release process. Repository browser and provider boundaries remain
governed by `AGENTS.md` and the testing guideline.

### 1.1 Start or resume here

Unqualified task IDs refer to this plan. "Old P04" refers specifically to
`PLAN-HLLM-RECOVERY-CLOSEOUT-001`, whose pending task is production delivery.

1. Read [AGENTS.md](../AGENTS.md) and the entire
   [testing guideline](../docs/liveview-go-testing-guidelines.md) once per fresh
   executor context. Then read sections 2–4 of this plan.
2. Consult the status table in section 10. Start the first unfinished task whose
   prerequisites are complete. A task marked `In progress` resumes from its
   recorded evidence; it does not restart automatically.
3. Load that task's source files, its direct callers, and relevant tests. Use
   `rg -n` on named symbols before reading whole large files. Do not load all
   historical plans or all provider implementations for a local UI edit.
4. Before editing, write a short task record: invariant, exact symbols to
   change/delete, expected behavioral equivalence, and selected test command.
5. Make one reviewable change. A task should normally touch at most three
   production files plus directly related tests. If more are needed, split it
   or record the dependency that requires a larger change before proceeding.
6. Run the focused checks and the required gate for that change. Record the
   source SHA, result, size measurements, and next task. Commit verified
   checkpoints using the repository's branch policy and push them promptly.
7. If a check fails, retain its result and investigate its cause. Do not expand
   the refactor, increase timeouts, weaken assertions, or repeat an ambiguous
   production operation to get a green result.

Task IDs below are planning IDs, not registered `TEST-###` cases. Reuse existing
behavioral coverage. Add a test only for an uncovered invariant or a reproduced
defect, using the [backend catalog](from_utility-llm/harden-llm-self-hosted-test-spec.md),
[frontend catalog](from_utility-llm/phoenix-liveview-frontend-spec.md), and existing
registration in `test/test-tiers.json` and `scripts/verify-test-tiers.mjs`.
Do not reuse retired IDs or create tests that only assert a new helper's name.

## 2. Verified starting point and corrected reasoning

The read-only investigation preceding this plan checked Git ancestry, the
frontend source diff, and selected fields from the actual Docker containers
and production descriptor. These are dated observations; refresh them in P00.

| Item | Observed value | Meaning |
| --- | --- | --- |
| Checkout | Clean `main` at `004bf5042040c673c10b691bd57a3c384ef9b9ff` | Later implementation must recheck drift. |
| Gateway release | `6887fcd8146961dc64598dd7a236e7a9fc522c9c` | Includes the later backend fixes. |
| Gateway image | `sha256:036d82749a1e5a29e36c848d5fca955c4b416858c9ea272f2cf1b4428210905b` | Running and descriptor image IDs matched; healthy. |
| Web release | `3201fd249f86031292be1c47acf64e0eb8a4540b` | Predates the final inherited retry controls. |
| Web image | `sha256:299439921295e6037a0cc01e1b1893e5bd1c2e441b740021479ca6666c1e5276` | Running and descriptor image IDs matched; healthy. |
| Missing web change | `40d19dc5bb5962e5e1c31905e4dbd5dc69fa7667`, followed by formatting in `2106948` | Adds effective inherited retry values and the owner-navigation action. |
| Existing certification | [Release record](../docs/release-certification.md) records a successful 28-task release run for `6887fcd...` | Retained evidence; inspect its exact run/attempt before reuse. |
| Recovery closeout | [Existing plan](recovery-production-closeout-plan.md), P04 | Partly stale progress notes, but delivery is genuinely incomplete. |

Different service SHAs are permitted. The defect is established by the missing
frontend source change, not by SHA inequality. A check against the old intended
web SHA can correctly return `equivalent` while the intended feature is absent.
Health checks establish availability, not feature delivery.

Do not mark the old P04 complete through a documentation-only change. The
backend fixes already delivered remain complete. P01 completes the web portion
and reconciles the old plan's evidence without rebuilding the gateway solely
to make version strings match.

An empty issue list, clean Git state, CodeQL success, and past green gates do
not establish the absence of critical defects. P02 is a bounded source and
behavior review. Report its scope and unresolved findings explicitly.

The local image lifecycle is an accepted decision in
[ADR-HLLM-028](../docs/adr/ADR-HLLM-028-local-image-build-deployment.md).
Backup infrastructure was not audited in this investigation. Do not infer a
missing backup from a missing release receipt or reopen GHCR, backup automation,
capacity engineering, or topology changes as prerequisites for this refactor.

## 3. Boundaries and acceptance criteria

### 3.1 Preserve these contracts

- [OpenAPI](../api/openapi.yaml) remains the Go/Phoenix contract. Keep public Go
  exports, REST fields, versions, errors, defaults, and supported imports stable.
- Keep provider execution, recovery, pricing, and cache identity in the Go
  library; authentication, owner isolation, and persistence in the gateway;
  presentation and browser sessions in Phoenix. See
  [architecture](../docs/architecture.md).
- Preserve root-owned retry budgets, finite recovery roles, explicit disabled
  branches, missing/null/false/zero distinctions, attempt ordering, cancellation,
  cache producer identity, and unknown/partial accounting semantics.
- Preserve form names, IDs, labels, ordering, hidden-input behavior, event
  bindings, per-instance isolation, staged-key handling, and ordered draft saves.
- Preserve migration history, fixture provenance, credential redaction, artifact
  publication grace, exact owner scoping, and existing timeout limits.
- Keep supported simple execution and explicit recovery execution paths. Their
  common bookkeeping is a candidate for sharing; their scheduling semantics
  are not assumed identical.

### 3.2 Excluded changes

No new public API, schema migration, provider feature, dependency, generic form
framework, validation DSL, orchestration layer, or compatibility fallback is
needed by this plan. Do not alias public Go types to internal implementation
types to save conversion code. Do not remove defensive validation because
another layer also validates at a different trust boundary.

Do not shorten names, minify code, delete useful tests/comments, remove historical
evidence, or move logic into fixtures/generated data to manufacture savings.
Simple module extraction may improve navigation, but report it separately; it
does not establish a reduction in total code size.

No browser, Wallaby, Chromium, Playwright, browser-containing Compose suite,
DOM emulator, public provider request, or profile-save probe is authorized by
this plan. Browser layout remains unchecked unless separately requested.

### 3.3 Completion criteria

| ID | Required evidence |
| --- | --- |
| CR-A01 | P01 web source contains the missing change; exact web image and runtime match the accepted candidate; service-specific checks and allowed HTTP/auth probes pass. |
| CR-A02 | All five P02 boundaries have a source-to-test finding record; no unresolved confirmed high-severity defect remains in that reviewed scope. |
| CR-A03 | Every retained refactor has a before/after deletion map, passing behavioral coverage, and measured net production-source reduction. |
| CR-A04 | Fixed representative context sets are measured before/after, including required helpers, contracts, and tests; at least one gets smaller without making another larger through new required dependencies. |
| CR-A05 | Public contracts, storage formats, visible behavior, and required test assertions remain intact; final applicable gates pass on the final application source. |
| CR-A06 | Results distinguish production bytes/tokens, tests, tooling, documents, and total maintained code; no claim of total reduction if only one category shrank. |
| CR-A07 | Every planned task is completed or has a precise supported rejection; delivery, rollback identities, unresolved limitations, and Git destination are recorded. |

Do not promise a percentage before measuring candidates. If no safe reduction
is demonstrated, report that the objective remains unmet; do not mark it achieved
because the candidate list was exhausted. Necessary bug fixes may add code;
measure them separately from the subsequent refactor baseline.

## 4. Execution sequence and shared commands

```text
P00 refresh facts
  -> P01 finish web delivery and correct P04 evidence
  -> P02 review critical boundaries and fix confirmed blockers
  -> P03 freeze size/context baseline and deletion inventory
  -> P04 execute bounded reductions, one at a time
  -> P05 verify final source, measure, and deliver results
```

A serious defect discovered during any phase immediately follows P02.6; it
does not wait for the nominal phase order. When a deployment prerequisite is
blocked, independent review and measurement preparation may continue, but
application refactoring waits for the required baseline and safety findings.

Use `dev` for normal implementation and feature branches under the current
repository policy. Check that the chosen base contains the reviewed fixes;
resolve legitimate branch divergence explicitly. Do not reset, stash, overwrite,
or force-push unrelated work. A clean isolated worktree is appropriate when the
shared checkout is busy. P01 promotes only its explicitly scoped web candidate;
later refactoring does not silently authorize another production rollout.

### 4.1 Deterministic commands

Run from the repository root unless a subshell changes directory. Set the pinned
toolchain for every shell that invokes Mix or a mixed-language gate:

```bash
export PATH=/home/kirill/.local/elixir-1.20.2/bin:/home/kirill/.local/otp-28.4.3/bin:$PATH
```

| Check | Command / scope |
| --- | --- |
| F-WIDGET | `(cd frontend && mix test test/harden_llm_web/live/profile_widget_component_test.exs test/harden_llm_web/live/profile_widget_state_test.exs test/harden_llm_web/live/embedding_live_test.exs --seed 104729)` |
| F-WORKSPACE | `(cd frontend && mix test test/harden_llm_web/live/workspace_live_test.exs test/harden_llm_web/live/profiles_live_test.exs test/harden_llm_web/live/history_trace_test.exs --seed 104729)` |
| F-AUTH | `(cd frontend && mix test test/harden_llm_web/live/auth_test.exs test/harden_llm_web/controllers/session_controller_test.exs test/harden_llm_web/session_vault_test.exs test/harden_llm_web/controllers/artifact_controller_test.exs test/harden_llm_web/controllers/trace_controller_test.exs --seed 104729)` |
| G-AUTH | `go test ./internal/gateway/auth ./internal/profiles ./internal/gateway/httpapi ./internal/gateway -count=1` |
| G-RUNTIME | `go test . ./internal/runtime ./internal/retry ./internal/cachekey -count=1` |
| G-STREAM | `go test ./internal/gateway/httpapi -run '^TestSSE' -count=1 -timeout=60s` |
| N-PROGRESS | `node --test scripts/test/run_progress_test.mjs` |
| G-RACE | `go test -race ./internal/runtime ./internal/gateway/httpapi -count=1` when shared state, callbacks, or cancellation changes justify it |
| FAST | `make test-fast` after each application refactor or defect-fix checkpoint |
| INTEGRATION | `make test-integration` for actual SQL/ownership/artifact boundaries |
| INTEGRATION-RACE | `make test-integration-race` when the changed invariant concerns concurrency across those boundaries |
| FORMAT | `gofmt -w` only changed Go files; run `mix format` only on changed Elixir files, with filenames relative to `frontend/`; verify `(cd frontend && mix format --check-formatted)` |
| WHITESPACE | `git diff HEAD --check` |
| RELEASE | `make test-release` at an explicitly required release or cross-system checkpoint; it already includes broader backend verification |

The existing [task manifest](../test/test-tiers.json) and
[Makefile](../Makefile) own execution. Default-tag Go tests do **not** run
`auth_profile_test.go`, `resource_routes_test.go`, `artifact_coordinator_test.go`,
or Postgres integration tests. Their `integration` build tag requires the managed
INTEGRATION lane. Never report those cases covered by G-AUTH or FAST alone.

Use one integration/release workflow at a time on the shared host. The runner
owns isolated resources and cleanup. Do not launch raw integration commands
without its service pool, restart Docker, kill unrelated jobs, or increase
readiness budgets in response to host contention. A failed/blocked lane remains
an explicit gap. Once a gate passes, repeat it only for changed source or a
specific unresolved concern. Plan-only edits need Markdown/link/whitespace
checks, not application builds or release certification.

## 5. P00 — Refresh the baseline

### P00.1 Record source and component identities

**Read:** section 2; the latest entries of
[release certification](../docs/release-certification.md);
[production configuration guidance](../docs/environment.md).

**Do:** inspect Git status, branch/remote ancestry, and the web source diff from
its running release. Read the protected production descriptor and inspect only
the two application containers' name, image ID, release identity, running state,
and health. Use its approved Docker context. Bound each Docker observation;
avoid a whole-daemon inventory. Print only selected nonsecret fields, never
whole container environments, descriptor source files, credentials, or sessions.

**Accept:** record both components independently and prove whether `40d19dc...`
and its formatting follow-up are included in the web source. If an operator has
already delivered the fix, retain that fresh evidence and skip the unnecessary
build/apply portion of P01. Keep the evidence reconciliation task.

**Stop:** uncertain host/context/container identity, dirty candidate build inputs,
or missing source provenance. Continue independent code reads while resolving it.

### P00.2 Freeze scope and record the initial finding

**Do:** create `docs/codebase-reduction-results.md` as a concise evidence ledger.
Enter finding `F-001`: the missing inherited-policy frontend delivery, affected
commit/symbols, impact, current component identities, and P01 as its remedy.
Record the five P02 review boundaries as pending. Use source links and counts;
never include credential-bearing diagnostics or copied production data.

**Accept:** the ledger distinguishes observed facts, retained certification,
unreviewed areas, and planned checks. An old P04 sentence claiming no push or
deployment is labeled historical; the phase itself is not marked complete yet.

## 6. P01 — Deliver the existing frontend fix

This phase refines the old recovery plan's P04: keep its required feature and
deployment evidence, account for the already delivered gateway, and finish only
the missing web delivery. Record this scoped adjustment in the old plan during
P01.5. Do not silently weaken its completion rule.

### P01.1 Select one certified source SHA

**Read:** [release source selection, section 4](gateway-image-build-deployment-spec.md);
[test workflow](../.github/workflows/test-hierarchy.yml);
[old P04](recovery-production-closeout-plan.md).

**Do:** start by investigating the recorded successful source `6887fcd...` and
release run `35726391122`; verify the actual run attempt, head SHA, accepted
release job/steps, and runner report. Verify that source is merged into `main`,
contains `40d19dc...`, and includes the latest approved frontend inputs. Inspect
the entire `frontend/` diff, including its Dockerfile and dependency lock.
Do not substitute today's moving `main` SHA for a run's actual head SHA.
Compare the candidate frontend's wire/configuration requirements with the
running gateway contract. If newer inputs require an undelivered backend or
migration, stop the web-only promotion and resolve that specific scope change.

Reuse exact-source accepted evidence if available and applicable. If it is
unavailable or source changes are needed, use the current workflow's explicit
`suite=release` selection and verify its actual run SHA/attempt, following the
linked source-selection procedure. Do not resurrect the retired publisher.
An accepted fast job alone is insufficient release evidence.

**Accept:** ledger records candidate SHA, source ancestry, exact certification
run/attempt/report, relevant frontend tests, build inputs, and the deployment
scope `harden-llm-web`. Distinguish any newly run F-WIDGET check from retained
release evidence. Resolve an actual test gap before building.

### P01.2 Prepare the web image and a concrete rollback

**Read:** [frontend Dockerfile](../frontend/Dockerfile) and the
service-specific check/apply behavior in [environment guidance](../docs/environment.md).

**Do, in this order:**

1. Confirm deployment authorization covers P01. This release has no backup or
   restore prerequisite under the user's explicit data-loss policy; verify the
   deployed-to-candidate persisted-data compatibility recorded below.
2. Record the current web image/release and prove the rollback image exists.
   Record the gateway identity and retained session volume for comparison.
3. Use a clean detached worktree at the selected full candidate SHA. Verify its
   HEAD and status. Keep the build separate from later refactor changes.
4. Use the frontend Dockerfile with **`frontend/` as the build context** and
   **`VERSION` as the build argument**. Use the approved Docker context and
   observed runtime platform. Do not copy the gateway build command unchanged.
5. Use tag `harden-llm-web:release-<full-candidate-sha>`. If it exists, inspect it;
   never replace an existing release tag with different content. Reuse only
   when its provenance/version/platform match; stop on ambiguous identity.
   Coordinate one release operator for this tag and descriptor update.
6. Record the exact resulting image ID and OCI version. The frontend Dockerfile
   currently supplies a version label; do not require a nonexistent revision
   label or modify the Dockerfile simply to mimic gateway metadata.
7. Preserve a mode-0600 descriptor checkpoint in a private mode-0700 directory,
   its hash, and the four prior web values listed below. This checkpoint stays
   outside Git. Record nonsecret identifiers in the ledger.

**Backup decision (2026-09-23):** at the user's direction, this release has no
pre-upgrade backup or restore-rehearsal gate. The deployed-to-candidate source
comparison (`6887fcd` to `cf14628`) has no application migration, OpenAPI, or
Postgres storage-code change, so the earlier migration-based justification did
not apply to this rollout. Retain the existing immutable application images for
code rollback; no new backup service, snapshot workflow, or restore host is
being provisioned.

The user accepts loss of persistent data if the database or object store is
lost. HardLLM's consumer history/trace API reads product records from Postgres
and artifact bodies from Garage; Langfuse receives gateway observability traces
and is not a source for that widget history. A normal code deployment leaves
the current history store in place, but a Postgres/Garage loss will remove
widget history even if Langfuse still has its telemetry traces. Profile
export/import remains the separate way to move profile configuration.

Core build invocation, only after these prerequisites and variables are checked:

```bash
set -euo pipefail
: "${HLLM_DOCKER_CONTEXT:?Record the approved Docker context first}"
: "${HLLM_RUNTIME_PLATFORM:?Record the runtime platform first}"
: "${HLLM_WEB_RELEASE_SHA:?Select the certified full source SHA first}"
: "${HLLM_BUILD_CHECKOUT:?Create the clean candidate build worktree first}"
[[ "$HLLM_WEB_RELEASE_SHA" =~ ^[0-9a-f]{40}$ ]]
HLLM_BUILD_HEAD="$(git -C "$HLLM_BUILD_CHECKOUT" rev-parse HEAD)"
test "$HLLM_BUILD_HEAD" = "$HLLM_WEB_RELEASE_SHA"
HLLM_BUILD_STATUS="$(git -C "$HLLM_BUILD_CHECKOUT" status --porcelain --untracked-files=all)"
test -z "$HLLM_BUILD_STATUS"
HLLM_EXISTING_WEB_IMAGE="$(docker --context "$HLLM_DOCKER_CONTEXT" image ls \
  --quiet --no-trunc --filter "reference=harden-llm-web:release-$HLLM_WEB_RELEASE_SHA")"
test -z "$HLLM_EXISTING_WEB_IMAGE"
docker --context "$HLLM_DOCKER_CONTEXT" build \
  --platform "$HLLM_RUNTIME_PLATFORM" \
  --build-arg VERSION="$HLLM_WEB_RELEASE_SHA" \
  --tag "harden-llm-web:release-$HLLM_WEB_RELEASE_SHA" \
  --file "$HLLM_BUILD_CHECKOUT/frontend/Dockerfile" \
  "$HLLM_BUILD_CHECKOUT/frontend"
```

The variable values come from the recorded candidate/context/worktree, not
defaults or guessed host paths. Use fail-fast shell execution; retain failed
build diagnostics and worktree for investigation, and clean only owned scratch
resources after success. Do not run image pruning.

### P01.3 Prepare and inspect the scoped descriptor change

**Do:** compare the private descriptor hash with the checkpoint before editing.
Atomically replace only these `harden-llm-web` fields while retaining permissions:

- `services.harden-llm-web.expectedImage`;
- `services.harden-llm-web.identityEnvironment.HARDEN_LLM_RELEASE`;
- `serviceEnvironmentOverrides.harden-llm-web.HARDEN_LLM_RELEASE`;
- `serviceImageOverrides.harden-llm-web`.

Review the redacted field diff and verify that all other values are identical.
If the descriptor changed concurrently, reconcile its current web fields rather
than writing the entire older checkpoint over it. Honor the existing service
allowlist; do not broaden it to dismiss an unexplained difference.

```bash
node scripts/production-config.mjs check \
  --descriptor /home/kirill/.config/harden-llm/production.json \
  --services harden-llm-web \
  --expected-release "$HLLM_WEB_RELEASE_SHA"
```

**Accept:** candidate image/version and desired configuration are correct;
only expected old-runtime differences remain, or the service is already
equivalent. `check` exits 0 for equivalence, 2 for differences, and 1 for failure.
Review each difference; exit 2 alone is not permission to apply.

### P01.4 Apply, inspect, and handle failure

After the authorized candidate is concrete and reviewed:

```bash
node scripts/production-config.mjs apply \
  --descriptor /home/kirill/.config/harden-llm/production.json \
  --services harden-llm-web \
  --expected-release "$HLLM_WEB_RELEASE_SHA"
```

**Accept:** the same service-specific `check` returns equivalent; the actual
container image ID, OCI version, release environment, and healthy state match
the web candidate. Run a whole-descriptor check without `--expected-release`
when the two applications intentionally have different SHAs. Verify gateway
identity and retained sessions/infrastructure against the before record.

Run bounded public web `/healthz` and `/login`, API `/healthz` and `/readyz`,
and the documented authenticated read-only routes using the approved login
procedure. Retain statuses, not credentials or response bodies. Do not borrow
bearer sessions, save profiles, sync profiles, or make a provider call to probe
this deployment. Component tests establish rendered control semantics; HTTP
availability and image identity do not establish browser layout.

**Failure:** `apply` exits 0 or 1. A failure after service recreation may leave a
partial transition. Inspect actual state before another action. If rollback is
required, restore only the recorded web fields into the current descriptor,
check the retained web image against its old release SHA, and apply web only.
Keep current gateway fields, data, keys, and session volumes. Record failures
and actual final disposition; never call a failed deployment complete.

### P01.5 Close the real delivery gap

**Edit:** `docs/release-certification.md`,
`plans/recovery-production-closeout-plan.md`, and the results ledger.

**Do:** append dated evidence, retain earlier failed attempts as history, and
correct the stale claim that nothing was pushed/deployed. Mark old P04 complete
only after its required behavior, certification, component identities, rollback,
and probe evidence are reconciled. Explain the web-only completion of a gateway
portion already delivered; record per-component SHAs instead of claiming both
were rebuilt. Record F-001 resolved only after P01.4 succeeds.

**Exit:** production baseline is accurate and the intended UI change is delivered.
Documentation changes do not require another application rebuild or a repeated
release gate. Only now advance to refactor implementation.

## 7. P02 — Review the critical boundaries

Each task below reviews one complete path, then maps its invariants to existing
tests. Use synthetic owners, credentials, payloads, and local provider stubs.
Do not inspect real users' prompts/history to establish a test case.

### P02.1 Authentication and owner isolation

**Read:** `internal/gateway/auth/service.go`, `httpapi/httpapi.go`,
`httpapi/resources.go`, `resources.go`, `profile_service.go`,
`internal/postgres/resources.go`, `repository.go`, and `records.go`.
Then follow the corresponding Phoenix auth/session/controller entrypoints.

**Check:** authentication precedes access; owner identity comes from the session,
not request fields; profile/state/run/history/trace/resource/cache reads and
mutations use that owner through the final SQL/object operation. Test guessed
IDs with two owners, expired/revoked sessions, and unauthenticated access.
Check both HTML/controller and LiveView entrypoints. Do not treat a custom
provider endpoint or an intended operator privilege as a defect without a
violated trust boundary.

**Evidence:** named cases in `auth/service_test.go`, `auth_profile_test.go`,
`resource_routes_test.go`, frontend auth/session/controller tests. Run G-AUTH
and F-AUTH; real owner/SQL claims require INTEGRATION in P02.6.

### P02.2 Credentials, origin binding, and diagnostics

**Read:** `internal/profiles/credentials.go`,
`internal/gateway/runtime_profiles.go`, `profile_service.go`,
`shared_runtime.go`, `internal/diagnostics/diagnostics.go`,
`frontend/lib/harden_llm_web/session_vault.ex`, and credential-staging handlers
in `ProfileWidgetComponent`.

**Check:** credential encryption/AAD and intended endpoint binding; request-local
owner bindings survive shared-client reuse; API keys never become public profile
fields, browser cookie contents, saved drafts, logs, error bodies, or diagnostic
fixtures. Staging/canceling a replacement key cannot silently save it. Review
provider redirect/dispatch behavior where it can change the credential origin.

**Evidence:** `credentials_test.go`, `shared_runtime_test.go`,
`diagnostics/bundle_test.go`, vault and profile-widget tests. Run the relevant
G-AUTH/F-AUTH/F-WIDGET tests plus
`go test ./internal/diagnostics ./internal/providers -count=1` as warranted.
A discovered leak uses fake values in the regression, never captured secrets.

### P02.3 Retry budgets, cancellation, and completion

**Read:** `internal/runtime/execute.go`, `recovery_execute.go`, `retry/retry.go`,
`internal/gateway/run_service.go`, `httpapi/resources.go`, and
`frontend/lib/harden_llm_web/harden_api.ex`.

**Check:** total attempt budget across branches; credential/catalog failures
before dispatch; deadline and disconnect propagation; interruptible retry waits;
transport retry preserves repair payload; one terminal SSE outcome; no replay of
an ambiguous mutation. Establish the supported differences between simple and
explicit recovery paths, including preflight/terminal progress emission.

**Evidence:** `repair_test.go`, `recovery_plan_test.go`, `sse_test.go`,
`internal/gateway/run_test.go`, and `scripts/test/run_progress_test.mjs`.
Run G-RUNTIME, G-STREAM, and N-PROGRESS; select G-RACE for the actual
concurrency boundary. Preserve controlled event-order assertions, not sleeps.

### P02.4 Cache isolation, admission, and accounting

**Read:** `internal/gateway/run_service.go` (`ownerCacheStore`),
`internal/postgres/cache.go`, `internal/cachekey/cache.go`,
`client_cache.go`, `internal/runtime/cache.go`, and both runtime execution files.

**Check:** same operation hash for two owners cannot cross-read or cross-delete;
version/producer/target/search facts are validated before replay; corrupt records
fail according to the current contract; cache write failure does not invent a
durable hit or erase provider work; result and provider accounting remain distinct;
unknown cost remains unknown. Preserve concurrent insert/delete semantics.

**Evidence:** `client_cache_test.go`, `internal/cachekey/cache_test.go`,
`internal/postgres/cache_test.go`, `shared_runtime_test.go`, and runtime
search/accounting cases. Run G-RUNTIME and needed gateway unit tests; defer the
actual SQL concurrency proof to the managed lane in P02.6.

### P02.5 Artifact publication, deletion, and crash convergence

**Read:** `internal/gateway/artifact_coordinator.go`,
`internal/postgres/artifact_lifecycle.go`, `records.go`,
`internal/artifacts/garage.go`, `client_artifacts.go`, and
`internal/gateway/command/artifact_inventory.go`.

**Check:** publication journal to canonical metadata handoff; the existing
30-second publication grace; owner/run/trace association; retryable interrupted
deletion; fresh in-flight objects survive reconciliation; aged orphans converge;
inventory reports truncation and failures honestly. Do not change grace or
retention durations to make a test pass.

**Evidence:** `artifact_coordinator_test.go` (TEST-060), artifact tests, and
inventory command tests. Identify a cheap invariant case before any defect fix;
retain the real Postgres/Garage case for the distinct cross-store boundary.
Run INTEGRATION in P02.6. Do not run a destructive production cleanup.

### P02.6 Triage, repair, and close the review

For each area, add one ledger row with source path/symbol, invariant, named
test/subtest, whether it actually ran, result, and remaining uncertainty.
If a defect is suspected, record a concrete trigger and expected/actual behavior
before assigning severity. Missing evidence is not proof of a defect.

| Finding | Required action |
| --- | --- |
| Confirmed unauthorized access, credential exposure, data loss, duplicate charged execution, or failure to respect a bounded execution budget | Pause dependent refactoring; reproduce locally, add the lowest sufficient regression, fix the owning layer, and rerun affected checks. Resolve before P03. |
| Confirmed lower-impact defect in a module selected for refactoring | Fix in a separate commit first, so behavioral repair and equivalence refactoring remain distinguishable. |
| Suspicion without reproduction / unclear contract | Investigate the narrow call chain and specification; retain an explicit open finding. Do not change behavior on a guess. |
| Optional enhancement or accepted operational limitation | Record a bounded follow-up with rationale; it does not automatically block code reduction. |

Register any new regression in the existing catalogs. Run one managed
INTEGRATION lane covering the three identified database/storage boundaries;
run INTEGRATION-RACE only when its concurrency assertion requires it. A failure
gets diagnosis and a justified follow-up run. Any code fix also requires FAST.
Do not report an unrun integration case as passing because its source looks right.

If a necessary fix requires a migration or public-contract change, separate it
from the size refactor and write the concrete migration/rollback implications
before implementation. Obtain a scope decision when supported behavior or
destructive production actions would change; ordinary local fixes and their
regressions need no repeated permission within an authorized execution task.

**Exit:** five review records exist, required boundary evidence is available,
and no confirmed serious defect remains. State the limited review scope;
do not certify the whole application free of security/correctness defects.

## 8. P03 — Measure and select actual deletions

### P03.1 Freeze the post-repair measurement baseline

Commit the verified P02 state and record its full SHA as `refactor_base`.
The earlier planning SHA remains the initial comparison point, but bug-fix
growth is not charged to the subsequent refactor result.

Measure only Git-tracked files from clean snapshots. Exclude dependencies,
build outputs, ignored scratch, and generated/vendor assets from the maintained
code total; list exclusions explicitly. New files must be included, so measure
after committing them rather than relying on an old file list.

Record UTF-8 bytes, physical lines, nonblank lines, and file count for these
separate categories with a frozen path manifest:

| Category | Include |
| --- | --- |
| Application | Root library, gateway commands, production internal packages, `frontend/lib/`, application JS/CSS/templates. |
| Tooling | Maintained scripts and test/deploy harness implementations, including `internal/integrationtest`, `testkit`, `smoke`, `capacity`, `deploytest`, and `eval`. |
| Tests | Go test files, frontend test/support files, Node test files, and test fixtures reported separately from production. |
| Contracts/configuration | OpenAPI, SQL migrations, lockfiles, Docker/Compose and workflow/config sources. |
| Documents | Active plans, specifications, runbooks, and archived evidence, separated from code savings. |

Do not delete a harness as unused production code. Do not count a moved file as
deleted or hide new descriptors/helpers in another category. Keep the category
manifest stable; report additions and legitimate classification corrections.
Keep full scratch measurements under `tmp/codebase-reduction/` and commit the
small totals/manifests/method in the results ledger. A scratch report alone is
not durable completion evidence.

Use one dependency-free measurement procedure throughout the work:

1. Start from a clean committed snapshot and record `git rev-parse HEAD`.
2. Get tracked paths with `git ls-files -z`; classify each maintained code file
   exactly once using the saved path manifest. List excluded generated/vendor
   files separately. Include additions when measuring the final snapshot.
3. Read file bytes without changing them. `bytes` is the raw byte length;
   `physicalLines` is the count from splitting into lines; `nonblankLines` is
   the number of those lines with non-whitespace content. Record a SHA-256 for
   each measured file. Only UTF-8 text gets line/token metrics; retain byte
   counts and an explicit binary label for other tracked assets.
4. Sum per-file records by category and fixed context set. Write JSON containing
   `sourceSha`, method/version, exclusions, category totals, context totals, and
   the per-file records. Keep paths sorted for a reproducible diff.
5. Reuse the exact measurement code and manifest rules for each after-snapshot.
   Record its hash in the ledger. Validate that no path is counted twice or
   disappears solely because the manifest was not updated after a move.

The implementer may use a short local Python/Node script for this procedure;
there is no requirement for a new CI task, metrics service, or dependency.

If an offline tokenizer is already available, freeze its name/version/config
and include token counts. Otherwise report bytes as a proxy and token counts
as unmeasured. Do not divide characters by four, claim a model's exact context
utilization, call a paid API, or add a dependency solely for this measurement.
Any new measurement code counts as tooling overhead in the final result.

### P03.2 Freeze representative code-context sets

These measure the files needed for a real maintenance task, rather than only
the length of the edited file. Store exact relative paths once in the ledger.
Include new helpers or contracts required after a refactor in the after-set.
Do not drop necessary tests or dependencies to obtain a smaller number.

| Task | Initial context set |
| --- | --- |
| Change a profile option/recovery control | `profile_widget_component.ex`, `profile_widget_state.ex`, `profile_form.ex`, `profile_defaults.ex`, `components/core_components.ex`, and the component/state/embedding tests. |
| Change workspace draft or history behavior | `workspace_live.ex`, its `.html.heex`, `profile_widget_state.ex`, the imported result/trace components actually needed, and workspace/history tests. |
| Change runtime progress/accounting | `client.go`, root `types.go`, `internal/runtime/{execute,recovery_execute,types}.go`, relevant retry types, and client/runtime/progress tests. |

Resolve abbreviated paths against the current tree before freezing the sets.
Measure source, contracts, and tests separately as well as their sum. Whole-file
counts make the result reproducible; excerpting less text during one agent run
does not count as a code reduction.

### P03.3 Build the deletion inventory

Inspect all P04 candidates before editing them. Each row must contain:

```text
Candidate ID / owner / exact repeated or unreachable symbols:
Required behavior and intentional differences:
Existing callers and tests / uncovered invariant:
Proposed replacement and old code to delete:
Expected byte/token direction / added dependencies or helpers:
Chosen focused check / distinct expensive boundary if any:
Disposition: selected, rejected with reason, or awaiting specific evidence
```

Start with visible repetition already identified in P04. Rank by demonstrated
duplication, small behavioral surface, and net savings, not file length. Do not
promise reductions for `router.go`, `WorkspaceLive`, or the strict wire decoder
merely because they are large. Preserve strict decoding and intentional provider
protocol differences.

### P03.4 Re-review after the first measured context misses

The first P04 measurement did not meet CR-A04. Keep the frozen P03 totals
unchanged and re-review only exact task/file boundaries that caused the miss.
The largest avoidable runtime context increase is TEST-284's 433-line callback
contract appended to the general `repair_test.go` suite. Select one bounded
organization candidate: move TEST-284 and its dedicated fixtures to
`internal/runtime/progress_snapshot_test.go`, keeping every subtest, event and
counter expectation, time/deadline check, failure case, cache case, and
copy-isolation assertion. Do not reduce the oracle or delete cases. Make the
focused progress-task context explicit and compare the whole `repair_test.go`
baseline against the dedicated progress test plus any genuinely shared helper
files after the move. This is a context-boundary candidate, not a claim that
the complete repository becomes smaller. Reject it if additional helper
dependencies erase the measured task-context saving.

## 9. P04 — Implement bounded reductions

Each task uses the same sequence: inspect callers and assertions; fill its
deletion row; run/reuse an applicable passing baseline; add only missing
behavioral coverage; implement; run focused checks and FAST; measure all changed
production code including helpers; review; commit/push; update status.
No-op/rejected candidates require evidence and move to the next candidate.
If a candidate expands production code or cannot preserve its contract, discard
only that task's uncommitted edits or revert its isolated commit; preserve
unrelated work and record the reason. If later tasks already depend on it,
review those dependencies before a revert rather than leaving broken callers.

### P04.1 Consolidate repeated profile option markup

**Owner:** `frontend/lib/harden_llm_web/live/profile_widget_component.ex`,
`profile_editor/1`. **Tests:** F-WIDGET, then F-WORKSPACE if host behavior changes.

The four numeric option fields (`maxTokens`, `temperature`, `topP`, `topK`)
repeat the same input markup with different labels and `step`. The three
capability checkboxes repeat another group. Replace each group with a small
literal descriptor list and the same input component in a `:for` loop.
Use fixed field atoms; never turn user strings into atoms. Keep the descriptors
local to this component; do not introduce a general form engine.

Before editing, record each field's ID/name, label, order, type, min, optional
step, placeholder, event target, and hidden checkbox input behavior. In
particular, absent `step` on integer controls must stay absent; decimal controls
retain `step="any"`. Preserve validation, all field names, and zero/false values.

**Accept:** rendered component assertions cover the exact field matrix in
workspace/profile-definition/recovery contexts; payload behavior is unchanged;
net component-plus-helper size decreases. Leave credential and inherited retry
controls outside this task; they have different submission semantics.

### P04.2 Calculate the editor model list once per render

**Owner:** the same `profile_editor/1`. **Tests:** F-WIDGET.

It calls `models_for/5` with the same arguments for the combobox, profile-definition
datalist, and displayed count. Derive one clearly named assign during each
render and use it in all three locations. Preserve exact ordering, custom
current-model inclusion, host-supplied catalog precedence, and count semantics.

Use `assign`, not a persistent cache or `assign_new` that could retain stale data.
Verify updates to selected profile, model catalog, current custom model, and
independent widget instances. Delete the repeated argument blocks.

**Accept:** all three consumers reflect current inputs; no new state owner or
cross-render cache; equivalent rendered behavior with net source reduction.

### P04.3 Simplify repeated fold-state assignment

**Owner:** `assign_fold_state/2` and `boolean_assign/3` in the profile component.
**Tests:** F-WIDGET, including parent-update and independent-instance cases.

Replace the repeated assign calls with an explicit fixed mapping of incoming
keys to socket keys only if it reduces code after the mapping is included.
Retain the exact rule: explicit `true`/`false` replaces the value; absent, nil,
or invalid input preserves the existing socket value. Preserve `fold_disabled`.
Keep atoms static and assignment order deterministic. Do not merge the separate
main/target state machines or change event names as part of this task.

**Accept:** partial parent updates and explicit false still behave identically;
no fold resets, cross-instance changes, or new notification/persistence calls.

### P04.4 Share runtime progress-snapshot construction

**Owner:** `internal/runtime/execute.go`'s `emitProgress` and
`recovery_execute.go`'s `emit`. **Tests:** G-RUNTIME, G-STREAM, N-PROGRESS
tests, then G-RACE for callback/state isolation.

The two closures duplicate accounting/timeout copies, stream counter addition,
remaining-attempt calculation, origin/attempt projection, and deadline facts.
Extract only snapshot construction into one package-private helper, preferably
in the existing runtime package. Keep one sequence counter and active stream
per call; do not introduce a global observer or a shared mutable record.

Concrete implementation sequence:

1. Write an equivalence table for no callback, nil planned work, before the first
   attempt, active stream, completed attempt, cache hit, deadline, and terminal
   failure. Record which path emits each event and which intentionally does not.
2. Identify the existing tests proving these cases. Add missing assertions at
   the public `Execute`/progress callback boundary before extraction; do not
   create tests that merely compare a helper to itself.
3. Pass stage, branch, profile, and reasoning identity explicitly. Simple
   execution currently supplies original-generation identity; explicit recovery
   supplies the active work identity. Preserve that difference.
4. Preserve snapshot copying, nil/empty behavior, counter accounting, and the
   timing/order of `config.Now` observations. Do not consolidate clock sampling
   or change preflight/terminal event ordering as an incidental optimization.
5. Leave callback guards, sequence advancement, stage transitions, cancellation,
   scheduling, cache admission, telemetry lifetime, and caller error handling
   with their existing owners. Remove the duplicated construction blocks.

**Accept:** exact progress ordering and accounting/counters remain equivalent
for both paths; race checks pass; net runtime-plus-helper size decreases. If
the extraction requires a new generic execution engine, reject that design and
retain the distinct loops. A discovered behavioral defect returns to P02.6.

### P04.5 Evaluate small public-result projection reuse

**Owner:** root `client.go`, specifically the progress adapter,
`resultFromRecord`, `publicAttempt`, and `publicOrigin`. **Tests:** root client
tests, G-RUNTIME, and the existing wire/contract checks in FAST.

Both progress and final results convert attempts and accounting ledgers. Origin
mapping is also repeated, but progress omits an empty origin while final results
carry a value. Consider short typed conversion helpers only where the combined
definition plus callers is smaller and easier to read.

Preserve empty attempt arrays, optional progress accounting, deep/shallow copy
behavior where observable, omission rules, and stable JSON fields. Never alias
public types to internal types or route one wire representation through another
just to reuse a serializer. Reject this candidate if helper plumbing outweighs
the removed mapping code; a measured rejection is a valid task outcome.

### P04.6 Remove only proven unreachable residue

**Owner:** only private symbols/dependencies identified in P03's inventory.
**Tests:** the affected owner suite and FAST; integration only for a real changed
service boundary. No presupposed dead symbols are listed in this plan.

For each deletion, search definitions/references and inspect dynamic use:
Phoenix callbacks, HEEx component calls, hooks, exports, registrations, build
tags, embedded resources, reflection, fixtures, and workflow entrypoints.
An exported Go API is not unused simply because the repo has no caller. A test
helper is not dead because production does not import it. Forward migrations,
provider branches, and the simple runtime path remain supported contracts.

Delete one proven group at a time and its now-unneeded private wiring. Preserve
meaningful behavioral tests. Dependency removal needs proof across all supported
build tags and tasks, not only the fast lane. If that proof would require an
unauthorized browser/provider call, retain the dependency and record the gap.

**Accept:** the specific deleted path is unreachable in supported operation,
required behavior remains covered, and reduction is measured. Do not expand into
a broad dependency upgrade or test-runner rewrite.

### P04.7 Split TEST-284 into a progress-focused test owner

**Owner:** `internal/runtime/repair_test.go` and a new
`internal/runtime/progress_snapshot_test.go`. **Tests:** TEST-284 and the full
runtime package.

The callback contract is a distinct progress concern appended to a broad retry
repair suite. Move `TestProgressSnapshotContracts` and its fixture types and
assertion helpers into the dedicated test file. Keep the TEST-284 traceability
comment and preserve test names and assertion contents. If the old suite's
`identityExecutor`, `planProfiles`, or telemetry cache would make the focused
context depend on unrelated test files, replace only those fixtures with
small local equivalents whose behavior is explicit; keep production paths and
the assertion oracle unchanged. Do not duplicate a helper already needed by
this focused suite.

**Accept:** zero test cases or assertions are removed; the dedicated test runs
within the runtime package; focused progress-task bytes decrease against the
P03 baseline after required helper files are included; and the runtime suite,
race boundary, and FAST pass. Keep the original frozen runtime-context result
published unchanged and report this focused context separately. If measured
dependencies erase the saving, revert this candidate and record that result.

### P04.8 Isolate the profile editor contract context

The profile component suite is a 43,483-byte whole-file context. Three adjacent
tests that directly exercise the profile editor have a self-contained scope:
the rendered numeric/capability matrix, model consumers across current renders,
and partial parent fold assignments. Their test bodies and editor-only helpers
occupy 5,656 bytes before module setup. Move those exact cases and private
helpers to `frontend/test/harden_llm_web/live/profile_widget_editor_contract_test.exs`.
Move the numeric matrix assertion helper to one shared test-support module so
the existing live-render assertions and the extracted component test keep the
same implementation and oracle. Reuse the existing canonical profile-state
fixture rather than creating a narrower fake profile.

The new module should use `ExUnit.Case, async: true`; it does not need the
connection setup, Req stub, or other live-view integration helpers from the
large suite. Preserve every assertion, test count, and test ID. Keep the
relevant profile editor rendering and server-side update boundaries covered by
their existing tests in the original suite.

Measure the editor-contract task context before and after with production
component/form dependencies and every needed test fixture/helper included. The
baseline uses the full `profile_widget_component_test.exs`; the candidate uses
the focused test module plus its actual helper dependencies. Also recompute the
frozen broad profile context with all moved tests and helpers included. This is
a context-boundary candidate: it is acceptable only if the focused task context
falls materially without dropped test coverage. It does not claim a reduction
in total repository bytes, and it cannot by itself satisfy CR-A04 if the frozen
broad context grows. Keep P03's frozen manifest unchanged.

**Checks:** run the focused module and the remaining component suite, then
`make test-fast`; retain the browser-free release gate for the resulting exact
tree. Browser checks remain opt-in. If the context measurement does not show a
real task-context saving after helper dependencies, revert the split and
record the rejection.

**Risk / aftercare:** the focused module tests component rendering and direct
server-side update semantics; it does not prove endpoint/session routing,
LiveSocket behavior, or browser layout. Keep routed workspace/embed tests in
the original suite. For later changes that touch those host paths, run the
profile component suite and the affected workspace/embedding suite in addition
to the focused editor module.

### P03.5 Re-review the remaining broad context misses

Keep the original P03 manifest and before totals frozen. Review the P04.8
workspace draft/history context for a concrete cross-concern block whose
behavior and tests can move to a separate owner without weakening assertions.
The selected candidate is the contracted JSON Schema shorthand conversion and
validator currently embedded in `WorkspaceLive`, together with its schema-only
LiveView cases. It occupies a distinct rendering/validation concern from draft
serialization and History lifecycle. Do not move schema fields out of the
workspace state, the generic schema decoder used by draft restore, or the
run-gating owner.

The candidate is accepted only if the after-context path set excludes the new
schema module and schema-only test module for the frozen *draft/history*
maintenance task with a written dependency rationale, and the measured context
falls after every required draft/history helper and test remains included.
Keep `state_schema/1` in `WorkspaceLive`; the new module handles only schema
editing and structured-run validation, while draft restore/save parses a schema
value as state data. Retain the module and tests in a separate schema
maintenance context. If that dependency rationale is not accurate, or the
frozen workspace context does not fall, revert P04.9 and record the result
rather than changing P03's baseline.

### P04.9 Give contracted schema conversion its own owner

**Owner:** `WorkspaceLive` currently owns schema shorthand conversion,
normalization, contracted-subset validation, status text, and schema gating.
Move only shorthand conversion and validation plus their fixed keyword/type
tables to a small `HardenLlmWeb.WorkspaceSchema` module. Keep defaults,
workspace state decoding/normalization, event handling, form assignment,
persistence, status presentation, and Run lifecycle in `WorkspaceLive`.
Preserve all existing return tuples, error text, blank-input behavior,
text-mode behavior, property ordering, recursive validation, JSON Pointer
escaping, and schema form serialization.

Move `WEB-TEST-057` and the separate invalid-local-JSON LiveView test, with
their assertions unchanged, to a schema-focused test module. Keep combined
workspace parity, draft persistence, default rendering, and run-payload tests
in `workspace_live_test.exs`. Update the canonical test-spec path for
`WEB-TEST-057`; do not change its ID or oracle. Add no browser, dependency, or
second schema dialect.

Implementation steps:

1. Record the exact schema-owned definitions and callers, and run the current
   focused workspace suite as a baseline. The pinned-toolchain
   `mix test test/harden_llm_web/live/workspace_live_test.exs` baseline passed
   all 57 tests. Confirm the schema cases selected for relocation are
   behaviorally independent from draft/history assertions.
2. Add a pure module API for the extracted operations and unit-level tests for
   any behavior not already observable in the retained LiveView assertions.
   Keep UI status maps and socket changes with `WorkspaceLive`.
3. Route shorthand generation, contracted validation, and schema checks through
   the new module. Leave HEEx status functions local to `WorkspaceLive`; preserve
   exact HTML messages, run payloads, persistence, and request count.
4. Relocate the two schema-only tests without removing or rewriting assertions.
   Keep test IDs and canonical catalog references traceable. Do not duplicate
   general workspace stub logic if the schema cases can use a small private
   fixture setup.
5. Format, run the schema-focused module and the remaining workspace LiveView
   suite, then run `make test-fast`. Recompute all three frozen whole-file
   contexts and category totals; include any helper actually needed by a
   draft/history change. Run the hosted browser-free release gate on the exact
   final source. Browser tests remain opt-in.

**Acceptance:** workspace draft/history context is smaller after dependency
accounting; all moved and retained test assertions pass; no event, wire,
storage, schema, or UI contract changes; other frozen context totals do not
increase from this phase. This is a task-context reduction and module-boundary
change, not a claim that moving code alone shrinks total application bytes.

**Risks / aftercare:** the schema module remains required for schema editing
and structured-run validation, so it belongs in that focused task context.
Draft restore keeps its state decoder in `WorkspaceLive`, avoiding a dependency
from draft/history work to the validator. Keep `WEB-TEST-057` end-to-end
coverage; a pure module test cannot prove Run gating or absence of backend
mutation. If the extraction adds helper dependencies to draft/history work or
causes a byte-neutral/larger context, revert the phase and keep the current
implementation.

## 10. P05 — Final verification, measurement, and handoff

### P05.1 Review the complete change against the post-repair baseline

Inspect the entire diff from `refactor_base`, including new helpers and tests.
Check public exports, OpenAPI, persisted wire versions, migrations, defaults,
error handling, hidden form fields, and test assertions. Explain any changed
file outside the selected owners. Search deleted symbol names for dangling
registrations. Confirm formatting and WHITESPACE.

Run FAST on the final application source unless that exact final source already
has an accepted run. Run only distinct additional boundaries required by actual
changes: G-RACE for runtime state; INTEGRATION for database/storage behavior;
RELEASE for cross-system changes or an explicit release. Do not run `make verify`
again after a successful RELEASE that already included it. Retain the exact
report/run identity and label local, hosted, deployed, and unrun evidence.

### P05.2 Publish honest measurements and disposition

Reapply P03's frozen measurement method to the final clean source. Report:

- initial planning SHA, post-repair baseline SHA, and final refactor SHA;
- per-category before/after bytes, lines, files, and tokens when actually measured;
- production deletions, new helper overhead, test additions, and total maintained
  code change; report necessary safety-fix growth separately;
- before/after totals for all three fixed context sets;
- candidate disposition, removed duplication, preserved behavior, and exact tests;
- delivery destination, actual production component identities, and what remains
  undeployed; evidence limits and any genuine outstanding findings.

Keep detailed raw reports in ignored scratch with hashes/summary evidence in
`docs/codebase-reduction-results.md`. Keep this plan's status table compact.
Archive neither active source nor useful specifications just to shrink the metric.

Default refactor delivery is the verified feature/dev checkpoint under repository
policy; `dev` may deploy through its normal automation. Main/production promotion
of these later refactors requires an explicit release instruction. P01's web
delivery authorization does not silently extend to every future production push.
If a final production release is authorized, select exact certified source,
build only affected services, retain per-service rollback, and verify each
against its own intended candidate as in P01.

### 10.1 Status table

| Task | Status | Evidence / next action |
| --- | --- | --- |
| P00.1 source/runtime refresh | Complete | Refreshed 2026-09-22; exact current identities and candidate evidence in `docs/codebase-reduction-results.md`. |
| P00.2 initial ledger | Complete | F-001 and the five unreviewed boundaries recorded in `docs/codebase-reduction-results.md`. |
| P01.1 certified candidate | Complete | Historical `6887fcd` exact-source run 35726391122 and ancestry are verified. Final full-tree candidate `bafa622` passed hosted FAST/release (35821407815/35821617071), was promoted to `main` by documentation-only tip `e988207`, and that tip passed main FAST/CodeQL (35823822733/35823822677). See the dated P01.1 record in the results ledger. |
| P01.2 image and rollback | Pending | User explicitly declined backups and accepts persistent-data loss. The deployed-to-candidate comparison (`6887fcd` to `cf14628`) shows no application migration, OpenAPI, or Postgres storage-code change; the migration-based backup gate was unnecessary for this release and is removed. Retain the current immutable web/gateway images for code rollback. HardLLM widget history remains in Postgres/Garage; Langfuse telemetry is not a replacement, and history is not recoverable after data-store loss. Prepare exact image identities; no backup destination or restore host is required. |
| P01.3 descriptor review | Pending | Review only the authorized service fields after source, image IDs, and rollback image identities are recorded. |
| P01.4 delivery/checks | Pending | Production apply and probes remain gated on image identity, descriptor review, and runtime checks; no backup or restore rehearsal is required under the user's accepted data-loss policy. |
| P01.5 old P04 closeout | Pending | Complete only after actual delivery, runtime identity, rollback, and probe evidence. |
| P02.1 owner isolation | Complete | Authenticated principal ownership traced to SQL/resource operations; G-AUTH, F-AUTH, and managed INTEGRATION passed. See results ledger. |
| P02.2 credential boundaries | Complete | Origin/AAD, request-local owner binding, diagnostics, staged cancellation, and stored-binding retention reviewed; focused checks and managed TEST-022 passed. |
| P02.3 runtime completion | Complete | Shared and explicit retry budgets, cancellation, and SSE terminal handling reviewed; G-RUNTIME, G-STREAM, N-PROGRESS, G-RACE, and INTEGRATION passed. |
| P02.4 cache/accounting | Complete | Owner/version isolation, replay admission, concurrency, and provider/result accounting reviewed; G-RUNTIME and INTEGRATION passed. |
| P02.5 artifact lifecycle | Complete | Publication/deletion journal, grace, crash convergence, Garage and inventory reviewed; managed TEST-060 and artifact integration cases passed. |
| P02.6 findings and repairs | Complete | No confirmed serious defect in reviewed scope; no code repair required. Five bounded records and Docker network troubleshooting are in `docs/codebase-reduction-results.md`. |
| P03.1 size baseline | Complete | Clean post-repair source `81206faa09d3da2289d956716475e20cccb9bd0a`; frozen tracked-path manifest and category totals are recorded in `docs/codebase-reduction-results.md`. |
| P03.2 context baseline | Complete | Three whole-file task sets and per-category byte/line totals are frozen in `docs/codebase-reduction-manifest.json`. |
| P03.3 deletion inventory | Complete | The six original candidates and their evidence are recorded in `docs/codebase-reduction-results.md`. |
| P03.4 focused-context candidate review | Complete | CR-A04 missed; TEST-284's 433-line contract was appended to the broad repair suite. P04.7 is selected only as a test-context organization candidate; it preserves the existing oracle and must prove a net saving with required fixtures included. |
| P03.5 post-P04.8 broad-context review | Complete | The contracted schema shorthand/validator block and schema-only LiveView cases form a separate owner from workspace draft/history. Draft schema decoding remains in `WorkspaceLive`; the schema helper and focused schema suite are not required to change draft/history behavior. P04.9 measured the frozen context after required files. |
| P04.1 option markup | Complete | WEB-TEST-044/090 pin exact numeric and checkbox matrices; F-WIDGET passed 32 and FAST passed all 10 tasks. Production owner shrank 1,208 bytes / 29 lines; full profile context grew 2,256 bytes from necessary tests. See P04.1 record in the results ledger. |
| P04.2 model-list reuse | Complete | WEB-TEST-044 checks the three rendered consumers across profile/catalog/custom-model changes and separate renders; F-WIDGET/profile-definition passed 31. Production source fell 484 bytes / 22 lines, while the frozen profile context grew 1,940 bytes / 46 lines from test coverage. The final accepted FAST report covers the current source; see results ledger. |
| P04.3 fold assignments | Complete | WEB-TEST-044 exercises full and partial callback assigns, explicit false, nil, invalid, omitted values, and separate target config state. F-WIDGET/profile-definition/embedding passed 34. Production source fell 418 bytes / 17 lines; full profile context grew 989 bytes / 29 lines from regression coverage. The final accepted FAST report covers the current source; see results ledger. |
| P04.4 progress construction | Complete | TEST-284 preserves both callback paths; runtime and focused race tests, N-PROGRESS, and FAST passed. Production source fell 1,005 bytes / 3 lines, but the full runtime context grew 19,454 bytes because the required regression added 20,459 bytes / 433 lines. See results ledger; this is not a context-size win. |
| P04.5 result projections | Rejected with evidence | Attempt/accounting conversions are already shared; origin-only factoring has too little projected saving for added abstraction/test surface. See inventory. |
| P04.6 unreachable residue | Rejected with evidence | No exact unreachable private symbol has been proven; retain supported dynamic boundaries. See inventory. |
| P04.7 progress test context | Complete | TEST-284 is isolated and its focused context fell 12,810 bytes / 120 lines. Focused Go and runtime race checks passed. After recording and correcting the 100 ms LiveView handshake failure, hosted FAST run 35818091415 passed all 10 tasks on `a6a7bdd` with 247 frontend tests; the stale-loading failure did not recur but remains unexplained. Browser-free hosted release run 35818416126 then passed all 28 tasks on that same source. |
| P04.8 profile editor test context | Complete | Three editor contract tests moved with their exact test/helper bodies; all 17 old/new component tests pass and the focused task context fell 161,788 bytes / 4,737 lines. The frozen broad profile context grew 707 bytes / 19 lines against the P04.7 tree and 5,892 bytes / 164 lines against the P03 baseline; CR-A04 remains unmet. Hosted FAST run 35821407815 and browser-free release run 35821617071 passed on `bafa622` with 247 frontend tests. The local FAST timeout remains recorded. |
| P04.9 workspace schema ownership | Complete | `WorkspaceSchema` owns shorthand conversion and contracted-subset validation; state decoding, UI/status, persistence, and Run lifecycle remain in `WorkspaceLive`. WEB-TEST-057 and invalid-local-JSON coverage moved unchanged to the focused schema suite. Workspace draft/history context is 491,960 bytes / 14,336 lines (-14,097 / -456 from P03); all three contexts are in `contexts-after-p04.9.json`. The combined workspace/schema suite passed 57 tests, the canonical schema suite passed 2 tests, and formatting passed. Exact source `cf14628` passed hosted FAST run 35870390854 (10/10 tasks) and hosted browser-free RELEASE run 35870717879 (28 release tasks plus one integration and one integration-race task). Two failed local FAST attempts under heavy host load are retained in the results ledger; assertions and deadlines were not changed. |
| P05.1 final verification | Complete | P04.9 exact source `cf14628` passed hosted FAST run 35870390854 and browser-free RELEASE run 35870717879. Release included the 28-task release selector and separate integration/integration-race selectors; runner report hashes are in the results ledger. The full changed-file diff was reviewed; no Go export, OpenAPI, stored schema, migration, provider behavior, or browser hook changed. Browser/provider checks remain opt-in and unrun. |
| P05.2 result/handoff | Complete | Whole-source category totals and P04.9-adjusted contexts are recorded with clean-snapshot SHAs and artifact hashes. Application source `cf14628` is promoted to `main` at `42c64d8`; main FAST 35874387028 and CodeQL 35874386762 passed. CR-A04 remains unmet because two broad context sets grew from required regression coverage; selected candidates are exhausted, so keep that limitation explicit. User declined pre-upgrade backups and accepts persistent-data loss. The deployed-to-candidate diff shows no database migration or Postgres storage-code change; Langfuse telemetry is separate from HardLLM widget history. No backup gate or restore host is required for this release. No production image or deployed component identities exist. |

Allowed status values: `Pending`, `In progress`, `Complete`, or `Rejected with
evidence` for P04 candidates. A blocked prerequisite stays unfinished with the
specific missing evidence; elapsed time is not acceptance.

### 10.2 Per-task handoff template

```text
Task / status / source SHA / branch:
Invariant and exact source symbols:
Change or review finding:
Checks actually run and outcomes (local / hosted / deployed):
Required checks not run and reason:
Before/after production size and required-context size:
Added helper/test/tooling overhead:
Evidence paths/run IDs and retained rollback identity if applicable:
Unresolved issue / concrete next action:
Next task:
```

### 10.3 Prompt for the implementing model

```text
Implement the next eligible task in PLAN-HLLM-CODEBASE-REDUCTION-001.
Read AGENTS.md, the testing guideline, plan sections 1-4, the status table,
and that task's source/tests. Reuse completed evidence and refresh mutable facts.
Explain the invariant and exact deletion/edit before changing code.
Keep the task within its named owner and preserve supported behavior.
Run the specified focused checks and applicable gate; diagnose any failure.
Measure all added helpers/tests as well as removed code; do not claim savings
from file moves, shorter names, weaker tests, or omitted dependencies.
Update the ledger/status and commit a verified checkpoint under branch policy.
Proceed to the next task when its prerequisites and authorization are satisfied.
Do not launch browsers or paid providers. Do not infer production authorization
from this prompt if the user's execution instruction excludes P01 deployment.
If scope or evidence is insufficient, record the precise missing fact and
continue independent authorized preparation rather than inventing completion.
```
