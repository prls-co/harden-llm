Known Error Record: Initial stats load failure renders placeholder zeroes as if authoritative

KER slug: 20260901-stats-load-failure-renders-zero-values
Git reference: fcda74b3824fc22a517df709f0a67939b8aa0b9c (application release where observed)
Resolution status: Resolved
Applies to (scope): Phoenix workspace and history aggregate LLM stats during initial API loading or failure
Tags: llm-stats, liveview, loading-state, error-state, stale-data, availability
Anomaly classification (IEEE 1044–inspired, lightweight):
  - anomaly type: defect
  - reproducibility: always
  - impact: medium
  - likely cause category: code

Trigger patterns (for fast matching)
- `Aggregate stats are temporarily unavailable.` appears while the metric grid still shows zero runs and zero totals
- initial `GET /api/v1/stats` fails before any successful stats snapshot exists
- stats request is in flight but zero values are rendered without a loading label

Problem Record (conceptual guidance)

Symptoms
- Both LiveViews initialize `@stats` with `LlmTraceProjection.stats(%{})`, which produces a complete zero-valued map.
- They track the async request reference and set an error subtitle on failure, but the stats component always renders the full metric grid.
- Before the first successful response, loading, unavailable, and genuine all-zero data are visually similar.
- A user can read transport failure as authoritative absence of runs, tokens, duration, and cost.

Likely causes (ranked mental model)
1) The presentation contract accepts only values and an optional subtitle, not an explicit loading/available/stale state.
2) Zero normalization is useful after a successful empty response but is reused as pre-response placeholder state.
3) Tests verify the error text but do not require that unverified metrics be hidden or marked unavailable.

Diagnostic decision path
1) Check: Determine whether a successful stats snapshot has ever loaded.
   How: Inspect LiveView assigns for `stats_ref`, `stats_error`, and a dedicated loaded/snapshot flag; currently no loaded flag exists.
   If true: Zeroes cannot be classified as server data or initialization defaults.
   Next step: Add an explicit stats resource state.

2) Check: Simulate initial API failure and delayed success.
   How: Use the process-owned HardenAPI test transport to return an error, then mount workspace/history and inspect the stats region.
   If true: The grid renders normalized zeroes during unavailable state.
   Next step: Assert loading/unavailable UI and absence of authoritative numeric values.

3) Check: Simulate refresh failure after a successful snapshot.
   How: First return nonzero stats, then fail the next refresh.
   If true: Decide whether to keep the last snapshot as explicitly stale rather than replacing or presenting it as current.
   Next step: Encode this state in one shared component contract.

Evidence from this incident
- key error excerpt:
  `assign(:stats, LlmTraceProjection.stats(%{}))`
  `assign(:stats_error, "Aggregate stats are temporarily unavailable.")`
  `llm_stats_summary` renders `stats_fields()` unconditionally.
- logs / files involved: No persistent log is required; reproduce with deterministic LiveView API failure fixtures.
- code / config areas involved: `frontend/lib/harden_llm_web/live/workspace_live.ex`, `frontend/lib/harden_llm_web/live/history_live.ex`, their HEEx templates, `frontend/lib/harden_llm_web/components/llm_trace_components.ex`, `frontend/lib/harden_llm/llm_trace_projection.ex`
- what did NOT work:
  Error subtitle -> reports failure but leaves authoritative-looking zero metrics visible.
  Async reference guards -> prevent stale response races but do not model data availability.

Decisions and tradeoffs
This section captures WHY choices were made, including rejected options.

Options considered
- option: Explicit stats resource state
  description: Pass `loading`, `available`, `stale`, or `unavailable` plus an optional last successful snapshot to the component.
  pros: Distinguishes all-zero data from no data and supports robust refresh behavior.
  cons / risks: Adds a small state contract shared by two LiveViews.
  decision: accepted
  rationale: Availability is not a numeric value and must be represented separately.

- option: Keep zero grid with only an error subtitle
  description: Preserve current rendering.
  pros: No layout change.
  cons / risks: Misrepresents unknown data as measured zero.
  decision: rejected
  rationale: The core semantic defect remains.

- option: Clear the whole widget on every refresh
  description: Hide prior values whenever a request starts.
  pros: Never presents stale data.
  cons / risks: Causes flicker and discards a useful last-known snapshot during transient failures.
  decision: rejected
  rationale: Last-known data is useful when explicitly labeled stale.

Key constraints influencing decisions
- Initial failure and refresh failure have different user meaning.
- Async reference guards must continue rejecting out-of-order responses.
- A successful HTTP 200 with `totalCount = 0` must still render real zeroes.

Non-obvious context
- PostgreSQL computes stats directly from `llm_runs`; this defect is presentation state, not aggregate drift.
- Workspace history loads independently, so visible history can directly contradict a failed stats grid showing zero.
- Telemetry availability does not determine product-stats availability.

Workarounds
- Treat the grid as unavailable whenever the error subtitle is present.
- Reload after the API recovers; verify with History rather than relying on placeholder zeroes.

Known Error Record (what actually worked)

Root cause (best current understanding)
- Initialization uses a valid zero-valued domain projection as a transport-state placeholder, and the reusable stats component has no availability-state input.

Permanent fix
1) Introduce a small explicit stats resource state in workspace and history LiveViews.
2) Render a loading state before the first response and an unavailable state without numeric claims after initial failure.
3) Preserve a last successful snapshot on refresh failure only when visibly marked stale with the failure message.
4) Keep genuine all-zero rendering only for a successful stats response.
5) Add deterministic tests for initial loading, successful empty, successful nonempty, initial failure, stale refresh failure, and out-of-order responses.

Verification
How to confirm the fix:
  Run focused workspace/history LiveView tests for all resource states, then `make test-fast` and the targeted browser test.
Expected result:
  Initial failure never displays unverified zero metrics; successful empty data displays zeroes; refresh failure retains and labels only the last successful snapshot as stale.

Prevention / guardrails
- Never use a valid domain value as a loading/error sentinel.
- Require reusable data widgets to model availability independently from values.
- Keep a test that renders existing history alongside a failed stats request and rejects a false zero claim.

Principal-engineer resolution plan (2026-09-01)

Deep-RCA corrections
- The defect is confirmed in both hosts: `workspace_live.ex:76` and `history_live.ex:17` initialize a complete zero projection before any HTTP response. Failure handlers at `workspace_live.ex:335-343` and `history_live.ex:95-103` change only error text, while `llm_trace_components.ex:278-283` renders every metric unconditionally.
- The workspace false-zero loading state is timing-dependent because its widget is hidden until workspace hydration is ready; History renders the stats widget during initial loading. Initial failure remains deterministic on both once the region is visible.
- The monotonic reference guards at `workspace_live.ex:323-346,1028-1038` and the equivalent History code correctly reject stale responses. They solve races but not availability semantics.
- The API client validates the response envelope but does not strictly validate the Stats payload shape before projection. Missing or malformed successful fields can therefore become zero through projection normalization, reproducing the same false-authority defect without a transport error.
- PostgreSQL aggregation at `internal/postgres/resources.go:252-304` and the owner-scoped API are authoritative and are not the root cause. Existing `getStats` request telemetry already observes this path; no additional data store is required.

Resource-state contract and invariants
- Availability is independent of numeric value. A valid successful empty response is authoritative zero; no response, a malformed response, and an HTTP failure are not.
- The component state is one of: initial `loading`, `available`, `refreshing` with last snapshot, `stale` with last snapshot and error, or `unavailable` with no snapshot.
- An out-of-order task must never replace newer state. A refresh failure may retain the last successful snapshot only when visibly labeled stale.
- A malformed 2xx Stats response is a bounded protocol error, not an empty dataset.
- PostgreSQL remains product truth; `HardenAPI` remains the sole auth/HTTP/telemetry boundary; Prometheus, Tempo, Langfuse, Laminar, and ClickHouse are never fallback sources.

Target architecture
1) Extract aggregate normalization from the trace projection into a pure `HardenLlm.LlmStatsProjection`. It strictly validates the complete OpenAPI Stats contract and returns `{:ok, value}` or a bounded error; it never treats `%{}` as successful zero data.
2) Use `Phoenix.LiveView.AsyncResult` as the shared lifecycle representation in Workspace and History. LiveView already owns task replacement by stable async key and can retain a prior successful result during loading/failure.
3) Use one stable `:load_stats` key rather than application-generated task names. Preserve the global 401 session-expiry handling and do not auto-retry 429/503 contrary to the frontend contract.
4) Change `llm_stats_summary/1` to accept one explicit resource contract, not independent `stats` and optional subtitle fields. The component owns accessible loading/unavailable/stale/available markup.
5) Workspace and History own only refresh triggers. Cut both callers over directly; do not retain the sentinel-map API as a compatibility path.

Required component behavior
- Initial loading: no numeric grid, visible `role="status"`, and `aria-busy="true"`.
- Initial failure or malformed success: no numeric grid, visible `role="alert"`, and a manual Retry action.
- Successful empty: render genuine zeroes with available state.
- Refreshing: retain the previous grid and label it as the previous snapshot.
- Refresh failure: retain the grid only as visibly stale, expose the failure, and offer Retry.
- Ready: render current values normally. Status/description IDs are derived from the component root.

Detailed implementation sequence
1) Add a canonical `WEB-TEST` entry for stats lifecycle, malformed success, retry, accessibility, and response ordering.
2) Add pure projection failures for missing field, wrong type, negative value, contradictory count/totals where applicable, malformed map, successful empty, and nonempty Stats responses.
3) Add state-aware component tests for every state and assert numeric facts are absent when no snapshot exists.
4) Add process-owned Req barrier tests in both LiveViews for initial load, empty/nonempty success, initial failure, refresh/stale failure, retry recovery, 401 expiry, and both reverse-completion orders.
5) Add `frontend/lib/harden_llm/llm_stats_projection.ex`; move strict aggregate normalization there and coordinate cost/token semantic validation with their KERs.
6) Validate/project Stats before `HardenAPI.get_stats/1` records a successful result. A malformed 2xx remains within existing bounded `getStats` logs, spans, request counters, and duration histogram.
7) Replace `stats`, `stats_error`, and `stats_ref` assigns in both LiveViews with `AsyncResult` and a stable async operation key.
8) Replace the component API with the resource contract, add manual `refresh-stats` handling, and update every caller in one cut.
9) Update frontend specification, component documentation, and parity inventory.

Test and production certification matrix
- T0: strict Stats validation/projection and pure resource-state classification; malformed data cannot become zero.
- T1: both LiveViews cover every lifecycle/race, and component tests assert roles, busy/description wiring, stale labels, Retry, and no numeric grid when unavailable.
- T2: not applicable; no browser-side decision logic is introduced.
- T3: retain existing owner-scoped PostgreSQL/API aggregate tests. Add only semantic contract cases shared with token/cost KERs; persistence itself is unchanged.
- T4: one browser canary proves LiveSocket patches loading to unavailable to retry to ready without a false-zero flash and preserves layout.
- T5: `make test-release`, release-only Compose browser gate, exact pushed SHA/image deployment, health/auth probes, authenticated `/api/v1/stats` schema check, hosted lifecycle verification, and cleanup. No public provider is required.

Telemetry, rollout, and exit criteria
- Reuse `harden_llm_web_api_requests{operation="getStats"}`, request duration, structured logs, and OTel spans. Add no second telemetry pipeline and no widget-data dependency on telemetry.
- Cut over both LiveViews and the component together; no database migration or retained-data rewrite is required.
- This KER closes when initial loading/failure/malformed success never show unverified numbers, successful empty still shows zero, refresh failure is explicitly stale, response ordering is deterministic, and the exact deployed widget passes the lifecycle canary.

Final resolution (2026-09-01)
- `9943ed901ae6d3181aac24557078e7a5b22568b7` introduced strict
  `LlmStatsProjection`, an explicit `AsyncResult` component contract, and one
  lifecycle across Workspace and History. Loading/unavailable states contain
  no numeric grid; stale state keeps and labels only a prior validated snapshot;
  successful empty remains an authoritative zero.
- Stable async references reject stale completion order, malformed 2xx stats
  remain protocol errors, and manual retry uses the existing `HardenAPI`
  boundary. PostgreSQL remains the only product-data source.
- Projection, component accessibility, both LiveViews, reverse completion
  order, browser, release, and authenticated production checks passed. Exact
  closure evidence is on issue `#46`.
