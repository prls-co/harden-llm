Known Error Record: Run history and output widgets use inconsistent token semantics

KER slug: 20260901-run-history-inconsistent-token-semantics
Git reference: fcda74b3824fc22a517df709f0a67939b8aa0b9c (application release where observed)
Resolution status: Resolved
Applies to (scope): Phoenix workspace history, full history, output trace summary, and owner-scoped aggregate stats for all providers
Tags: llm-stats, tokens, history, reasoning-tokens, cache-tokens, projection
Anomaly classification (IEEE 1044–inspired, lightweight):
  - anomaly type: defect
  - reproducibility: always
  - impact: medium
  - likely cause category: code

Trigger patterns (for fast matching)
- `Prompt tokens` in a history row differs from aggregate prompt-token semantics for the same run set
- `Output tokens` in history excludes `reasoningTokens`, while the output `📤` metric includes it
- history shows `inputTokens` or `outputTokens` under composite labels without displaying all component groups

Problem Record (conceptual guidance)

Symptoms
- The output summary computes `📤` as `outputTokens + reasoningTokens`.
- PostgreSQL aggregate `totalOutputTokens` also computes output plus reasoning, and aggregate `totalPromptTokens` computes input plus both cache token groups.
- Workspace and full-history rows label raw `inputTokens` as `Prompt tokens` and raw `outputTokens` as `Output tokens`; the workspace row omits cache-creation and reasoning groups entirely.
- Engineers and users can see different values for the same named metric depending on the surface.

Likely causes (ranked mental model)
1) Multiple presentation paths define token grouping independently instead of consuming one semantic projection.
2) Labels use provider-familiar terms such as prompt/output while values mix raw and composite normalized groups.
3) Tests assert individual surfaces but do not assert cross-surface equality or deliberate component-versus-total naming.

Diagnostic decision path
1) Check: Compare raw usage with all rendered token values for one run.
   How: Use a fixture with nonzero input, cache-read, cache-creation, output, and reasoning tokens; inspect `summary/1`, `history_stats/1`, and `stats/1`.
   If true: Equal labels map to unequal formulas.
   Next step: Define component and total names before changing arithmetic.

2) Check: Confirm aggregate SQL semantics.
   How: Inspect `RunStats` in `internal/postgres/resources.go` and its repository test fixture.
   If true: Prompt total is input + cache-read + cache-creation, and output total is output + reasoning.
   Next step: Make frontend labels and projections expose these formulas explicitly.

3) Check: Locate duplicate formulas and labels.
   How: Run `rg -n "Prompt tokens|Output tokens|completion_tokens|history_stats|totalPromptTokens|totalOutputTokens" frontend internal/postgres`.
   If true: The semantics are spread across projection, SQL, and two templates.
   Next step: Centralize frontend semantic derivation and add a cross-surface regression.

Evidence from this incident
- key error excerpt:
  `completion_tokens = outputTokens + reasoningTokens`
  `history_stats.output_tokens = outputTokens`
  SQL `SUM(input_tokens + cache_read_tokens + cache_creation_tokens)` and `SUM(output_tokens + reasoning_tokens)`
- logs / files involved: No error log is emitted; the anomaly is visible in rendered values and deterministic projections.
- code / config areas involved: `frontend/lib/harden_llm/llm_trace_projection.ex`, `frontend/lib/harden_llm_web/live/workspace_live.html.heex`, `frontend/lib/harden_llm_web/live/history_live.html.heex`, `internal/postgres/resources.go`, `internal/postgres/repository_test.go`
- what did NOT work:
  Adding all normalized usage fields -> preserved data but did not establish one display vocabulary.
  Separate per-surface tests -> did not detect conflicting formulas under identical labels.

Decisions and tradeoffs
This section captures WHY choices were made, including rejected options.

Options considered
- option: Expose raw groups and explicit composite totals
  description: Show Input, Cache read, Cache creation, Output, and Reasoning as components; name sums Prompt total and Completion total.
  pros: Lossless, provider-neutral, auditable, and consistent with aggregate formulas.
  cons / risks: Adds fields to compact history layouts and requires deliberate responsive presentation.
  decision: accepted
  rationale: Explicit names eliminate ambiguity without discarding normalized usage data.

- option: Make every `Output tokens` value raw output only
  description: Remove reasoning from output summary and aggregate output totals.
  pros: Simple label-to-field mapping.
  cons / risks: Changes existing aggregate meaning and can understate completion-side consumption.
  decision: rejected
  rationale: Reasoning remains a material token group and should be visible, not silently excluded.

- option: Keep formulas and document the mismatch
  description: Treat each surface as context-specific.
  pros: No code change.
  cons / risks: Identical labels remain misleading and downstream consumers cannot reason reliably.
  decision: rejected
  rationale: Documentation cannot compensate for contradictory UI semantics.

Key constraints influencing decisions
- Preserve canonical normalized token groups from the Go runtime.
- Do not derive billing unless provider cost is known; token totals and cost certainty are separate concerns.
- Compact workspace history may show fewer fields, but labels must remain exact.

Non-obvious context
- Provider-reported `totalTokens` is retained separately and should be checked against normalized component sums rather than blindly recomputed for every provider.
- Cache creation and reasoning tokens can be zero for many providers, which lets the inconsistency escape ordinary fixtures.

Workarounds
- Inspect the raw `result.usage` object when exact token accounting matters.
- Read current history `Prompt tokens` as raw input and history `Output tokens` as raw output; read aggregate prompt/output as composite totals.

Known Error Record (what actually worked)

Root cause (best current understanding)
- Token formulas and labels are independently owned by aggregate SQL, the output summary, history projection, and templates, with no cross-surface semantic invariant.

Permanent fix
1) Define canonical component and composite token fields and labels in `LlmTraceProjection`.
2) Render explicit component names and only use `Prompt total` or `Completion total` for sums.
3) Keep aggregate SQL formulas aligned with those named totals and document them in OpenAPI descriptions.
4) Add one fixture with every token group nonzero and assert the same semantics in projection, workspace, history, API, and PostgreSQL tests.

Verification
How to confirm the fix:
  Run the projection and LiveView tests with a five-group usage fixture, `make test-api`, `make test-fast`, and the targeted browser canary.
Expected result:
  Every repeated label has the same formula on every surface; raw groups sum to explicitly named totals, and reasoning/cache groups are never silently hidden by a composite label.

Prevention / guardrails
- Keep token formulas in one pure frontend projection and one authoritative backend aggregate query.
- Require nonzero cache-creation and reasoning values in cross-surface test fixtures.
- Reject new generic Prompt or Output labels unless the underlying formula is documented and tested.

Principal-engineer resolution plan (2026-09-01)

Deep-RCA corrections
- The visible label mismatch is one symptom of split accounting ownership. Provider normalization (`internal/providers/normalize.go:180-229`), runtime retry accumulation (`internal/runtime/execute.go:314-322`), PostgreSQL aggregation (`internal/postgres/resources.go:252-304`), Phoenix projection (`frontend/lib/harden_llm/llm_trace_projection.ex:16-35,179-195,236-246`), templates, and telemetry each encode part of the semantics.
- The mismatch is data-dependent rather than universal: raw input equals prompt total when both cache groups are zero, and raw output equals completion total when reasoning is zero.
- Provider-reported totals are not retained as an independent reconciliation fact. `completedUsage` derives `totalTokens` from normalized components, while malformed, missing, unsupported, or negative provider values can collapse to zero during normalization.
- History's `result` is an untyped object in `api/openapi.yaml:1256`, so the shared contract does not guarantee the same `Usage` semantics as a run result.
- Current fixtures fail to enforce arithmetic invariants: the run fixture near `frontend/test/harden_llm_web/live/workspace_live_test.exs:1364` and the stats example near `api/openapi.yaml:717` contain totals that do not equal their displayed component sums.
- Standard OpenTelemetry `gen_ai.usage.input_tokens` and `output_tokens` need inclusive prompt/completion meanings. Emitting exclusive raw input/output there makes telemetry disagree with the product vocabulary even if custom component metrics remain correct.
- Cache hits need two separate concepts: logical usage associated with the returned result and fresh provider consumption for the current call. Aggregating cached logical usage as fresh provider consumption overstates operational/provider activity.

Canonical accounting contract
- Components are nonnegative, integral, mutually exclusive values: `inputTokens`, `cacheReadTokens`, `cacheCreationTokens`, `outputTokens`, and `reasoningTokens`.
- `promptTokens = inputTokens + cacheReadTokens + cacheCreationTokens`.
- `completionTokens = outputTokens + reasoningTokens`.
- `totalTokens = promptTokens + completionTokens`.
- Accounting status is explicit: `complete`, `partial`, `unavailable`, or `inconsistent`. Missing or invalid data is not normalized to authoritative zero.
- An optional provider-reported total is reconciliation evidence, not the canonical source; a mismatch produces `inconsistent` and bounded diagnostics.
- Logical result usage and fresh provider consumption are separate. A cache hit may have logical usage but zero fresh usage.

Target architecture
1) Create one backend token-accounting owner, `internal/usage`, with checked `Validate`, `Summarize`, `Add`, and provider-total reconciliation operations. Provider adapters translate native fields; they do not define cross-surface display semantics.
2) Runtime and cache carry the canonical components, derived totals/status, and fresh-versus-saved attribution. Remove standalone arithmetic from `internal/runtime/execute.go`.
3) The gateway validates the typed usage contract before persistence. OpenAPI exposes components, named composites, status, and typed history results.
4) PostgreSQL continues deriving owner stats from `llm_runs`; SQL sums the five stored components and completeness counts. Go applies the same named summary contract to the returned aggregate. Do not add a second aggregate table.
5) Phoenix owns formatting only. It receives named components/composites from the API and applies one vocabulary across output, compact history, full history, and aggregate stats.
6) Telemetry consumes the canonical contract: standard GenAI attributes receive inclusive prompt/completion totals; bounded custom attributes/metrics retain cache, reasoning, status, and fresh/saved distinctions. Telemetry is never a widget fallback.

Detailed implementation sequence
1) Specify and test semantics
   - Amend `Usage`, `Stats`, `RunResult`, and `HistoryItem.result` in `api/openapi.yaml` with formulas, required types, status, and valid examples.
   - Add semantic OpenAPI assertions; JSON-schema shape checks alone cannot reject contradictory arithmetic.
   - Add one canonical fixture with every component nonzero and separate fixtures for unavailable, partial, inconsistent, cache hit, and retry accumulation.
2) Implement the backend owner
   - Add `internal/usage/usage.go` and focused tests for checked addition/overflow, completeness, malformed inputs, and provider-total match/mismatch.
   - Refactor `internal/providers/normalize.go` to return canonical accounting plus any provider-reported total; preserve provider-specific decomposition only at that boundary.
   - Refactor `internal/runtime/execute.go:314-322` to use the canonical adder and retain fresh/saved usage.
3) Cache and persistence
   - Validate usage when encoding/decoding `client_cache.go` and cut the cache schema/version once, coordinated with the execution-identity KER.
   - Validate the versioned execution document before `SaveExecution`; classify retained invalid/missing usage rather than fabricating zero.
   - Change `RunStats` to return the five component sums and complete/partial/unavailable/inconsistent counts; derive named totals through the shared Go helper.
   - Retire or redirect `internal/stats/stats.go` so there is one production aggregate semantic owner.
4) API and frontend cutover
   - Type history results as `RunResult` in OpenAPI and gateway responses.
   - Remove arithmetic from `LlmTraceProjection.summary/1`, `history_stats/1`, and `stats/1`; retain only formatting and legacy-state normalization.
   - Use exact labels everywhere: `Input`, `Cache read`, `Cache creation`, `Prompt total`, `Output`, `Reasoning`, `Completion total`, and `Total`.
   - Compact history may render prompt and completion totals; full detail renders all components. Repeated labels must always preserve the same formula.
5) Telemetry
   - Correct `internal/runtime/telemetry.go` standard GenAI usage attributes and add bounded accounting-status/reconciliation counters. Do not put model, profile, or trace IDs in metric labels.
6) Retained data
   - Audit retained run and cache documents for field/type validity and sum equality.
   - Backfill derived totals only when all required components are valid; mark all other records explicitly partial, unavailable, or inconsistent.
   - Invalidate old cache records at the version cut instead of retaining a second decoder.

Test and production certification matrix
- T0: all provider mappings, arithmetic/overflow, malformed and negative input, total reconciliation, retry addition, cache fresh/saved attribution, OpenAPI examples, and pure Phoenix formatting.
- T1: run/history/stats cross-surface equality, typed gateway responses, LiveView compact/full labels, and in-memory OTel assertions.
- T2: not applicable; do not add a DOM emulator for server-rendered arithmetic.
- T3: real PostgreSQL aggregation with all components and statuses, legacy rows, cache states, owner isolation, and zero newly persisted invariant violations.
- T4: one desktop/mobile browser canary for compact and full layouts plus restored history. Arithmetic remains proved at T0-T1.
- T5: migration audit, exact-image Compose release, authenticated run/history/stats comparison, telemetry gate, health checks, and cleanup.

Dependencies, rollout, and exit criteria
- Coordinate cache/document version cuts with the execution-identity KER so production performs one cache invalidation and one persisted v2 contract rollout.
- Coordinate aggregate completeness with the cost and stats-availability KERs; unknown usage must not become the next false-zero state.
- Before deployment, record complete/partial/unavailable/inconsistent retained counts and prove backup/restore. After deployment require `knownUsageCount + incompleteUsageCount = totalCount`, standard telemetry totals equal canonical prompt/completion values, and no new persisted invariant violations.
- This KER closes when a five-component fixture has identical named semantics in run output, both history surfaces, stats API/SQL, and telemetry, while invalid usage remains visibly non-authoritative.

Final resolution (2026-09-01)
- `1470930c204989e3bb94c9dad3b5e6d31b6ac97f` introduced one usage ledger
  with input, cache-read, cache-creation, output, and reasoning components plus
  exact prompt, completion, and total equations and completeness status.
- The same vocabulary now feeds run/trace persistence, direct Postgres stats,
  OpenAPI, telemetry attributes, compact history, full history, and the output
  details projection; Phoenix performs formatting, not accounting.
- Retained v1 records remain read-only and display only captured components.
  Malformed current equations fail the strict wire boundary.
- Five-component parity fixtures and runtime, SQL, gateway, projection,
  LiveView, browser, release, and deployed checks passed; final evidence is on
  issue `#46`.
