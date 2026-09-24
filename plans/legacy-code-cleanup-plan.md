# Legacy Code Cleanup Plan

## 1. Goal and current scope

- Document ID: `PLAN-HLLM-LEGACY-CLEANUP-001`.
- Status: **Complete**.
- Date: 2026-09-23.
- Goal: remove obsolete compatibility branches and backup instructions while
  preserving current LLM reliability, profile import/export, history, and trace
  behavior.
- Branch: `feat/legacy-cleanup`, based on `origin/main` at `465dc929`.

This is a small, targeted follow-up to `PLAN-HLLM-CODEBASE-REDUCTION-001`.
It does not reopen that plan's completed review or measurement work.

## 2. Decisions and reasoning

The cleanup targets compatibility paths current writers no longer need. It
does not remove a feature merely because that feature spans several files: the
test is whether a supported caller, consumer, or service still depends on it.
For each deletion, preserve the current path, record compatibility costs, and
add a regression at the lowest tier that proves the contract.

| Candidate | Decision | Reasoning and risk |
| --- | --- | --- |
| `repairInvalidOutput` boolean policy | Retire the old field and active UI/runtime path; keep JSON repair through `jsonRepair`. | The old flag applied only to structured calls. On parse or schema-validation failure, it made the failure retryable and sent another LLM request containing the prior output, target schema, and bounded validator feedback. It was LLM-mediated correction, not a local JSON fixer. The legacy loop reused the same target and could repeat repairs up to `maxAttempts`; current `jsonRepair` names the repair target and has finite initial/escalation stages. Keeping the boolean preserves an implicit duplicate policy and extra same-target calls. Deleting it without converting defaults would disable repair, so keep and test the explicit path. Removing exported `RepairInvalidOutput` is a Go source-compatibility break; rejecting the old wire field is an API compatibility break. Saved history requests keep their original JSON as opaque maps; `WEB-TEST-103` proves old request values remain renderable without a legacy recovery-policy decoder. |
| Profile schema v2 and bundle schema v2 input | Require v3 for new profile catalog and bundle imports; keep v3 export/import. | Current writers emit v3 and v2 input is normalized on ingestion. One accepted import shape removes a compatibility branch and gives consumers one format to produce and test. This intentionally rejects old exported files; before deploying the v3-only importer, users who need a v2 bundle must open it with the currently installed version that accepts v2 and re-export it as v3. Keep migration 0008 because it upgrades already-stored policy data; removing import compatibility is not a reason to rewrite applied migrations. |
| Node data backup/restore procedures | Remove active instructions for backing up/restoring node data; add no service, command, snapshot workflow, or restore gate. | The owner accepts loss of persistent application data and wants normal GitHub development. Source inventory found no application backup/restore command or service to delete; the extra operational surface is documented in self-hosting/upgrade guidance. Profile v3 export/import remains profile portability, not a node backup. Langfuse traces remain the trace destination; they do not reconstruct consumer history. Keep the history widget while accepting that its stored entries can be lost with node data. Preserve past release records as historical evidence. |
| Cache-mode handling of `off` | Keep unchanged. | The source has a general fallback to the widget's supported `cache` mode, not a separate `off` migration. API calls still support `off`; removing the fallback would change invalid-input behavior without meaningful deletion. |
| PostgreSQL migrations, including migration 0008 | Keep unchanged. | The ordered migration chain builds a fresh database and upgrades existing installations. Migration 0008 converts stored old recovery-policy rows to current policy data. Rewriting history or replacing the chain would add deployment/data risk without reducing runtime code. |
| `backupProfiles` token in provider-option validation | Keep unchanged. | This is an LLM profile/fallback control name, not application-data backup functionality. Removing it could weaken rejection of misplaced recovery options. |
| Test scheduler, capacity tooling, observability, Langfuse, history APIs, profile export/import | Keep. | These are wired into reliability, statistics/tracing, or consumer flows. Their size alone is not evidence that they are dead. The product goal explicitly includes robust LLM calls, stats/traces, the consumer history widget, and profile portability. |

## 3. Preserved behavior

- JSON repair remains part of structured-call recovery. Preserve the current
  explicit generation-relative repair stages, retry budget, cancellation, and
  disabled branch semantics. The removed boolean no longer permits repeated
  same-target repairs beyond the configured stages.
- Current profile and bundle v3 export/import remains supported.
- Keep run history, trace/stat APIs, Langfuse integration, profile data, auth,
  redaction, and provider execution behavior.
- Keep Git as the normal source rollback mechanism. Git does not restore node
  data; do not add a parallel backup or restore workflow.
- Preserve SQL migration history and historical ADR/plan evidence. Update only
  current contract and operator documentation.
- Do not add services, backup/snapshot code, new dependencies, broad cleanup
  frameworks, or unrelated refactors.

## 4. Phases and acceptance gates

### Phase 1 — Retire the old recovery-policy input

**Why first:** establish one current recovery contract before changing import
formats or operator instructions. JSON repair is user-visible reliability
behavior, so prove its explicit path before removing the obsolete control.

1. Replace internal default uses of `RepairInvalidOutput` with an explicit
   `jsonRepair` plan equivalent to the existing behavior.
2. Remove old-boolean unmarshalling, active UI checkbox/serialization support,
   and the deprecated OpenAPI shape. Keep the explicit JSON repair editor and
   current request/profile shape.
3. Retain old-field decoding only where required to render existing historical
   diagnostics/traces; prove whether such records can still be read from the
   history/artifact path. Do not emit the old field from any writer.
4. Update tests to assert old input rejection, configured-stage bounds, exact
   current behavior, and transport-retry telemetry separately from semantic
   repair. Do not weaken current retry/repair assertions or remove JSON repair
   coverage.

**Primary files:** `internal/retry/retry.go`, `internal/runtime/execute.go`,
`internal/runtime/recovery_execute.go`, `internal/runtime/types.go`,
`internal/providers/payload.go`, `api/openapi.yaml`, profile widget state and
component modules under `frontend/lib/harden_llm_web/`, and their existing Go
and Phoenix tests. Delete `internal/runtime/repair.go` only if its request
builder has no remaining caller. Keep historical decoding in
`frontend/lib/harden_llm/llm_diagnostics_wire.ex` only if saved trace fixtures
prove it is needed for display.

**Accept when:** focused Go retry/runtime/gateway tests and Phoenix widget/API
tests pass; OpenAPI has a single current shape; default structured repair still
uses the bounded current policy.

### Phase 2 — Require current profile formats on import

**Why separate:** this is a distinct consumer contract and migration risk from
recovery execution. Keeping it separate makes rejected v2 imports easy to
diagnose and avoids mixing profile portability changes with run-time behavior.

1. Require profile `schemaVersion: 3` in catalog parsing and bundle
   `schemaVersion: 3` in bundle replacement.
2. Remove v2 normalization constants/acceptance branches. Keep focused negative
   tests proving that v2 profile and bundle imports are rejected.
3. Keep current export shape and round-trip coverage. Keep unrelated widget
   state schema versions and execution-data schema handling unchanged.

**Accept when:** current-format catalog and bundle round trips pass; v2 profile
and bundle inputs fail with clear validation errors; stored v3 records still
load and export. Cover both import boundaries in `internal/profiles` and
`internal/gateway`; do not alter unrelated `ClientState` or run-result schema
compatibility.

### Phase 3 — Remove backup procedures from active docs

**Why documentation-only:** source inventory found no application-owned node
backup tool or service. The manual procedures create an operational expectation
the owner has rejected. Remove that expectation without deleting history,
changing Langfuse, or treating profile export as whole-node restore.

1. Remove the manual node volume/database backup and restore section from
   `docs/self-hosting.md`, plus upgrade/rollback directions that require
   restoring pre-upgrade backups. Keep the accepted data-loss statement,
   forward-only migration warning, and compatible-image rollback boundary.
2. Remove active architecture/environment/preview wording that describes
   node-data backup or restore steps. Keep secret storage, Git hygiene, profile
   portability, and deployment compatibility instructions.
3. Search active setup/upgrade docs for remaining node-data backup or snapshot
   steps. Leave historical rollout records and unrelated LLM fallback uses of
   “backup” intact.

**Accept when:** no active setup/upgrade guide directs operators to make a
node-data backup or snapshot; current setup and compatible-image rollback
instructions remain complete. The current README inventory has no such
instruction, so do not edit README unless a real reference is found.

### Phase 4 — Final verification and record

1. Run focused tests for changed Go and Phoenix owners, then `make test-fast`.
2. Run browser-free release certification only if required by the authorized
   release path; do not launch browser or provider checks.
3. Search remaining old-field/schema references and classify them as rejected
   current input, required database migration, retained historical trace reader,
   or historical documentation.
4. Record exact deletions, tests, compatibility changes, retained readers, and
   post-change checks here and in the code-reduction results ledger.

## 5. Risks and stopping conditions

- Removing v2 import support breaks users who only have v2 exports. Preserve a
  clear validation error and confirm the current v3 export path remains usable.
  Such users must re-export through the previous importer before deployment;
  the new importer cannot convert v2 files.
- Removing the old repair boolean from runtime can disable repair if defaults or
  callers are incompletely converted. Stop if any current writer or default
  emits it, or if the configured current repair stages fail. The old repeated
  same-target behavior is intentionally retired and must be called out as a
  compatibility break, not described as equivalent. Removing an exported Go
  field also needs an explicit source-compatibility note.
- Do not remove the read-only old trace decoder until history fixtures and
  stored-record shape prove it is unnecessary; traces/history must remain
  viewable.
- Do not equate Langfuse traces with consumer history. Preserve both features;
  accept only the stated loss of node-persisted data if the node is lost.
- The shared-config rotation guide's private `.env`/profile-config rollback
  instruction covers deliberate configuration changes, not node-data recovery.
  This cleanup does not rotate secrets, so leave that procedure and its rollout
  records unchanged.
- Do not edit or squash historical SQL migrations. If schema migration work
  becomes necessary, stop and revise this plan before changing production data.
- Do not touch the root checkout's unrelated edits to `docker-compose.yml` or
  `scripts/test/preview_policy_test.mjs`; implementation is isolated in the
  clean worktree.

## 6. Progress log

| Phase | Status | Evidence / next action |
| --- | --- | --- |
| Plan and source inventory | Complete | The legacy boolean still drives the old runtime loop and UI; current `jsonRepair` uses explicit stages. Profile writers emit v3; import paths accept v2. Source inventory found no node backup command/service; manual node procedures are in self-hosting/upgrade documentation. The current README has no matching backup instruction. Cache fallback is not a dedicated migration. |
| Phase 1 | Complete | Removed Go runtime and Phoenix UI handling for `repairInvalidOutput`, kept current `jsonRepair`, and kept old saved trace requests readable as opaque historical data (`WEB-TEST-103`). Focused Go recovery/runtime/provider/gateway tests passed; focused Phoenix/API/widget suites passed (132 tests). |
| Phase 2 | Complete | Removed v2 catalog and bundle normalization. New red tests failed against the old code, then passed after the change. `go test ./internal/profiles ./internal/gateway -count=1` passes; current catalog round trip is retained. `make verify` passed the Docker-backed `TestResourceRoutes` bundle export/import boundary. |
| Phase 3 | Complete | Removed the self-hosted node snapshot/restore recipe and backup-dependent rollback steps; removed encrypted-host-backup wording from the environment reference and the restore-together/backup-domain wording from architecture. Updated preview rollback wording. Active setup/upgrade search found only explicit no-restore statements, image rollback behavior, and history-widget restoration. |
| Phase 4 | Local certification complete; hosted gate pending | Local `make test-fast`, `make verify`, and the corrected-source browser-free release selector passed. The first exact-main hosted release failed one frontend test because a coalesced state save left a correctly disabled fold control locked when clicked. The test now waits for the save lock to clear without changing its click or assertions; focused and full frontend tests plus `make test-fast` pass on the follow-up branch. Repeat hosted browser-free release certification on the corrected exact-main SHA before production. See the exact-source follow-up below. |
| Exact-source release correction | Local certification complete; PR and hosted gate pending | GitHub run [35936283153](https://github.com/prls-co/harden-llm/actions/runs/35936283153) failed at `frontend-deterministic` on merged cleanup SHA `8ee2dc89136163ea6525193ea049881e6363cb40` (247/248 passed). The bounded readiness wait is in `frontend/test/harden_llm_web/live/workspace_live_test.exs`; focused test, full frontend suite (248 passed), all 10 `make test-fast` tasks, and the full 28-task local browser-free release selector pass on the follow-up source. The corrected source still needs PR checks and exact-main hosted release certification before production. |

## 7. Verification and troubleshooting record

- An initial `make test-fast` found two root-package tests whose valid profile
  fixtures still used schema v2. Updated the shared test profile and Compose
  smoke fixture to v3; the v2 rejection tests remain. A subsequent run exposed
  a duplicate-DOM-ID test regex that also matched the `id` suffix in
  `phx-value-node-id`; tightened it to match an actual whitespace-delimited
  `id` attribute. The uniqueness assertion was not weakened. Final fast gate:
  all 10 tasks accepted.
- The first `make verify` stopped before Go integration because Docker could
  not create a test network. Docker inventory showed all default bridge ranges
  occupied and an old `harden-llm-test-*` network with no attached containers
  or matching test-resource receipt. Removed only that orphaned test network;
  application networks and containers were untouched. The next complete
  `make verify` passed, including integration and integration-race tasks.
- The first release-gate attempt stopped at frontend preflight because this
  isolated worktree had no Mix dependencies. `mix deps.get` installed the
  lockfile-pinned dependencies without changing tracked files. The first
  Compose smoke then found Docker's predefined address pool full. Inventory
  identified `harden-llm-smoke-4167581-1790110522106137767_harden-private`
  (`192.168.224.0/20`), created on 2026-09-22 with zero endpoints and no
  matching containers or active Compose project. Removed only this empty,
  test-owned network; the older smoke project with 14 running containers was
  left untouched.
- A parallel release-gate retry passed Compose startup but exposed one
  timing-sensitive `TEST-049` fixture-start wait (two-second start signal);
  the same test passed in isolation in 256 ms. No test assertion or timeout
  was changed. The complete browser-free release selector was then rerun with
  one CPU candidate slot and accepted all 28 tasks, with zero timeouts,
  failures, cleanup errors, or cleanup warnings. Its report is
  `tmp/test-feedback/runner-1790207427332-3776603-5de85572d0348ab7.json`.
- `govulncheck` reported no vulnerabilities reachable from this code and three
  vulnerabilities in required modules that the code does not call. The gate
  passed; revisit only if dependency usage or reachability changes.
- Remaining legacy-field references were classified: current tests reject the
  old run-policy field; migrations 0006/0008 and their fixtures verify stored
  policy conversion; `WEB-TEST-103` preserves opaque historical request display;
  ADRs and plans retain historical contracts. `backupProfiles` and fallback UI
  references concern LLM routing; schema v2 uses in ClientState/run/capacity
  formats are outside profile import and remain unchanged.
- The node-data backup search found no application backup command or service.
  The shared-config rotation guide still describes private config rollback and
  historical releases keep their evidence; neither is a node snapshot path.
- The changed application-source and OpenAPI files show 93 added and 215
  removed lines by `git diff --numstat`, a net reduction of 122 lines. Tests
  and this plan add regression context, so this is not a whole-repository
  line-count reduction claim.
- No browser, deployed-browser, or provider test was run. This cleanup did not
  change deployment state, production configuration, secrets, or Langfuse.
- Current profile and bundle import is intentionally v3-only. A needed v2
  profile bundle must be opened and re-exported through the current importer
  before the v3-only importer is deployed. After deployment, check that v3
  import/export and configured JSON repair behave normally.

### Exact-source release follow-up — 2026-09-23

The first hosted release workflow on the merged cleanup SHA failed in
`frontend-deterministic`, with 247 of 248 tests passing. The failure was in
`WorkspaceLiveTest` (`translates shorthand schemas, persists folds, and sends
bounded retry controls`): `render_async/2` waited for the async state-save PIDs
that existed when called, while completion of one save started the coalesced
pending save. The test then clicked `#model-config-toggle` while
`state_save_in_flight` correctly kept it disabled. The HEEx lock and application
save behavior were left unchanged.

The test now waits up to five seconds for the control to become enabled, using
`render_async/2` to wait for each in-flight save. It retains the original click
and all subsequent assertions. Evidence on the follow-up source:

- Focused test with the hosted scheduler setting (`ERL_FLAGS='+S 4:4'`,
  `max_cases: 8`): 1 passed.
- Full deterministic Phoenix suite with that scheduler setting: 248 passed,
  five opt-in tests excluded.
- `make test-fast`: all 10 tasks accepted with no cleanup error or warning.
- `node scripts/run-test-tier.mjs --task release --candidate-slots 1`: all 28
  tasks accepted, with no timeout, failure, cleanup error, or warning. Report:
  `tmp/test-feedback/runner-1790210903236-814076-372cf4620d82df8c.json`.

The correction changes a test only. It does not weaken assertions or alter
product behavior. Phase 4 and deployment remain open until PR checks and
browser-free hosted release certification succeed on the corrected exact-main
SHA; browser and live provider checks remain outside authorization.

## 8. Post-change checks for the operator

- Before deployment, re-export any needed v2 profile bundle through the current
  importer; the new importer rejects v2.
- After deployment, verify profile v3 export/import and configured JSON repair.
- Confirm production profile defaults still display and perform JSON repair for
  invalid structured responses.
- Confirm the history widget and trace/stat display still load historical and
  new records.
- Existing Langfuse trace delivery remains in place; this cleanup adds no
  separate trace backup path or changes to Langfuse retention settings.
