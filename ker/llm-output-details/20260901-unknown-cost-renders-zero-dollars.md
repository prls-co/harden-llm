Known Error Record: Unknown run costs render as a measured zero-dollar total

KER slug: 20260901-unknown-cost-renders-zero-dollars
Git reference: fcda74b3824fc22a517df709f0a67939b8aa0b9c (application release where observed)
Resolution status: Resolved
Applies to (scope): Aggregate LLM stats for owners whose runs include unknown cost, especially when `knownCostCount` is zero
Tags: llm-stats, cost, unknown-cost, partial-total, billing-semantics
Anomaly classification (IEEE 1044–inspired, lightweight):
  - anomaly type: defect
  - reproducibility: always
  - impact: medium
  - likely cause category: code

Trigger patterns (for fast matching)
- `Known cost` displays `$0.0000` while `Known-cost runs` is `0`
- `Unknown-cost runs` is greater than zero but the monetary total has no partial or unavailable qualifier
- API returns `totalCost: 0`, `knownCostCount: 0`, and `unknownCostCount > 0`

Problem Record (conceptual guidance)

Symptoms
- Aggregate SQL correctly sums only costs where `cost.known` is true and separately counts known and unknown runs.
- The frontend always formats numeric `totalCost`, defaulting absent or zero to `$0.0000`.
- When every run has unknown cost, the widget shows `$0.0000`, which visually resembles a measured free workload.
- With mixed known and unknown runs, the displayed dollar amount is a partial total but is not labeled partial.

Likely causes (ranked mental model)
1) The projection formats the numeric accumulator without considering `knownCostCount` and `unknownCostCount` as part of the value's certainty.
2) The API uses zero as the additive identity for known-cost sums, which is mathematically correct but semantically incomplete without counts.
3) The metric label `Known cost` is not sufficient to distinguish no known observations from a known zero total or a partial total.

Diagnostic decision path
1) Check: Inspect all three cost fields together.
   How: Read authenticated `/api/v1/stats` values for `totalCost`, `knownCostCount`, and `unknownCostCount`.
   If true: `knownCostCount = 0` means the numeric sum has no observed cost basis.
   Next step: Render unavailable rather than a measured currency value.

2) Check: Test the mixed-certainty case.
   How: Project fixtures for all-known, none-known, and mixed known/unknown runs.
   If true: Mixed totals need an explicit partial qualifier even when nonzero.
   Next step: Derive presentation from both amount and certainty counts.

3) Check: Distinguish an actual known zero-cost run.
   How: Use `knownCostCount > 0`, `unknownCostCount = 0`, and `totalCost = 0`.
   If true: `$0.0000` is appropriate and must remain distinguishable from unknown.
   Next step: Add this as a regression fixture.

Evidence from this incident
- key error excerpt:
  `known_cost: "$" <> :erlang.float_to_binary(total_cost * 1.0, decimals: 4)`
  The 2026-08-29 owner-scoped production stats response had 20 runs, `knownCostCount=0`, and `unknownCostCount=20`, while the widget projected `$0.0000`.
- logs / files involved: Authenticated production `/api/v1/stats` metadata from the audit conversation; no credentials or provider output retained.
- code / config areas involved: `internal/postgres/resources.go`, `internal/gateway/resources.go`, `api/openapi.yaml`, `frontend/lib/harden_llm/llm_trace_projection.ex`, `frontend/lib/harden_llm_web/components/llm_trace_components.ex`
- what did NOT work:
  Separate unknown-cost count -> preserves truth but requires users to infer that the adjacent dollar value is unavailable or partial.
  `Known cost` label -> does not communicate zero observations versus a known zero amount.

Decisions and tradeoffs
This section captures WHY choices were made, including rejected options.

Options considered
- option: Certainty-aware cost presentation
  description: Show unavailable when no costs are known, exact currency when all are known, and explicitly partial currency when only some are known.
  pros: Honest, compact, and uses fields already present in the API.
  cons / risks: Requires a richer display value or status in the projection.
  decision: accepted
  rationale: It distinguishes unknown, known zero, and partial totals without changing storage.

- option: Treat unknown cost as zero
  description: Preserve the current formatted sum and omit qualifiers.
  pros: Simplest arithmetic.
  cons / risks: Understates spend and conflates missing data with free execution.
  decision: rejected
  rationale: Unknown is not zero.

- option: Estimate missing costs from current profile pricing
  description: Recalculate historical costs from current catalog prices and usage.
  pros: Produces a complete-looking total.
  cons / risks: Pricing and provider accounting can change; estimates are not trace-attributed facts.
  decision: rejected
  rationale: The widget must not fabricate billing data.

Key constraints influencing decisions
- Cost must remain trace-attributed and explicitly known or unknown.
- Historical pricing must not be inferred from mutable current profiles.
- Owner-wide API counts are sufficient for total-cost certainty, but cached cost needs a cached-subset certainty count. No new telemetry or analytics store is needed.

Non-obvious context
- A known zero-cost run is valid and must render differently from zero known-cost observations.
- Cached-cost certainty follows the same rule, but it cannot be inferred from owner-wide known/unknown counts when cached and noncached runs are mixed.
- ClickHouse/Langfuse analytics are not the application billing source of truth.

Workarounds
- Read `$0.0000` together with known/unknown run counts; if known count is zero, treat cost as unavailable.
- Use raw per-run `cost.known` and `cost.totalUsd` for audits.

Known Error Record (what actually worked)

Root cause (best current understanding)
- Backend aggregation preserves cost certainty, but frontend formatting discards that certainty and converts the additive zero for an empty known subset into a currency claim.

Permanent fix
1) Change the stats projection to derive a cost display state from amount, known count, unknown count, and total count.
2) Render `—` or `Unknown` when `knownCostCount = 0`; render `$0.0000` only when at least one known observation proves zero.
3) Mark mixed known/unknown sums as partial and retain both counts.
4) Add cached-subset certainty to the stats contract and apply the same semantics to cached cost.
5) Preserve known partial attempt spend rather than collapsing a mixed-certainty run to an empty unknown cost.
6) Add projection, component, API, PostgreSQL, cache, and LiveView tests for empty, all-known zero, tiny positive, all-known nonzero, all-unknown, mixed, and cached-subset data.

Verification
How to confirm the fix:
  Run focused projection/component tests for every certainty state, PostgreSQL/gateway integration, `make test-fast`, `make verify`, `make test-browser`, and inspect an authenticated stats response with unknown costs.
Expected result:
  Unknown-only data never renders as measured `$0.0000`; known zero remains distinguishable; tiny positive values never round to zero; mixed data is visibly partial; cached and overall counts/amounts remain internally consistent.

Prevention / guardrails
- Model measurement certainty alongside every aggregate whose additive identity can be confused with missing data.
- Require unknown-only and known-zero fixtures for monetary metrics.
- Never backfill cost from telemetry or mutable catalog pricing without an explicit separate estimation contract.

Principal-engineer resolution plan (2026-09-01)

Deep-RCA corrections
- Overall certainty reaches Phoenix but is discarded: `internal/postgres/resources.go:283-287` returns the known subtotal plus known/unknown run counts, while `frontend/lib/harden_llm/llm_trace_projection.ex:147-168` formats the amount independently and defaults missing/non-numeric values to zero.
- Cached certainty is not present in the API. SQL returns known cached subtotal and total cached count, but `knownCostCount`/`unknownCostCount` describe all runs. Mixed cached/noncached populations cannot be classified from those fields.
- The current four-decimal formatter makes `$0.0000` ambiguous even for a known observation: a positive amount below `$0.00005` rounds to zero. The OpenAPI examples include small values in this range.
- SQL classifies `cost.known=true` independently from the validity of `totalUsd`; a malformed/negative/missing retained amount can therefore count as known while contributing normalized zero. Certainty must fail closed on malformed shape.
- Runtime aggregation at `internal/runtime/execute.go:325-336` replaces a mixed known/unknown attempt set with `{Known:false, Source:"unknown"}`, discarding the known subtotal. This is adjacent to the widget symptom but must be resolved for production-grade retry/fallback cost accounting.
- Cache hits replay trace-attributed cost of the cached result while avoiding a current provider invocation. `cachedCost` is not a provider bill, an incremental cache charge, or proven savings; labels and documentation must not imply those meanings.
- The historical production count in the incident record is time-sensitive and not a durable test oracle. The source defect is sufficient; refresh counts before any production certification.

Cost certainty contract
- Use states `empty`, `unknown`, `exact`, `partial`, and `invalid`.
- `empty`: no observations; render `—` in the stats widget rather than claiming measurement.
- `unknown`: zero known and one or more unknown observations; render `Unknown`.
- `exact`: one or more known and zero unknown observations; render currency, including a true exact zero.
- `partial`: both known and unknown observations; render `$x known · partial` with accessible coverage text.
- `invalid`: impossible counts or non-finite/negative amount; render `Unavailable` and emit bounded diagnostics.
- A positive amount below normal display precision renders `<$0.0001`; it never renders `$0.0000`.
- Overall invariants: `totalCount = knownCostCount + partialCostCount + unknownCostCount` after the per-run certainty cut.
- Cached invariants use the cached subset: `cachedCount = cachedKnownCostCount + cachedPartialCostCount + cachedUnknownCostCount`.

Target architecture
1) Keep PostgreSQL `llm_runs.result` as the sole owner-scoped source and direct aggregation path. Add no maintained aggregate table or telemetry query.
2) Extend the canonical run `Cost` contract with explicit status and known subtotal so runtime retries preserve facts:
   - exact: all observed attempt cost is known;
   - partial: known subtotal plus one or more unknown attempts;
   - unknown: no known amount;
   - unavailable: no cost observation.
   Keep legacy `known` semantics only as a derived compatibility field during the additive API transition; new code consumes status.
3) Aggregate known subtotals and exact/partial/unknown counts directly in `RunStats`. Add cached-subset counts from the same rows. If the execution-cost status change is staged, add `cachedKnownCostCount` first but do not infer cached certainty in Phoenix.
4) OpenAPI defines amount meanings, count equations, cached subset, and trace-attributed—not billing—semantics.
5) One pure frontend `cost_measurement(amount, exact_count, partial_count, unknown_count)` returns structured state, display text, and accessible explanation for overall and cached amounts. HEEx and both LiveViews contain no certainty conditionals.
6) Integrate this measurement into the explicit stats resource state from the stats-availability KER so pre-response unknown and measured cost unknown remain distinct.

Detailed implementation sequence
1) Add canonical backend/frontend test IDs and amend stats, cost, cache, and frontend specifications with the state table and count equations.
2) Add pure cost aggregation tests around `addCost`: known+known, known+unknown, unknown+known, all unknown, exact zero, tiny positive, source mixing, retry, fallback, repair, and cache replay.
3) Extend the runtime/public/versioned execution cost type and cache v2 envelope in coordination with the execution-identity KER. Preserve known subtotal and status through cache write/hit and persistence.
4) Harden `Store.RunStats` cost extraction so `known/status` requires a finite nonnegative amount; malformed retained JSON classifies unavailable/unknown rather than known zero.
5) Add exact/partial/unknown overall and cached-subset counts to `postgres.RunStats`, `gateway.StatsView`, `ResourceService.Stats`, and `api/openapi.yaml`. If partial run status is not yet present in v1 rows, map `known=true` to exact and `known=false` to unknown.
6) Strengthen resource route and owner-isolation tests to assert every amount/count equation, not only aggregate amount.
7) Add the pure cost measurement and nonzero-safe USD formatter in `LlmStatsProjection`; make `llm_stats_summary/1` render state, qualifier, title, and ARIA explanation.
8) Update API fixtures and the browser backend to calculate cached certainty from the cached subset. Coordinate caller cutover with stats availability and token semantics.
9) Audit retained cost shapes read-only before deployment. Do not rewrite or estimate from current profile pricing; classify malformed/legacy records explicitly.

Test and production certification matrix
- T0: cost state table, runtime partial aggregation, exact/zero/tiny formatting, malformed values/counts, cached subset equations, cache-v2 validation, and OpenAPI examples.
- T1: gateway response contract, component and both LiveViews, accessible qualifiers, loading/unavailable/stale integration, and no false currency claim.
- T2: not applicable; no JavaScript decision logic is needed.
- T3: real PostgreSQL rows for every certainty state, cached/noncached mixtures, malformed legacy JSON, owner isolation, and cache persistence round trip.
- T4: update one existing Chromium canary for unknown, partial, tiny-positive, and accessible text; keep permutations below the browser tier.
- T5: `make test-release`, full Compose fixture, exact pushed SHA/image identity, health/readiness, authenticated `/api/v1/stats` to rendered-UI comparison, and canary cleanup. Public provider calls remain separately authorized.

Migration, rollout, and exit criteria
- The stats count fields are derived from existing JSON and need no relational data migration. The versioned cost-status/cache change uses the coordinated execution document/cache v2 cut; retained v1 maps only from its own immutable `known` field.
- Deploy gateway before or atomically with Phoenix. The new frontend fails closed when required cached certainty is absent; do not add mathematical guesses or a telemetry fallback.
- Before deployment, verify overall/cached equations and malformed-shape counts. After deployment require unknown-only never renders currency, exact zero remains exact, tiny positive never renders zero, partial known subtotal survives retries, and cached certainty is independently correct.
- Closure requires T0-T4 gates, `make test-fast`, `make verify`, `make test-browser`, `make test-release`, hosted CI, exact release/image identity, authenticated production comparison, and zero canary history residue.

Final resolution (2026-09-01)
- `1470930c204989e3bb94c9dad3b5e6d31b6ac97f` replaced the ambiguous amount
  with a canonical exact/partial/unknown/unavailable cost ledger, known
  subtotal, source, and observation counts for both result and provider views.
- Direct Postgres aggregation owns overall and cached-subset coverage equations.
  `LlmStatsProjection` renders unknown as unavailable, partial as a qualified
  subtotal, exact zero as `$0.0000`, and positive subprecision values as
  `<$0.0001`; it never infers certainty from an amount.
- Retry, fallback, repair, cache replay, malformed retained JSON, SQL/API
  equations, projection, component, browser, release, and authenticated
  production checks passed. Exact closure evidence and smoke cleanup are on
  issue `#46`.
