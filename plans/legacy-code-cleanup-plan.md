# Legacy Code Cleanup Plan

## 1. Goal and current scope

- Document ID: `PLAN-HLLM-LEGACY-CLEANUP-001`.
- Status: **Complete**.
- Date: 2026-09-23.
- Goal: remove obsolete compatibility branches and backup instructions while
  preserving current LLM reliability, profile import/export, history, and trace
  behavior.
- Implementation merged through PRs [#50](https://github.com/prls-co/harden-llm/pull/50)
  and [#51](https://github.com/prls-co/harden-llm/pull/51). Production release
  source: `main` at `d581942634b7197cea2d09069f73ebc89ff75bce`.

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
| Profile schema v2 and bundle schema v2 input | Require v3 for new profile catalog and bundle imports; keep v3 export/import. | Current writers emit v3 and v2 input was normalized on ingestion. One accepted import shape removes a compatibility branch and gives consumers one format to produce and test. This intentionally rejects old exported files; users who need a v2 bundle must open it with a v2-capable importer and re-export it as v3 before using this importer. Keep migration 0008 because it upgrades already-stored policy data; removing import compatibility is not a reason to rewrite applied migrations. |
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
| Phase 4 | Complete | Local `make test-fast`, `make verify`, and 28-task browser-free release selector passed. Exact-main hosted `make test-release` passed on `d581942634b7197cea2d09069f73ebc89ff75bce` in run [35940427921](https://github.com/prls-co/harden-llm/actions/runs/35940427921). Production deployment and post-deploy checks passed; details follow. |
| Exact-source release correction | Complete | PR #51 merged the test-only coalesced-save wait as `d581942634b7197cea2d09069f73ebc89ff75bce`; exact-main hosted release passed, then this source was deployed. No production behavior or test assertions were weakened. |

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
- No browser, deployed-browser, or provider test was run. At this verification
  checkpoint, deployment had not yet occurred; the completed exact-main release
  is recorded below. No secrets or Langfuse configuration were changed.
- Current profile and bundle import is intentionally v3-only. A needed v2
  profile bundle must be opened and re-exported through a v2-capable importer
  before using this v3-only importer. After deployment, check that v3
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
product behavior. PR #51 merged the fix as `d581942634b7197cea2d09069f73ebc89ff75bce`.
Exact-main hosted browser-free release run [35940427921](https://github.com/prls-co/harden-llm/actions/runs/35940427921)
passed on attempt 1. The production rollout on that exact source and its
browser-free checks are recorded below. Browser and live-provider checks were
not authorized or run.

### Production closeout — 2026-09-23

The cleanup was deployed from the exact hosted-certified main SHA
`d581942634b7197cea2d09069f73ebc89ff75bce`.

- Production images are local immutable tags built from that source:
  `harden-llm-web:release-d581942634b7197cea2d09069f73ebc89ff75bce`
  (`sha256:f989a6fdca2b27aa008d4b70100d9911bd6a4278dda3d0e1b71f7e889fd83d38`)
  and `harden-llm-gateway:release-d581942634b7197cea2d09069f73ebc89ff75bce`
  (`sha256:75f237ed40ec23ddc31b14ddcff78f008e2630a22fc9bd4debc8c99ff33bb01a`),
  both `linux/amd64`.
- Scoped `production-config apply` targeted `harden-llm-web` and
  `harden-llm-gateway` with expected release
  `d581942634b7197cea2d09069f73ebc89ff75bce`. It changed only their image,
  image override, and release-identity fields.
  Candidate-scoped and full five-service `production-config check` both report
  `equivalent`. Both new containers are healthy with zero restarts. The
  previous app release `cf14628eef80312fe0b03fefd3bed2d3e301860c` remains
  available for rollback: web image
  `sha256:e9073dc42e47131fdd4336557a801f4f268f5f0f115e6c09309f67864ce8a5af`,
  gateway image
  `sha256:99bb8fc0f5b26a83fa9b735545df2eb976f033479537c6d725a2343be02719ff`.
- Public web `/healthz` and `/login`, and API `/healthz` and `/readyz`, each
  returned HTTP 200. Unauthenticated profile access returned the expected
  HTTP 401. Read-only guest checks returned HTTP 200 for login, state, profiles,
  history, stats, and logout; the state is schema v3 without
  `repairInvalidOutput`, all 32 profiles are schema v3, one history item loaded,
  and stats use schema v2.
- Read-only `audit-artifacts` reported five scanned objects, five metadata
  references, five referenced objects, zero active-operation references, zero
  missing or unreferenced objects, `truncated=false`, and `healthy=true`.
- Production descriptor SHA-256 changed from
  `ca4f52b2cfeac5cf1357ae006883b0a86d3db7827260121610842b5b50f17dab` to
  `8ca1993d56c8ae468982dc472e0f67a93e8b8718c67d560855e5f5dc78063263`;
  permissions remain `0600`. A private descriptor-only pre-deploy copy is at
  `/home/kirill/.config/harden-llm/rollout-d581942/production.before.json`
  (`0600`, parent `0700`, SHA-256
  `ca4f52b2cfeac5cf1357ae006883b0a86d3db7827260121610842b5b50f17dab`). It
  contains deployment metadata, not Postgres/Garage application data. No
  persistent-data snapshot or restore rehearsal was made.
- No database migration or storage-code change was part of this release.
  Langfuse configuration and trace retention were unchanged. The consumer
  history remains node-persisted and can be lost with that data store, as
  accepted by the owner.

Remaining checks are ordinary post-release observation. Browser layout and
real-provider behavior were not exercised. Users holding old v2 profile files
must convert them with a v2-capable importer before using the v3-only importer;
production currently has 32 configured v3 profiles. A future v2-conversion
request is a compatibility request, not a failed current release.

## 8. Post-deployment checks and known limits

- Verified after deployment: the current production profile set and state use
  schema v3; profile, history, and stats endpoints respond successfully; the
  old `repairInvalidOutput` state field is absent. Bounded `jsonRepair` remains
  in the current source. No real provider call was made, so provider-generated
  repair behavior was not re-exercised during deployment.
- Verified after deployment: history and stats load through the guest API, and
  the read-only artifact inventory is healthy. Browser layout was not checked.
- Langfuse trace delivery/configuration was untouched. Langfuse traces do not
  reconstruct the consumer's node-persisted History after data-store loss.
- The user accepts persistent-data loss. No application-data backup, snapshot,
  or restore procedure is part of this plan. The private descriptor-only copy
  above supports restoring deployment metadata and does not contain node data.
- Current profile catalog and bundle imports require v3. Users with v2 exports
  need a v2-capable importer to re-export those files before importing them in
  this release.
