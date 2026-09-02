Known Error Record: Output details can misattribute fallback or repair output to the selected model

KER slug: 20260901-output-details-misattributed-fallback-model-identity
Git reference: fcda74b3824fc22a517df709f0a67939b8aa0b9c (application release where observed)
Resolution status: Resolved
Applies to (scope): Self-hosted Go runtime and Phoenix output trace details when a backup profile or structured-repair escalation differs from the selected profile/model
Tags: llm-trace, output-details, fallback, repair, profile-id, model-id, attribution
Anomaly classification (IEEE 1044–inspired, lightweight):
  - anomaly type: defect
  - reproducibility: always
  - impact: high
  - likely cause category: code

Trigger patterns (for fast matching)
- `Model: <selected model>` remains in output details after a different backup or repair profile produced the terminal result
- `profileId` is present in persisted attempt JSON but absent from the rendered attempt list
- a terminal provider-used attempt has a different `profileId` from top-level `result.profileId`

Problem Record (conceptual guidance)

Symptoms
- The summary and detailed panel identify the profile, model, provider, API type, and base URL selected before execution.
- A successful backup or structured-repair escalation can execute against another profile, but the final output remains labeled with the selected profile's model/provider identity.
- Attempt rows show category, status, timing, and retry state without the attempt profile, model, provider, or endpoint identity needed to explain the switch.

Likely causes (ranked mental model)
1) The gateway constructs top-level `RunOutput` and trace identity from `input.ProfileID` and the initially resolved `profile`; this fits every execution where the terminal provider-used attempt differs.
2) Runtime attempts preserve only `profileId`, while immutable attempt-level model/provider/API/base-URL snapshots are absent; this prevents reliable reconstruction after profiles change.
3) The frontend projection discards the existing attempt `profileId`; this makes even the partial runtime identity invisible.

Diagnostic decision path
1) Check: Compare the selected identity with the terminal provider-used attempt.
   How: Inspect the authenticated `/api/v1/history` result or `/api/v1/traces/{traceId}` JSON and compare top-level `profileId` with the last attempt where `providerUsed` is true.
   If true: The displayed top-level identity may not describe the producer of the final output.
   Next step: Resolve the attempt profile only for diagnosis; do not rewrite historical identity from mutable current profile rows.

2) Check: Confirm which fields survive each boundary.
   How: Run `rg -n "ProfileID|ModelID|ProviderBaseURL|attempts" types.go internal/gateway/run_service.go frontend/lib/harden_llm/llm_trace_projection.ex`.
   If true: `Attempt.ProfileID` exists, attempt model/provider fields do not, and `details/1` drops the profile.
   Next step: Extend the server-owned OpenAPI/runtime/persistence projection before changing presentation.

3) Check: Reproduce the attribution invariant cheaply.
   How: Add a deterministic runtime/gateway test whose primary attempt fails and whose backup or escalation succeeds with a different profile and model.
   If true: Assert that both selected identity and actual terminal execution identity survive the REST response and frontend projection.
   Next step: Keep a browser test only for rendered labeling; prove permutations below the browser tier.

Evidence from this incident
- key error excerpt:
  `RunID: runID, ProfileID: input.ProfileID, ModelID: profile.ModelID, Provider: profile.Provider`
  `"profileId" => text([result["profileId"]])`
  Persisted attempt `profileId` is not copied into the frontend attempt map.
- logs / files involved: No runtime error is emitted; this is a deterministic attribution defect found by source and contract inspection.
- code / config areas involved: `types.go`, `internal/runtime/execute.go`, `internal/gateway/run_service.go`, `api/openapi.yaml`, `frontend/lib/harden_llm/llm_trace_projection.ex`, `frontend/lib/harden_llm_web/components/llm_trace_components.ex`
- what did NOT work:
  Top-level immutable identity hardening -> preserved the selected configuration but did not distinguish it from actual terminal execution identity.
  Existing gateway identity tests -> cover explicit primary model override, not backup/repair terminal attribution.

Decisions and tradeoffs
This section captures WHY choices were made, including rejected options.

Options considered
- option: Persist selected and actual execution identity separately
  description: Keep selected identity for reproducibility and add immutable profile/model/provider/API/base-URL identity to each provider-used attempt plus an explicit terminal execution identity.
  pros: Accurate, historical, independent of later profile edits, and useful for retries and repairs.
  cons / risks: Requires coordinated Go types, OpenAPI, persistence fixtures, projection, and UI changes.
  decision: accepted
  rationale: It preserves both user intent and what actually produced the output without ambiguous overloading.

- option: Resolve attempt profiles from the current profile catalog while rendering
  description: Look up each persisted attempt `profileId` when the widget is opened.
  pros: Smaller response-schema change.
  cons / risks: Profiles are mutable or deletable, so historical attribution can change or disappear.
  decision: rejected
  rationale: Diagnostic identity must be immutable at execution time.

- option: Relabel the current top-level fields as actual identity
  description: Change only labels while retaining current values.
  pros: Presentation-only change.
  cons / risks: Makes the UI confidently wrong and hides selected-versus-actual semantics.
  decision: rejected
  rationale: Labels cannot repair missing data.

Key constraints influencing decisions
- Provider credentials and provider-native payloads must remain excluded.
- The backend owns execution and persistence; Phoenix must not reconstruct runtime truth.
- OpenAPI is the only backend/frontend coupling contract.
- Existing history must remain readable when new fields are absent.

Non-obvious context
- Repair attempts can change model without changing profile, while backups can change both profile and model.
- `profileId` alone is insufficient because a profile's model/provider/base URL can be edited after the run.
- Telemetry may contain runtime attributes, but the widget must not depend on Tempo, Langfuse, Laminar, or ClickHouse to recover product data.

Workarounds
- Inspect persisted attempt `profileId` and the trace observations when diagnosing a fallback.
- Treat top-level model/provider fields as selected configuration, not proven terminal producer identity, until fixed.

Known Error Record (what actually worked)

Root cause (best current understanding)
- The output schema overloads selected identity as execution identity, does not persist a complete immutable identity per provider-used attempt, and drops the one attempt identity field that already exists during frontend projection.
- This conclusion is source-confirmed; no retained live fallback run was created during the audit.

Permanent fix
1) Add immutable attempt execution identity fields to the public Go type and OpenAPI schema, populated at the provider-call boundary.
2) Derive an explicit terminal execution identity from the final provider-used attempt while retaining separately named selected identity.
3) Persist both identities in run and trace documents and project them without consulting mutable profile state.
4) Render selected identity and actual attempt identity clearly, with backward-compatible unavailable states for legacy rows.
5) Add deterministic runtime, gateway, API, projection, LiveView, and one rendered browser assertion for a differing backup and repair model.

Verification
How to confirm the fix:
  Run `make test-fast`, focused Go backup/repair tests, `make test-api`, and `make test-browser`; inspect a bounded run where the selected and terminal models differ.
Expected result:
  The widget names the selected configuration and the actual terminal producer separately; each provider-used attempt has immutable identity, and no profile lookup is required to render history.

Prevention / guardrails
- Require every provider invocation record to carry an immutable, redacted execution identity snapshot.
- Add an OpenAPI invariant that selected and terminal identities are distinct named concepts.
- Keep a cheap regression where fallback succeeds on a model different from the selected model.

Principal-engineer resolution plan (2026-09-01)

Deep-RCA corrections
- The defect is broader than a missing display field. `internal/runtime/types.go:112-123` stores generic `retry.Attempt` values as the canonical call attempts, although the prepared provider operation already knows the model, provider, protocol, and normalized endpoint. The public conversion in `client.go:320-343` then copies only retry metadata and sets `ProviderUsed` without carrying that prepared-operation identity.
- `internal/gateway/run_service.go:240-258` reconstructs both the response and trace identity from the initially selected profile. That value is selected configuration, not proof of the terminal producer.
- Cache hits are a separate result source, not a current provider invocation: `internal/runtime/execute.go:86-105` returns before an attempt exists. The cache envelope at `cache.go:18-27` does not retain the original producer identity, so a cache hit cannot currently be attributed honestly.
- `ProviderUsed` is not a reliable discriminator for pre-provider repair failures because it is assigned during public conversion rather than at the `Executor.Execute` boundary. Repair telemetry is also initialized from the outer backup profile at `internal/runtime/execute.go:123-126`, before a repair profile can replace it.
- Retry-local attempt numbers restart for every backup candidate because `retry.Do` is inside the backup loop at `internal/runtime/execute.go:63-127`. The result contract needs a call-global attempt number plus, if useful for diagnosis, the retry-local number.
- The observed impact is diagnostic and audit misattribution. There is no evidence that this defect changes the generated output itself.

Current ownership and violated invariants
- Provider preparation owns the immutable execution target facts.
- Runtime owns attempt lifecycle and must be the only producer of actual execution identity and result source.
- Retry owns classification, waiting, and retry policy only; it must not acquire LLM provider identity fields.
- Gateway owns selected configuration and persistence, but must not infer actual execution identity.
- PostgreSQL run/trace documents are product truth. OpenTelemetry, Tempo, Langfuse, and Laminar may consume the same facts but must never be queried to reconstruct them.
- A provider result must reference exactly one successful provider-used attempt. A cache result must identify the cached producer while recording no provider invocation for the current call. A failed/pre-provider result must use an explicit `none` source.

Target architecture
1) Add runtime-owned `ExecutionIdentity` containing redacted immutable `profileId`, `modelId`, `provider`, `apiInferenceType`, protocol, and normalized `providerBaseUrl`.
2) Replace `CallRecord.Attempts []retry.Attempt` with a runtime `AttemptRecord` that embeds or references retry facts and adds call-global number, retry-local number, backup index, repair state, prepared execution identity, and an exact `providerUsed` lifecycle bit.
3) Add a discriminated `ResultSource`:
   - `provider` with the successful call-global attempt number;
   - `cache` with the immutable producer identity stored in the cache envelope;
   - `none` when no result was produced by either source.
4) Keep the existing top-level identity fields for API compatibility but explicitly define and render them as selected configuration. Do not add a second duplicate selected-identity object.
5) Build one typed, versioned execution document in the gateway and use it for the public result, persisted run result, and trace record. Remove the manually duplicated anonymous trace map.
6) Feed provider-attempt telemetry from the canonical runtime attempt record after preparation. Metric labels remain bounded; model/profile IDs may be span attributes but not metric dimensions.

Detailed implementation sequence
1) Contract-first regressions
   - Extend the canonical backend tests for primary failure then backup success, same-profile repair model override, different-profile repair, credential/prepare failure, all-attempt failure, cache hit, and legacy rows.
   - Add OpenAPI examples and semantic contract assertions for selected identity, provider/cache/none result source, and attempt-reference integrity.
2) Runtime model
   - Add `ExecutionIdentity`, `AttemptRecord`, and `ResultSource` in `internal/runtime/types.go`.
   - In `internal/runtime/execute.go`, capture identity from the active prepared operation, allocate call-global numbers, and set `providerUsed` immediately before `Executor.Execute`.
   - Keep `internal/retry` generic; translate its facts into the runtime attempt record.
3) Cache provenance
   - Extend the runtime/cache value and public `CacheRecord` in `cache.go` with the terminal source identity.
   - Bump the cache envelope/version, invalidate v1 entries during rollout, and use one v2 reader. Do not guess old producer identity from the current profile.
4) Public and persisted contract
   - Extend `types.go` and `client.go` to publish canonical attempts and result source.
   - Define typed `ExecutionIdentity`, `Attempt`, and `ResultSource` schemas in `api/openapi.yaml`; resolve the attempt-array budget against the runtime retry/backup policy.
   - Replace the two constructions in `internal/gateway/run_service.go:240-258` with one typed `harden-llm.execution.v2` document. Persist selected identity and canonical runtime result without reconstruction.
5) Trace and telemetry consumers
   - Update `internal/traces` observations to derive from the same attempt records.
   - Move attempt span/accounting attribution to the active execution identity in `internal/runtime/telemetry.go`.
6) Frontend
   - Preserve `profileId`, execution identity, `providerUsed`, backup index, repair state, and result source in `frontend/lib/harden_llm/llm_trace_projection.ex:49-72`.
   - Render `Selected configuration`, `Result source`, and per-attempt execution identity in `frontend/lib/harden_llm_web/components/llm_trace_components.ex:169-207`.
   - For v1 rows, render `Actual execution identity: not captured` instead of looking up mutable profiles or silently omitting the field.
7) Documentation and operations
   - Amend the backend, OpenAPI, frontend, and cache-version specifications together.
   - Coordinate the v2 historical classification with the legacy-history KER; identity v2 must be finalized before any historical schema migration.

Test and production certification matrix
- T0: identity extraction, global numbering, result-source selection, cache-v2 decoding, v1 projection, OpenAPI schema/examples, and impossible-state rejection.
- T1: deterministic backup/repair/pre-provider/cache permutations through runtime, gateway, trace projection, telemetry assertions, and LiveView rendering.
- T2: not applicable unless client-side decision logic changes.
- T3: real PostgreSQL run/history/trace round trips and cache miss/write/hit, asserting that run, trace, observation, and cache producer identities agree.
- T4: one browser canary showing a selected primary and a distinct actual fallback/repair producer; keep permutations below the browser tier.
- T5: `make test-release`, cache-v1 invalidation evidence, exact pushed SHA/image deployment, health/readiness, authenticated run/history/trace/cache checks, and canary cleanup.

Rollout and exit criteria
- Deploy the additive API/document v2 and cache version cut in one coherent release; legacy persisted rows remain readable through one explicit v1 normalization path.
- Record bounded counters for `result_source=provider|cache|none` and identity transition class. Alert on provider-used attempts without identity, invalid provider attempt references, or new cache hits without producer identity.
- This KER closes only when selected and actual identity differ correctly in deterministic backup and repair cases, cache provenance survives a hit, telemetry agrees with product records, legacy data is explicit, and the exact deployed revision passes the production gates.

Final resolution (2026-09-01)
- `1470930c204989e3bb94c9dad3b5e6d31b6ac97f` made schema v2 the canonical
  execution record: selected target, attempt-local immutable target, terminal
  result source, provider invocation, and result/provider accounting are
  persisted together in the run and trace documents.
- `8407b83b01a36fe63397259f75ccbab05f34329a` derives telemetry attribution
  from the same immutable attempt/result facts. Telemetry remains diagnostic
  and never reconstructs product identity.
- `9943ed901ae6d3181aac24557078e7a5b22568b7` projects selected and actual
  producer identity through strict pure view models. Retained v1 records show
  unavailable producer identity instead of consulting mutable profiles.
- Backup, repair, cache-hit, failure, strict-wire, component, LiveView,
  browser, release, and authenticated deployed-canary coverage passed. Exact
  closure release and image evidence are recorded on issue `#46`.
