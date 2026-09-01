Known Error Record: Artifact bodies and PostgreSQL metadata have cross-store crash windows

KER slug: 20260901-artifact-metadata-cross-store-crash-window
Git reference: fcda74b3824fc22a517df709f0a67939b8aa0b9c (application release where observed)
Resolution status: Open
Applies to (scope): Self-hosted run persistence and history deletion spanning Garage object storage and application PostgreSQL
Tags: garage, postgres, artifacts, crash-consistency, reconciliation, persistence
Anomaly classification (IEEE 1044–inspired, lightweight):
  - anomaly type: integration
  - reproducibility: unknown
  - impact: medium
  - likely cause category: ordering

Trigger patterns (for fast matching)
- a Garage trace artifact exists without a corresponding `llm_artifacts` row after interrupted run persistence
- `llm_artifacts.available = true` but the Garage object returns not found after interrupted deletion
- process termination occurs between Garage mutation and PostgreSQL commit

Problem Record (conceptual guidance)

Symptoms
- Normal PostgreSQL failure after upload invokes best-effort Garage cleanup, but a hard crash before that cleanup can leave an unreferenced object.
- History deletion removes Garage objects before transactionally deleting PostgreSQL run/trace metadata; a crash or database failure afterward can leave available metadata pointing to missing bodies.
- PostgreSQL is internally atomic for run, trace, observations, and artifact references, but that transaction cannot include Garage.

Likely causes (ranked mental model)
1) The workflow crosses two stores without a durable operation state or reconciler; no local transaction can atomically commit both.
2) Correct fail-closed ordering prevents metadata deletion when Garage deletion fails, but cannot eliminate termination between successful object deletion and database commit.
3) Existing cleanup handles returned errors, not process death, host loss, or an indeterminate network response.

Diagnostic decision path
1) Check: Compare available artifact metadata to Garage object existence.
   How: Enumerate owner-scoped `llm_artifacts` object keys read-only and issue bounded metadata/head checks through the owner-scoped Garage client.
   If true: Metadata refers to a missing body.
   Next step: Mark/report the record unavailable and reconcile according to a recorded policy; do not fabricate content.

2) Check: Find objects with no PostgreSQL reference.
   How: Compare a bounded Garage prefix inventory with normalized object keys from `llm_artifacts` without printing object contents.
   If true: An upload may have escaped metadata commit or an older lifecycle path.
   Next step: Quarantine or delete only after age, ownership, and backup rules pass.

3) Check: Exercise both termination windows deterministically.
   How: Add failpoints after Garage upload/before `SaveExecution` commit and after `DeleteMany`/before `DeleteExecution` commit in an isolated T3 fixture.
   If true: The invariant report detects both object-only and metadata-only states.
   Next step: Verify idempotent reconciliation and retry behavior.

Evidence from this incident
- key error excerpt:
  Specification: `A Postgres failure after upload is logged as a possible orphan; v1 does not add an orphan sweeper.`
  Delete path: `DeleteMany(ctx, keys)` completes before `DeleteExecution(...)` starts its PostgreSQL transaction.
- logs / files involved: No confirmed hard-crash event was observed; this is a source-confirmed consistency window. How to confirm incidence: run the inventory comparisons above.
- code / config areas involved: `internal/gateway/run_service.go`, `internal/gateway/resources.go`, `internal/postgres/resources.go`, `internal/artifacts/garage.go`, `plans/from_utility-llm/self-hosted-go-stack-spec.md`
- what did NOT work:
  Best-effort upload cleanup -> handles ordinary returned PostgreSQL errors but cannot execute after process death.
  Garage-first fail-closed deletion -> preserves metadata when Garage reports failure but cannot roll back already deleted objects if PostgreSQL later fails.

Decisions and tradeoffs
This section captures WHY choices were made, including rejected options.

Options considered
- option: Durable operation state plus bounded reconciler
  description: Record pending artifact publication/deletion state in PostgreSQL, make object operations idempotent, and reconcile incomplete operations by age and owner.
  pros: Handles crashes without distributed transactions and provides observable recovery.
  cons / risks: Adds state transitions and requires careful retry/idempotency tests.
  decision: accepted
  rationale: It is the smallest conventional design that closes both termination windows.

- option: Distributed transaction across PostgreSQL and Garage
  description: Attempt a two-phase commit spanning both services.
  pros: Theoretical atomic commit.
  cons / risks: Garage does not participate in the application's PostgreSQL transaction protocol; complexity and failure modes are disproportionate.
  decision: rejected
  rationale: It is not supported by the current stores and would over-engineer this bounded workflow.

- option: Periodic blind deletion of unreferenced objects
  description: Delete every object absent from a current metadata snapshot.
  pros: Simple storage cleanup.
  cons / risks: Races in-flight uploads and can remove recoverable data.
  decision: rejected
  rationale: Reconciliation needs operation state or a conservative age/quiescence contract.

Key constraints influencing decisions
- Object contents may contain sensitive provider output and must not be logged.
- Operations must remain owner-scoped, idempotent, and bounded.
- A normal run should not depend synchronously on telemetry or an additional service.

Non-obvious context
- The 43 retained runless trace artifacts observed separately do not by themselves prove Garage object orphaning; metadata still exists for those traces.
- Retrying deletion can resolve a metadata-only state only if deleting an already-missing Garage object is treated idempotently.
- Tempo, Langfuse, Laminar, and ClickHouse cannot repair application storage consistency.

Workarounds
- Run read-only metadata/object inventories after suspected interruption.
- Retry owner-scoped deletion only when missing-object deletion is known to be idempotent; otherwise reconcile manually from a redacted manifest.

Known Error Record (what actually worked)

Root cause (best current understanding)
- Garage and PostgreSQL mutations are sequential and lack durable intermediate operation state. Returned errors are compensated, but hard termination can occur before compensation or after an irreversible object deletion.

Permanent fix
1) Add a PostgreSQL operation journal for pre-metadata publication and deletion intent, plus explicit available/deleting/unavailable artifact metadata states.
2) Make Garage put/delete operations idempotent and persist transitions before and after each cross-store mutation.
3) Add a bounded owner-aware reconciler for aged incomplete operations, with dry-run metrics and redacted logs.
4) Add deterministic failpoint tests for both crash windows, ambiguous puts, partial deletes, concurrent clear/save, and repeated reconciliation.
5) Update the storage specification and operational runbook with recovery and retention rules.

Verification
How to confirm the fix:
  Run isolated Postgres/Garage integration tests with termination failpoints at each store boundary, restart the service, and execute reconciliation twice.
Expected result:
  The first reconciliation converges every interrupted operation to one documented state; the second is a no-op; no available metadata points to a missing body and no aged committed object lacks metadata.

Prevention / guardrails
- Export counts and oldest age for pending publication/deletion states and reconciliation failures.
- Keep object keys deterministic and owner-scoped so retries cannot duplicate logical artifacts.
- Retain crash-window integration cases at T3; do not move the actual cross-service boundary to a fake-only test.

Principal-engineer resolution plan (2026-09-01)

Deep-RCA corrections
- Publication crosses the stores earlier than the KER initially implies: the root client uploads redacted artifacts in `client_artifacts.go:35-68` before `RunService` receives references and calls PostgreSQL `SaveExecution` at `internal/gateway/run_service.go:232-278`.
- A plain `pending publication` state cannot live only in `llm_artifacts`. That table requires an existing trace and currently checks `available = true` at `internal/postgres/migrations/0001_application.sql:88-103`, while the publication intent must exist before the trace transaction.
- An ambiguous Garage PUT response is another inconsistency window. `internal/artifacts/garage.go:225-240` returns an error without proving whether the object was committed, and a retry treats an existing key as conflict rather than success-if-identical.
- `DeleteMany` can partially succeed across objects/chunks before returning an error (`internal/artifacts/garage.go:338-353`). The current metadata can therefore remain `available` even without a process crash.
- `ClearHistory` snapshots Garage keys and later deletes all owner executions without an owner-scoped lock (`internal/gateway/resources.go:327-335`, `internal/postgres/resources.go:459-497`). A concurrent `SaveExecution` can enter between those phases.
- `PresignArtifact` authorizes through PostgreSQL but does not check body existence (`internal/gateway/resources.go:418-430`), so stale available metadata can redirect to an object-store 404.
- This remains a confirmed design window, not a confirmed production incident. Runless trace artifacts from the historical KER still have metadata and are not proof of unreferenced Garage bodies.

Required invariants
- Every aged Garage object is referenced by available artifact metadata or a durable pending operation.
- Every `available` artifact has an integrity-matching object. `deleting` artifacts are hidden and converge to deletion; `unavailable` means confirmed missing/corrupt and is never presigned.
- Publication/deletion retries are idempotent and owner-scoped. An identical retry succeeds; a differing digest/size/content type is an integrity conflict.
- A second reconciliation pass is a no-op.
- Telemetry reports state and age but never determines or repairs product state.

Target architecture
1) Introduce one gateway-owned `ArtifactCoordinator`; route run publication, single deletion, clear history, and reconciliation through it. Keep public client interfaces stable where practical.
2) Add `llm_artifact_operations` as a durable PostgreSQL journal with action (`publish`/`delete`), owner/run/trace/object identity, expected digest/size/content type, timestamps, attempt count, next retry time, and bounded error category. Store no artifact content.
3) Replace the boolean-only artifact metadata contract with `available`, `deleting`, and `unavailable`. Pre-trace publication remains in the journal because no artifact row can legally exist yet.
4) Publication sequence:
   - persist publish intent;
   - perform idempotent conditional Garage PUT;
   - atomically insert execution/artifact metadata and consume matching intents in `SaveExecution`;
   - clean up an aged publish intent with no committed execution rather than publishing an unattached object after restart.
5) Deletion sequence:
   - under an owner-scoped PostgreSQL advisory lock, snapshot exact execution/artifacts, record deletion intents, and mark metadata deleting;
   - delete Garage objects idempotently;
   - transactionally delete the exact execution aggregate and consume intents;
   - retain durable retryable state across cancellation, partial delete, or SQL failure.
6) Make PUT idempotent: use conditional write, then HEAD after conflict or ambiguous response; accept only matching key/digest/size/content type. Make delete of an already-missing object successful.
7) Run one bounded in-process reconciler guarded by a PostgreSQL advisory lock. It claims aged journal rows, retries deterministic transitions, marks confirmed integrity loss unavailable, and performs conservative inventory audits. Do not add a separate service.

Detailed implementation sequence
1) Add a canonical storage test ID and amend `self-hosted-go-stack-spec.md`, test spec, OpenAPI artifact states, and the operational runbook.
2) Add the artifact lifecycle migration and update `internal/postgres/records.go`; map existing rows to `available` without a dual read path.
3) Add transaction methods for begin publication, consume publication in execution save, begin exact deletion, claim operations, finalize deletion, and mark unavailable.
4) Take the same owner advisory lock in `SaveExecution`, `DeleteHistory`, and `ClearHistory` so clear/save cannot interleave across the key snapshot and relational mutation.
5) Refactor `internal/artifacts/garage.go` for conditional idempotent PUT, integrity HEAD, bounded inventory, and idempotent multi-delete accounting.
6) Add `internal/gateway/artifact_coordinator.go` and a bounded reconciler; preserve owner scoping and redact logs.
7) Replace direct artifact mutation in `RunService`, `DeleteHistory`, and `ClearHistory`; remove `cleanupUploadedArtifacts` only after the journal is the sole recovery path.
8) Update artifact list/presign APIs and Phoenix resource rendering so only available artifacts are actionable and unavailable state is explicit.
9) Add bounded metrics for operation state/count/oldest age/retry outcome and a dashboard/runbook. Never label metrics with owner, run, trace, or object key.

Test and production certification matrix
- T0: lifecycle transition table, age eligibility, exact-intent matching, idempotent PUT decision, migration constraints, bounded telemetry labels, and redaction.
- T1: failpoints after intent/after Garage/before SQL finalize, cancellation, repeated reconciliation, delete/clear idempotency, unavailable-presign rejection, and owner-lock behavior with process-owned fakes.
- T2: only if a frontend resource-state decision core is added; otherwise not applicable.
- T3: real PostgreSQL/Garage ambiguous PUT, partial multi-delete, service restart, concurrent clear/save, competing reconcilers, integrity mismatch, and second-pass no-op.
- T4: one browser check only if unavailable/deleting state becomes user-visible; do not move storage permutations to Chromium.
- T5: full Compose fake-provider upload/fetch, forced gateway termination at both boundaries, restart/reconcile/delete/inventory, `make test-release`, and exact-image deployment. No public provider is required.

Rollout and exit criteria
- Prove independent PostgreSQL and Garage backup/restore before migration. Deploy the schema and sole coordinator path together; do not retain legacy mutation paths.
- Start reconciliation in audit-only mode, review redacted counts/oldest age, then enable mutations only after the scope is approved.
- Require zero aged publish intents, zero stuck deletions, zero available/missing artifacts, zero unreferenced aged objects, and a no-op second pass.
- Record exact commit/image/migration identity, health, authenticated artifact round trip, reconciliation telemetry, and cleanup evidence before closure.

Implementation checkpoint (2026-09-01)
- Migration 0004 replaces the boolean artifact flag with explicit lifecycle
  state and adds typed publication/deletion journals and integrity-audit age.
- The root client now publishes through one gateway coordinator; execution save
  consumes exact intents, and history deletion uses durable batches.
- Garage PUT is idempotent only for identical integrity metadata, deletion is
  retry-safe, and the bounded reconciler resumes interrupted work while
  demoting missing or mismatched available objects.
- TEST-060 covers object-without-metadata cleanup, interrupted deletion,
  integrity demotion, idempotent second passes, real Postgres/Garage behavior,
  and bounded reconciliation telemetry. Production deployment evidence remains
  required before this KER can close.
- A bounded `audit-artifacts` command now performs the reverse comparison that
  the periodic metadata HEAD audit cannot: Garage keys are matched against live
  metadata and incomplete journal operations, output is count-only, and aged
  unreferenced or missing available objects fail the command without deletion.
- Grafana now exposes journal backlog, oldest pending age, and reconciliation
  failures. Prometheus warnings cover non-converging operations, stale backlog,
  and continuous reconciliation errors with no owner/run/trace labels.
