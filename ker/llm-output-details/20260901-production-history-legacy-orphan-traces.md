Known Error Record: Retained production history contains legacy runless traces and incomplete identity

KER slug: 20260901-production-history-legacy-orphan-traces
Git reference: fcda74b3824fc22a517df709f0a67939b8aa0b9c (application release where observed)
Resolution status: Resolved
Applies to (scope): Retained self-hosted production PostgreSQL data created before the atomic execution and immutable-identity release; exact counts must be refreshed before remediation
Tags: postgres, history, legacy-schema, orphan-trace, artifact-metadata, reconciliation
Anomaly classification (IEEE 1044–inspired, lightweight):
  - anomaly type: integration
  - reproducibility: always
  - impact: medium
  - likely cause category: code

Trigger patterns (for fast matching)
- a row in `llm_traces` has no matching `(owner_id, trace_id)` in `llm_runs`
- trace request/response resources report unavailable because `RunByTrace` returns not found
- retained `llm_runs.result` lacks `profileId`, `modelId`, `provider`, `apiInferenceType`, or `providerBaseUrl`

Problem Record (conceptual guidance)

Symptoms
- The 2026-08-29 production audit found 44 runs and 87 traces, including 43 traces without matching runs.
- Those runless traces retained 43 artifact metadata rows and 39 observations.
- All 44 retained runs used the older result shape and lacked the complete newer run identity fields.
- Historical trace details therefore cannot consistently show model/provider/protocol/base URL, and runless traces cannot expose persisted request/response resources.

Likely causes (ranked mental model)
1) Retained rows were created by the former history-deletion path, which deleted `llm_runs` but left matching `llm_traces`, observations, and artifact metadata. Atomic `SaveExecution` existed; coordinated deletion did not.
2) Schema evolution added identity inside JSON documents without a data backfill, so structurally valid rows remain semantically incomplete.
3) The read path contains a standalone/imported-trace compatibility comment, but there is no public trace creation/import endpoint. Classification must still fail closed before deletion because exact per-row lineage is not retained.

Diagnostic decision path
1) Check: Count run/trace relationship states by owner and age.
   How: Run a read-only PostgreSQL query joining `llm_traces` to `llm_runs` on owner and trace ID, grouped by owner and creation period.
   If true: Runless traces exist outside expected imported/domain-only categories.
   Next step: Produce a dry-run reconciliation report with trace, observation, artifact, and object-key counts.

2) Check: Measure legacy identity completeness.
   How: Query `jsonb_typeof(result)` and presence of `profileId`, `modelId`, `provider`, `apiInferenceType`, and `providerBaseUrl` in `llm_runs.result`; compare trace record fields separately.
   If true: Historical rows cannot satisfy the new details contract.
   Next step: Backfill only values that are immutable and derivable from the same persisted run/trace document; mark the rest unavailable.

3) Check: Confirm forward-path protection.
   How: Run the atomic rollback, delete failure, clear orphan, and owner-isolation tests in `internal/postgres/repository_test.go` and `internal/gateway/resource_routes_test.go`.
   If true: New ordinary writes/deletes are protected, but retained data still needs one-time handling.
   Next step: Keep remediation separate from the normal request path.

Evidence from this incident
- key error excerpt:
  Production audit on 2026-08-29: `runs=44`, `traces=87`, `run_without_trace=0`, `trace_without_run=43`, `artifacts_on_orphan_traces=43`, `observations_on_orphan_traces=39`.
  Identity audit: `missing_run_identity=44`, `runs_new_identity_schema=0`.
- logs / files involved: Read-only production SQL results from the audit conversation; no credential values or provider output were retained. Refresh is required because these counts are time-sensitive.
- code / config areas involved: `internal/postgres/resources.go`, `internal/postgres/repository.go`, `internal/postgres/migrations/0001_application.sql`, `internal/postgres/migrations/0002_remove_stats_projection.sql`, `internal/gateway/resources.go`
- what did NOT work:
  Atomic `SaveExecution` -> protected writes but could not compensate for the former run-only history deletion path.
  Rendering missing fields as unavailable -> avoided fabrication but did not reconcile storage or restore historical identity.

Decisions and tradeoffs
This section captures WHY choices were made, including rejected options.

Options considered
- option: Bounded idempotent reconciliation with dry-run evidence
  description: Classify runless traces, report Garage object references, backfill only derivable identity, and remove only records proven stale under an explicit owner-scoped policy.
  pros: Auditable, rerunnable, safe for retained data, and separates migration from request handling.
  cons / risks: Requires classification rules and backup/restore evidence before deletion.
  decision: accepted
  rationale: The data set is finite and safety matters more than automatic guessing.

- option: Delete every trace without a run
  description: Treat the join absence as proof of garbage.
  pros: Simple and immediately reduces counts.
  cons / risks: Can destroy imported or domain-only traces that the API explicitly permits.
  decision: rejected
  rationale: Relationship absence alone does not prove intent.

- option: Reconstruct identity from current profile rows
  description: Fill missing historical model/provider values from the current profile catalog.
  pros: Produces complete-looking rows.
  cons / risks: Current profiles are mutable and may not match execution-time configuration.
  decision: rejected
  rationale: Fabricated historical identity is worse than an explicit unavailable value.

Key constraints influencing decisions
- Production data deletion requires exact scope, backup, dry-run counts, and restore confidence.
- Credentials and provider payloads must never enter reconciliation output.
- Application PostgreSQL and Garage are authoritative; telemetry stores are not migration sources of truth.

Non-obvious context
- `Clear History` already removes owner-scoped runless traces, but it is a broad user-visible destructive operation rather than a safe global migration.
- The trace API intentionally supports domain traces without run request/response data, so not every runless trace is automatically invalid.
- Forward-path tests do not certify historical data cleanliness.

Workarounds
- Render unavailable historical identity and request/response states explicitly.
- An owner can use Clear History when complete owner-scoped history removal is intended; do not use it as an implicit migration.

Known Error Record (what actually worked)

Root cause (best current understanding)
- Production retained data spans the former run-only history deletion behavior and older unversioned result/trace JSON contracts. The 43 observed runless records are structurally consistent with deleted execution residue, while exact per-row deletion lineage is unavailable.
- New coordinated deletion protects the ordinary path, but the database still lacks a run-to-trace foreign key and exported independent writers remain available, so forward protection is procedural rather than structural.

Permanent fix
1) Refresh read-only counts and produce an owner-scoped, redacted dry-run reconciliation manifest.
2) Define explicit classifications for stale deleted-run traces, any proven standalone traces, derivable schema metadata, and unavailable execution identity.
3) Back up PostgreSQL and Garage metadata/object inventories and prove isolated restore before deletion.
4) Run an idempotent reconciliation that backfills only immutable derivable fields and deletes only proven stale metadata and matching objects.
5) Enforce run-owned trace lifecycle structurally, remove independent production writers, and version persisted run/trace documents.
6) Re-run referential, object-existence, identity-completeness, owner-isolation, and widget-history checks; retain a redacted report.

Verification
How to confirm the fix:
  Execute the reconciler in dry-run and apply modes against a restored production snapshot, then run read-only production invariants after approved rollout.
Expected result:
  Every remaining runless trace is classified as intentional, every artifact metadata row points to an expected object, legacy identity is either safely backfilled or explicitly unavailable, and repeated apply reports no changes.

Prevention / guardrails
- Add a scheduled read-only invariant report for run/trace/object relationships and JSON contract versions.
- Version run and trace result documents explicitly and test mixed-version rendering.
- Require migration/reconciliation evidence whenever a product JSON contract gains required historical fields.

Principal-engineer resolution plan (2026-09-01)

Deep-RCA evidence and corrections
- Git history identifies the historical writer/deleter asymmetry: the initial REST gateway already persisted execution rows atomically, while the pre-`fcda74b` `DeleteHistory`/`ClearHistory` path deleted only `llm_runs`. Coordinated `DeleteExecution`/`ClearExecutions` begins at `internal/postgres/resources.go:307-349` in current code.
- The schema at `internal/postgres/migrations/0001_application.sql:52-103` has separate run and trace tables with no run-to-trace foreign key. Observations and artifacts depend on traces, so deleting only a run left an internally consistent trace subtree.
- Independent `SaveRun`, `SaveTrace`, and `SaveArtifact` methods remain exported in `internal/postgres/repository.go`; current production uses `SaveExecution`, but tests can still construct states the production model intends to forbid.
- The trace read fallback at `internal/gateway/resources.go:359-394` tolerates a trace without a run, although no public trace-import/create endpoint establishes a separately owned trace lifecycle. `ClearExecutions` removes owner-scoped standalone traces, which further indicates that runs—not traces—are the intended aggregate root.
- Run results have no explicit schema discriminator, and trace documents retained numeric `schemaVersion: 1` after their field set evolved. Missing identity is currently omitted by the component, not explicitly labeled unavailable.
- The refreshed read-only 2026-09-01 audit still found 44 runs, 87 traces, and 43 runless traces. All 43 carry execution-like run/call/usage/cache data and trace artifacts; this strongly supports deleted-run residue. Exact row lineage remains unprovable without historical request logs, so apply mode must still fail closed on any unclassified row.

Target relational and document architecture
1) Treat one LLM execution as a PostgreSQL aggregate rooted at `llm_runs`:
   `llm_runs -> llm_traces -> llm_trace_observations / llm_artifacts`.
2) Add `run_id NOT NULL` to `llm_traces`, a unique owner/run/trace key on runs, and a composite owner/run/trace foreign key from traces with `ON DELETE CASCADE`.
3) Reorder `SaveExecution` to insert the run then trace/children inside the same transaction. Delete the run as the single relational deletion; PostgreSQL owns metadata cascade.
4) Remove exported independent run/trace/artifact production writers. Test fixtures create complete executions through `SaveExecution` or explicitly named corruption helpers scoped to reconciliation tests.
5) Do not support standalone/imported traces until a real producer API, authorization model, retention contract, and test catalog exist. Remove the permissive missing-run read fallback after reconciliation and migration.
6) Version persisted execution/result and trace documents as v2 after the execution-identity KER defines the canonical fields. One reader normalizes v1/v2; v1 identity is rendered as `not captured`, never reconstructed.
7) Garage body deletion remains coordinated by the artifact lifecycle design in the cross-store KER. The relational foreign key cannot solve object-store consistency by itself.

Reconciliation command design
- Implement a bounded administrative subcommand in the existing gateway binary; do not add a new service or a perpetual cleanup owner.
- Dry-run is the default. Scope is one owner unless `--all-owners` is explicitly provided.
- Classify each row using the owner/run join, self-consistent IDs, document version, observation sequence, artifact kind, and canonical object-key structure. Any unknown classification stops apply.
- Verify referenced Garage objects through bounded integrity-aware reads without printing content, IDs, owner values, or object keys.
- Emit redacted counts plus a deterministic plan digest. Apply requires the exact digest and rechecks that the classified rows have not changed.
- Delete object bodies through the artifact coordinator first, then conditionally remove the exact trace rows. A second apply must report no changes.
- Retain the 44 matched runs as explicit v1. Backfill only schema classification/derived facts that are provable from the same immutable document; do not fabricate execution identity.

Detailed implementation sequence
1) Amend `plans/from_utility-llm/self-hosted-go-stack-spec.md`, the backend test catalog, and `api/openapi.yaml` to define the execution aggregate and remove unsupported standalone-trace semantics.
2) Complete the execution-identity contract first so persisted v2 has one stable definition.
3) Add pure v1/v2 decoding and historical classification fixtures, including unknown rows that must fail closed and deterministic digest tests.
4) Add `internal/postgres/history_reconciliation.go`, `internal/gateway/command/history_reconciliation.go`, and gateway command routing for dry-run/apply.
5) Rehearse against matching restored PostgreSQL and Garage snapshots; prove dry-run, apply, second-apply no-op, owner isolation, redacted output, and rollback prerequisites.
6) During an approved maintenance window, quiesce writes, take tested backups, refresh the digest, apply the production cleanup, and record post-apply invariants.
7) Add the relational ownership migration only after no unclassified/runless trace remains. Populate `llm_traces.run_id`, add keys/FK/cascade, and reject null/runless rows.
8) Reorder `SaveExecution`, remove independent writer APIs, simplify relational deletion, and remove the missing-run compatibility read path.
9) Cut over typed version-aware OpenAPI and Phoenix presentation; v1 identity must say `not captured`.

Test and production certification matrix
- T0: historical classifier, v1/v2 decoding, deterministic digest, unknown-state fail-closed behavior, and static prohibition of independent production writers.
- T1: admin dry-run/apply with fake stores, owner isolation, redaction, mixed-version APIs, and integrity failure on a missing run.
- T2: not applicable.
- T3: real PostgreSQL migration/FK/cascade, restored legacy fixtures, real Garage verification, failure between object and SQL steps, and idempotent retry through the artifact coordinator.
- T4: browser rendering of explicit v1 `not captured` identity and normal v2 trace resources.
- T5: restored production snapshot rehearsal, `make test-release`, exact-image deployment, authenticated history/stats/trace canary, and recorded post-production invariant report.

Rollout gates and exit criteria
- Require proven PostgreSQL and Garage restore, quiesced writes, approved plan digest, and zero unclassified rows before destructive apply.
- After apply and schema migration require: zero runs without traces, zero traces without runs, one owner/run binding per trace, every artifact metadata row integrity-valid in Garage, and a no-op second reconciliation.
- Product stats remain PostgreSQL-derived. Tempo, Langfuse, Laminar, Prometheus, Loki, and ClickHouse provide operational evidence only and cannot classify or backfill retained product rows.
- This KER closes only after the retained set is reconciled, structural ownership prevents recurrence, mixed-version presentation is explicit, and exact deployed revision evidence is recorded.

Implementation checkpoint (2026-09-01)
- The existing gateway binary now owns a bounded `reconcile-history` command.
  Dry-run is mandatory by default, reports only redacted counts plus a
  deterministic digest, and apply requires the unchanged digest and explicit
  owner or all-owner scope.
- Classification accepts only self-consistent execution-like v1 trace subtrees,
  contiguous observations, canonical owner/run/trace artifact paths, no live
  run identity, and integrity-matching Garage objects. Unknown or truncated
  plans fail closed.
- Apply routes each object deletion through the durable artifact coordinator,
  rechecks an exact subtree fingerprint under the owner lock, and is idempotent.
  TEST-061 covers deterministic plans, changed-plan rejection, apply, and a
  no-op second apply.
- The restored production snapshot and live production were reconciled on
  2026-09-01: 43/43 classified runless traces were removed through exact
  artifact batches, the repeated apply was a no-op, and post-state was 44 runs,
  44 traces, zero runless traces, 46 available artifacts, and zero pending
  operations. Matching PostgreSQL and Garage backups and isolated restore proof
  were retained under ignored release evidence.
- Migration 0005 now rejects runless or mismatched bindings, makes
  `llm_traces.run_id` mandatory, and adds the exact cascading
  owner/run/trace foreign key. `SaveExecution` inserts the run first;
  independent production writers and the missing-run trace read fallback are
  removed. The same binary bounds `reconcile-history` at migration 4 so a
  direct upgrade can satisfy migration 5 without a legacy server image.

Final resolution (2026-09-01)
- `62898a0df330ff6df6f11ae16c28ad4ce4d9777c` made retained deletion batches
  isolated and repeatable. The production rehearsal used a fresh encrypted
  Postgres/Garage backup with isolated restore proof before applying cleanup.
- `dde9833e7543f97314a261a2ad7af0805c382433` removed independent writers and
  missing-run read fallback, made `SaveExecution` the sole aggregate writer,
  and added migration 5's non-null run ownership plus exact cascading
  owner/run/trace foreign key. Production reconciliation removed 43 classified
  runless legacy traces; its second apply was a no-op.
- `ed76eac0f003f10ce394393f301b8f020971be25` added the bounded OpenAPI v1/v2
  read union for historical Go zero values and proved every retained production
  history and trace document decodes without fabrication.
- `c1d448b8ca58aa2bed239a5d91f3bce10998ffb9` and
  `ebd8a4f2309b372a43eaf258dc0a53cfadd4b995` make refresh and deletion
  completion order deterministic, including reversible optimistic deletion.
- Production has zero runless traces, zero run/trace ownership mismatches, and
  no smoke residue after certification. Exact closure evidence is on issue
  `#46`.
