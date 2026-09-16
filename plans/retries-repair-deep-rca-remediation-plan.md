# Retries & Repair deep RCA and remediation plan

- Plan ID: `PLAN-HLLM-RECOVERY-BOUNDARIES-002`.
- Version: `2.1.0`.
- Status: implemented; R01-R04 complete and release-certified in the implementation worktree.
- Date: `2026-09-15`.
- Reviewed baseline: branch `feat/recovery-policy`, source `f0c67b976da14dbc45ca4d36296c46a9c18fd012`.
- Predecessor: [retries-repair-boundary-consolidation-plan.md](retries-repair-boundary-consolidation-plan.md), the record of completed P00-P04 work and its retained certification.
- Scope: transport classification, accounting coverage, cache replay and storage, and cache-write reporting across the Go API and Phoenix frontend.

This revision replaces the first proposal's broad integrity rewrite with corrections inside the existing owners. It adds the two reproduced defects, removes unsupported internal-misuse scenarios, and makes every implementation phase end with passing checks. It retains the current recovery policy, attempt budget, selected target, schema, cache key, response projection, and host-owned UI state. Section 5 defines the contracts; Section 6 supplies the file-level implementation order, fixtures and expected results; Section 7 supplies runnable checks. The implementation of R01-R04 is complete; Section 9 records the evidence and the remaining browser/live-provider boundary.

## 1. Decision and ownership

Use the current architecture with small shared functions at the boundaries that already need the same behavior.

| Owner | Responsibility |
| --- | --- |
| Guarded HTTP transport and shared provider error helpers | Endpoint policy, dispatch observation, bounded reads, transport failure classification, HTTP status and `Retry-After`. |
| Protocol normalization in `internal/providers` | Provider envelopes, completion markers, output extraction, protocol-specific usage/cost extraction and search projection. |
| `internal/accounting` | Validated arithmetic and aggregate coverage of observed provider work. |
| Existing runtime loop | One retry/repair budget; original parent deadline; result admission; result/provider ledger separation; cache lifecycle. |
| Cache persistence | One accepted result projection plus owner, key/version and timestamps. |
| Gateway and Phoenix | Persist and present canonical execution facts through OpenAPI; no retry decisions or accounting reconstruction. |

Runtime admission validates output, normalized accounting, search metadata and the original structured schema for both fresh results and replay. It does not re-parse provider completion. Completion is established by the provider normalizer before a fresh result reaches admission; cache writes contain only accepted results.

Keep `maxAttempts` as the total execution-slot budget. `retryOn: []` disables ordinary retries; `repairInvalidOutput` independently permits structured repair within remaining slots. No fallback provider, middleware retry owner, new service, compatibility reader, generic identity framework or second UI state owner is introduced.

## 2. Evidence and limits

The baseline review traced the production paths and ran:

```bash
go test ./internal/providers ./internal/runtime ./internal/retry -count=1
```

All three packages passed. The same review also used two temporary Go-overlay probes to exercise missing assertions without modifying the checkout or contacting a provider:

| Probe | Observed baseline behavior | Required behavior |
| --- | --- | --- |
| `TestReviewDispatchedAccountingCoverage/after_dispatch` | A dispatched failure without accounting followed by a measured success reports three tokens, $0.01, usage `complete`, cost `exact`, and zero unknown observations. | Preserve the known amounts; aggregate usage/cost coverage must be incomplete. The delivered result retains its own complete accounting. |
| `TestReviewAttemptTimeoutDuringSearch` | An attempt-local timeout during Jina search produces one request/attempt and terminal `timeout`, although the call's parent remains active and two network attempts are allowed. | The failure is `network`; a second attempt is allowed by the existing policy. |

Both defect probes failed their intended assertions. The accounting control with a failure before model dispatch passed. Temporary probe files were removed; their scenarios must become committed regressions in R01. These observations establish local behavior, not deployed or live-provider behavior.

Earlier race and release results belong to the predecessor evidence; they are not fresh certification of this proposal. Other findings below are source-traced or selected contract changes, not claimed observed production incidents. Application tests need not be rerun merely to edit this document.

## 3. Root causes and priority

Existing RCA identifiers are retained where their finding still applies. New findings use `RCA-016` and `RCA-017`; withdrawn and deferred items are identified separately.

| Finding | Evidence and cause | Required correction |
| --- | --- | --- |
| `RCA-016` P1: unknown dispatched work loses coverage | Reproduced. [Runtime](../internal/runtime/execute.go) skips an unavailable ledger; [accounting](../internal/accounting/accounting.go) treats unavailable values as additive identities. No-work and work-without-measurement become indistinguishable. | Observe dispatch separately from measurement availability. Preserve known subtotals and incomplete coverage across all attempts, including zero-valued measured observations. |
| `RCA-017` P1: nested timeout ends the logical call | Reproduced. [Router](../internal/providers/router.go) passes its attempt context to Jina and returns the search error unchanged. [Jina](../internal/providers/web_search.go) regards that context as its parent, so the runtime receives an overall deadline error. | Normalize the search failure against the original call parent and the attempt context before runtime classification. Keep the attempt timeout covering both search and model work. |
| `RCA-001` P1: rejected redirects can repeat | Source-traced. [Redirect rejection](../internal/providers/endpoint.go) returns an untyped error; Router and Jina can classify it as network failure. | Return the existing endpoint-policy error type; use the shared classifier in both paths. Verify one request and no redirected dispatch. |
| `RCA-002` P1: oversized error responses can repeat | Source-traced. Router/Jina classify a non-2xx status before checking the response-size failure. | Make an observed size breach terminal before status classification; preserve bounded-read behavior even when a reader returns bytes and an error together. |
| `RCA-005` P1: error-response accounting is dropped | Source-traced. Non-2xx paths return status metadata without normalizing available usage/cost. | Extract valid accounting through protocol normalization from complete decoded objects, including complete JSON obtained on an interrupted read. Do not salvage truncated JSON or require every accounting field to be present. |
| `RCA-010` P2: accounting failure can be masked | Source-traced. Interrupted-read paths discard normalization errors; runtime only substitutes a ledger-validation error when another failure is absent. | Make explicit invalid accounting terminal, retain independently valid facts and earlier totals, and never add invalid values. Test the real provider read/normalization path, not only an invented executor. |
| `RCA-003` P1: cache replay bypasses result admission | Source-traced. Cache hits validate accounting but skip output/schema admission. | Share protocol-independent result admission; reject an invalid cache entry without model/search dispatch or semantic repair. |
| `RCA-004` P1: replay changes JSON numbers | Source-traced. [Cache decoding](../client_cache.go) uses `json.Unmarshal` into `any`, unlike fresh structured decoding. | Decode the projection with `UseNumber` and require exactly one complete JSON value. Prove fresh/replayed value equality for large integers and precise decimals. |
| `RCA-006` P2: stored producer attribution is unchecked | Corrupt-row boundary case, not evidence of a misrouted production request. Cached producer metadata is accepted without comparison to the requested prepared target. | Check the existing provider/protocol/normalized-endpoint/model fields at replay. Profile aliases may share cache entries. Method/path/projection stay covered by the existing operation hash. |
| `RCA-007` data minimization and duplication | Source-traced. The cache retains the prepared operation and raw envelope without a replay consumer. Postgres also stores usage/cost copied from the canonical result. | Remove redundant cache fields and their construction. Retain the canonical result, including its accounting and producer. This reduces retention; it does not make generated output free of user data. |
| `RCA-011` selected success contract | Source-traced. A cache write error is returned after the provider result has already been accepted. | Keep accepted inference successful, expose `write_failed`, and do not redispatch. Include API, UI, traces and telemetry in the same phase. |

Scope decisions from the review:

- Withdraw `RCA-008` and `RCA-009` as implementation drivers. The public [Client](../client.go) constructs its Router; arbitrary executor injection and opaque-request mutation are not supported caller paths. Keep existing fixed-target regressions; add no general repair-identity guard or selected-target representation change.
- Withdraw `RCA-013` as a standalone hardening task. Public calls reject nil contexts and normal constructors initialize clocks. Do not build helpers to support invalid manual internal construction.
- Defer `RCA-012` and `RCA-015`. No new repair-size bound, provider-output fallback or speculative stream rule is justified by the current evidence.
- Retain `RCA-014` as a documented limitation: observed request dispatch does not prove remote execution or billing. No exactly-once claim or idempotency subsystem is proposed.
- Preserve host-owned recovery state and the ordered state writer. UI work concerns cache-write reporting; change retry/repair help only if it contradicts the existing policy.

## 4. Alternatives considered

| Approach | Tradeoff | Decision |
| --- | --- | --- |
| Patch each Router/Jina/cache branch separately | Small initial edits, but preserves duplicated classification and admission rules. | Reject. Share only rules with actual multiple callers. |
| Refactor within current owners | Fixes reproduced behavior, deletes duplicate paths and preserves public recovery semantics. | Selected. |
| Add a universal response/integrity layer | Needs protocol, pricing, completion and identity knowledge already owned elsewhere. | Reject. It creates another owner and broader coupling. |
| Keep extra cache diagnostics and pass them through the existing redactor | Feasible without a second redaction system, but retains copies with no current replay consumer. | Prefer deleting the copies; keep existing redacted trace and parse-failure artifacts. |
| Treat every cache error as a miss | Improves availability but silently authorizes new provider work when cache access or integrity failed. | Reject. Reads/integrity remain explicit failures; only writes after accepted inference are non-fatal. |
| Invalidate all cache rows during column removal | Simplifies an imagined format transition but causes unnecessary provider work when the projection has not changed. | Reject. Preserve valid projections through the same reader and key. |

## 5. Required behavior

### 5.1 Transport and failure precedence

Reuse the guarded client, bounded reader and HTTP-status/`Retry-After` helper. Extract shared transport classification from Router/Jina; do not make the HTTP helper parse protocol accounting or completion.

The original call parent is authoritative for cancellation and its deadline. An attempt timeout remains a network failure while that parent is active, whether it occurs during Jina search, model dispatch or a body read. The Jina-local timeout also remains network. Do not move the model-attempt timeout to start after search, or classify every deadline error as retryable.

For a failed attempt, apply this precedence:

1. Original parent cancellation/deadline stops execution and new result admission.
2. Endpoint/TLS policy rejection and an observed response-size breach are terminal.
3. Explicit invalid accounting is terminal `ACCOUNTING_INVALID`; retain HTTP status as metadata when available.
4. Otherwise, non-2xx HTTP status owns classification over diagnostic body text or an interrupted body read.
5. Otherwise, use the existing protocol failure or transport/read classification.

Keep absent accounting distinct from invalid accounting. Extract valid partial measurements from a complete decoded JSON object; do not parse a truncated object, promote error output to success, or manufacture missing values. A rejected output can still contribute accounting. Jina search fees remain outside the model ledger.

Preserve tested EOF/reset/timeout behavior while consolidating classification. A typed security error must not become network, and a real transient transport failure must not become terminal merely because the helper changed. No error-message substring classification is introduced.

### 5.2 Accounting coverage

Use one aggregation implementation in `internal/accounting`; runtime supplies dispatch and normalized measurements for each attempt. Any small amount of state needed to distinguish an empty aggregate from unmeasured work belongs to this per-call accumulator, not an additional public ledger.

| Observation | Provider aggregate |
| --- | --- |
| No model dispatch and no reported model accounting | No contribution; do not create an unknown charge. |
| Model dispatched, usage unavailable | Track missing usage coverage. All-unmeasured usage remains unavailable; measured usage plus unmeasured work is partial. |
| Model dispatched, cost unavailable | Add one unknown cost observation using the existing cost representation. Do not also increment uncertainty already represented by a partial/unknown cost. |
| Valid usage and/or cost reported | Add independently valid facts with checked arithmetic. Explicit measured zero remains known zero. |
| An accounting component is invalid | Terminate; retain earlier and independently valid measurements, and retain uncertainty about the dispatched work. Never turn invalid data into a retryable absence. |

Known-then-unknown and unknown-then-known must produce the same aggregate amounts and coverage. Usage coverage must not be inferred from cost coverage. Keep the accepted result ledger separate: it describes the delivered output only. Cache replay performs no new model work and leaves the provider ledger unavailable.

### 5.3 Result admission and replay

Create one runtime admission function over normalized results. It checks nonblank output under the existing text/structured contract, accounting validity, canonical search metadata and the original structured schema. Provider completion remains entirely in protocol normalization; add no cached completion flag or runtime protocol parser.

Only invalid extracted structured output from an otherwise completed fresh response may enter semantic repair. A malformed cache projection returns `CACHE_INTEGRITY` with no retry, repair, cache-as-miss conversion, DNS, search or model request.

The cache adapter checks current record schema/version/hash, decodes with `UseNumber`, and requires a complete value. Runtime compares cached producer fields with the existing prepared target, excluding profile ID; retain the original producer's attribution. Do not add method/path/projection fields to `ExecutionTarget` or change configured `SelectedTarget` reporting.

Replay existing valid response-projection `v3` entries using the same reader. Preserve the `operation-v2` namespace, cache-record schema version `2` and hash fixtures: deleting unused sidecar fields does not change the accepted projection. Previously obsolete keys stay unreachable.

### 5.4 Cache storage

Store one canonical result projection containing output, optional search metadata, result accounting and producer, with owner/key/version/timestamps at the persistence boundary.

Remove public cache `Operation` and `RawProviderEnvelope` fields and unused runtime/provider envelope copies. Remove the now-unneeded operation argument from the internal cache-write interface. Preserve the separate parse-failure diagnostic/redaction path.

In one forward migration, drop `llm_operation_cache.operation`, `provider_envelope`, and the duplicate cache-only `usage`/`cost` columns. The latter are currently copied from `result.accounting` in [ownerCacheStore](../internal/gateway/run_service.go); no cache accounting query consumes them. Update cache records/queries and remove the unreferenced `LatestCache` accessor. Run/history/statistics projections are outside this storage cleanup.

Preserve existing `result` JSON, owner isolation, keys, timestamps and upsert behavior. Do not purge rows, transform historical output, rewrite the original migration, add a legacy reader or promise a cold cache. Test that an entry created before the migration still replays after it with the same exact values and zero provider requests.

Describe this as reduced redundant retention. Generated output/search may themselves contain user content; this change is not a promise of prompt-free storage. Removing exported fields is an intentional package contract change and must be documented with the coordinated code change.

### 5.5 Cache-write success and reporting

After result admission succeeds, a failed cache write leaves inference successful: retain output, both ledgers and result source, set `Cache.Status = "write_failed"`, and keep `Written=false`. Do not retry the write or provider work. Cache lookup and integrity errors remain failures.

This success boundary also applies if the cache write itself encounters a deadline after inference was accepted. Parent cancellation before admission still fails the call. No second timeout policy is added.

Update all existing consumers in R03:

- Document `write_failed` in OpenAPI and the public library docs. The current wire status is a bounded string, not an enum; changing its description alone is insufficient.
- Preserve the status in run/history/trace results. A domain `cache.write` failure observation must not be mislabeled as a failed lookup or successful write.
- Extend telemetry's bounded outcome mapping and keep the write span failed while the successful inference remains successful. Never label metrics with raw storage errors.
- Render a clear cache-save failure in Phoenix live results and history/trace projections, with deterministic component/API tests. Do not let it appear as "Unknown" or make successful inference look failed.
- Preserve one host-owned recovery policy and the existing info control; no new setting is required.

## 6. Implementation guide

Implement R01, R02, R03 and R04 in order. The function names proposed below are internal implementation names, not additions to the public library API. Keep each change in its existing package; no new service, general middleware or test runner is needed.

For every step: read the named functions, add the relevant regression, observe the intended failure, implement, then rerun the same selector. A control case that already passes is useful coverage, not a required RED. Compilation errors, missing services and zero selected cases are setup failures. Do not add the next phase's failing tests before the current phase is green.

### 6.1 Start and resume instructions

1. Read `AGENTS.md`, `docs/liveview-go-testing-guidelines.md`, Sections 1-5 here, and ADR-HLLM-020/021. Use the current checkout as the source of truth.
2. Record `git status --short --branch` and `git rev-parse HEAD`. The reviewed source is the SHA in this document's metadata. If it has moved, inspect the affected-file diff and reuse any already implemented work; do not overwrite unrelated changes or repeat completed steps.
3. Read the execution log at the end. Resume from the first incomplete step whose predecessors are verified. A phase label or a previous model's statement is not evidence of passing tests.
4. Register the proposed test IDs before source comments reference them. They were unused at this baseline. If they have since been assigned, allocate the next free IDs and update this plan/catalog mapping together. Add a traceability comment to each new or modified Go test group, for example `// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-223`; use `# WEB-TEST-076` for the frontend cases.
5. Use the pinned PATH in Section 7. All fixtures are synthetic and local; deterministic tests do not require the production `.env`, bearer sessions or provider keys.
6. After each phase's tests pass, record the commands, selected tests, results and changed files. Keep ordinary iteration on the current authorized branch. This plan does not authorize promotion, deployment or browser/live-provider testing.

### 6.2 File and test ownership

These are the primary edit locations, not a requirement to change every file.

| Work | Production entrypoints | Existing tests/setup to reuse |
| --- | --- | --- |
| Transport, redirects, timeouts, bounded reads | `internal/providers/endpoint.go`: guarded client and redirect hook; `internal/providers/router.go`: `Router.Execute`, `readBounded`, `providerHTTPErrorAt`, DNS/TLS classification; `internal/providers/web_search.go`: `jinaSearcher.Search` | `internal/providers/requests_test.go`, `internal/providers/normalization_test.go`, `internal/providers/web_search_contract_test.go`; existing `roundTripFunc`, `errorReadCloser`, `mustURL` and local TLS-server patterns |
| Protocol accounting extraction | `internal/providers/normalize.go`: `normalizeDecodedResponse`, `normalizeUsage`, `normalizeCost`; Router's non-2xx and interrupted-read branches | `TestRecoveryBoundaryAccountingCacheRead`, `TestRecoveryBoundaryCompletion`, provider normalization fixtures |
| Provider totals and result admission | `internal/accounting/accounting.go`; `internal/runtime/execute.go`: attempt loop, cache-hit return, output/schema checks; `internal/runtime/search.go` | `internal/accounting/accounting_test.go`; `internal/runtime/repair_test.go`: `accountingSequenceExecutor`, `TestExecutionIdentityAndGlobalAttemptBudget`, repair and budget holdouts |
| Cache decoding and write contract | `client_cache.go`: `cacheAdapter.Get/Set`; `cache.go`: public `CacheRecord`; `internal/runtime/cache.go`: internal cache interface | `client_cache_test.go`: `memoryCache`, replay and old-record rejection; `client_test.go`: `fixedExecutor`, credential/ID fixtures |
| Database persistence | `internal/postgres/cache.go`, `internal/postgres/records.go`, `internal/postgres/resources.go`; new `internal/postgres/migrations/0007_cache_projection.sql`; `internal/gateway/run_service.go`: `ownerCacheStore`; `internal/gateway/shared_runtime.go`: delegation | `internal/postgres/cache_test.go`, `internal/postgres/repository_test.go`; `internal/integrationtest/pool.go`: `PostgresLease`; `internal/gateway/run_test.go` and `internal/gateway/shared_runtime_test.go` |
| Trace and telemetry facts | `internal/traces/traces.go`: `Project`; `internal/runtime/telemetry.go`: `StartCache`, `boundedCacheOutcome`; `client.go`: result projection | `internal/traces/parity_test.go`; `internal/runtime/telemetry_test.go`: in-memory span exporter and metric reader |
| API contract | `api/openapi.yaml`; `frontend/lib/harden_llm/llm_diagnostics_wire.ex` | `internal/gateway/openapi_contract_test.go`, `internal/gateway/run_output_test.go`; existing wire assertions in `frontend/test/harden_llm/llm_trace_projection_test.exs` |
| UI projection | `frontend/lib/harden_llm/llm_trace_projection.ex`; `frontend/lib/harden_llm_web/components/llm_trace_components.ex`; `frontend/assets/css/app.css` | `frontend/test/harden_llm/llm_trace_projection_test.exs`, `frontend/test/harden_llm_web/components/llm_trace_components_test.exs`, `frontend/test/harden_llm_web/live/history_trace_test.exs`; shared data in `frontend/test/support/api_fixtures.ex` |

Test executors are legitimate fixtures for runtime policy/arithmetic. They are not evidence of a public executor extension point. For observed model dispatch, use a real local HTTP transport: a custom `RoundTripper` does not automatically emit `httptrace.WroteHeaders`. Do not manually set that callback merely to make a transport assertion pass.

### Phase R01: Correct transport and accounting

#### R01.S01 — Register the contract and regressions

Edit the canonical backend test catalog at `plans/from_utility-llm/harden-llm-self-hosted-test-spec.md` and frontend catalog at `plans/from_utility-llm/phoenix-liveview-frontend-spec.md`. Register all Section 7 IDs with type, owner/location, command and acceptance criteria, following the existing TEST-212–222 and WEB-TEST-074/075 entries.

Add `docs/adr/ADR-HLLM-022-recovery-integrity-boundaries.md`. Record the settled Section 5 choices, particularly accounting uncertainty, parent versus attempt deadlines, preserved cache keys/rows, and successful inference after a cache-write failure. Align the relevant recovery/cache paragraphs in `plans/from_utility-llm/self-hosted-go-stack-spec.md`; preserve ADR-HLLM-020's public recovery-policy shape. Do not rewrite the predecessor's completed execution evidence.

No failing application test is required for this documentation step. Check references/IDs; behavioral RED starts below.

#### R01.S02 — Reproduce transport and timeout failures

Add tests with prefixes `TestRecoveryIntegrityTransport` (TEST-223) and `TestRecoveryIntegrityTimeout` (TEST-225). Extend the existing provider test files instead of creating a second harness.

For request-count tests, use `MaxAttempts=2`, the relevant retry categories, zero backoff and an injected no-sleep wait. Each test owns its server, counters, contexts and cleanup. Synchronize cancellation with request-entry channels; do not use arbitrary sleeps to guess whether a request began.

| Case | Fixture | Required oracle |
| --- | --- | --- |
| T01 | Model response redirects to a second local handler | One initial request, zero redirect-target requests, terminal `ENDPOINT_POLICY`, no cache write. |
| T02 | Jina returns a redirect | One search request, zero redirect-target/model requests; same terminal policy classification. |
| T03 | Model and Jina each return 429 or 503 with a body larger than a small configured limit | Each combination stops after one request with `RESPONSE_TOO_LARGE`/other; no accepted output or cache write. |
| T04 | A reader returns more than the limit and a read error in the same read | The size breach still wins. A bounded read must not become an ordinary retryable read failure. |
| T05 | 503 plus an interrupted body and contradictory diagnostic text | Server classification and valid `Retry-After` survive; body text does not reclassify it. Reuse the injected-clock HTTP-date test. |
| T06 | Permanent DNS or certificate failure versus temporary DNS/EOF/reset | Permanent failures stop; documented transient failures retain the configured retry behavior. Preserve the existing dispatch assertions. |
| D01 | Real Router preparation, Jina transport waits for its request context; attempt timeout is shorter than Jina timeout; overall parent is alive | Two search requests/attempts, zero model requests, network failures and an active overall parent. This is the reproduced defect. |
| D02 | Cancel the overall parent after search enters its request | One search request, no model request or second attempt, terminal canceled. |
| D03 | Overall parent deadline expires before the attempt/Jina timeout | Terminal timeout, no second attempt. Do not relabel it as network. |
| D04 | Jina's own timeout expires first while attempt/parent are alive | Network failure eligible for the second slot; search failures do not set model `ProviderDispatched`. |
| D05 | Search succeeds; both model requests then hit the attempt timeout | One successful search due to the per-call memo, two model attempts within the same budget, network classification. |
| D06 | Repeat D01 with `retryOn: []`, and separately with `maxAttempts: 1` | One attempt in each case; fixing timeout classification must not override either control. |

D01 may use the existing test `RoundTripper` for Jina that waits on `request.Context().Done()`; it still exercises `jinaSearcher.Search`, Router and runtime. Use a real local transport when asserting model-header dispatch. No wall-clock speed threshold is part of these assertions; the test command's timeout bounds a stuck test.

Run the TEST-223/225 selectors from Section 7. Record which baseline assertions failed and which controls already passed.

#### R01.S03 — Implement the shared error rules

Perform these edits in this order:

1. Return `endpointPolicyError` from the guarded client's existing redirect hook. Keep redirects disabled.
2. Extract one transport-error normalizer in `internal/providers` using the existing DNS/TLS predicates. It receives the caller parent, the local request context and the error; it preserves typed policy/provider metadata and applies Section 5.1. Use it for model and Jina `Do` errors.
3. At Router's `prepared.webSearch.results` error return, normalize against Router's original parent and attempt context. Jina can legitimately return its caller's deadline, which is the attempt deadline at that layer. The outer Router must distinguish that from the logical-call deadline.
4. Keep the timeout start before search. Preserve `SearchMemo`; do not add a background context, restart the overall deadline or place another retry loop inside Jina.
5. In `readBounded`, check the observed length against the maximum before returning a simultaneous read error. Keep the `maximum+1` bounded-read approach; do not read an unbounded body to diagnose it.
6. Share the size/status/read-error ordering needed by Router and Jina in a small provider helper. It classifies HTTP/transport facts only. Protocol accounting remains in the separate normalizer added in R01.S05.
7. For a local timeout, return a bounded network provider error whose underlying error does not still satisfy `errors.Is(err, context.DeadlineExceeded)`. Otherwise `retry.Classify` would treat it as a terminal parent timeout before seeing the explicit category. Preserve the original context error only for the actual parent.
8. Rerun TEST-223/225 and existing transport/Retry-After controls. Remove the replaced branches only once their assertions pass. Do not change `retry.Classify` to compensate for incorrectly normalized provider errors.

Keep `ProviderDispatched` assigned from the actual model transport event on every return path. A Jina request never sets the model fact. Do not introduce a generic response finalizer that owns completion, schema validation or pricing.

#### R01.S04 — Add accounting regressions with exact expected values

Add `TestRecoveryIntegrityAccounting*` cases under TEST-226. Use the existing `accountingSequenceExecutor` for runtime sequences and real provider body fixtures for normalization/read behavior.

Define a measured observation M as the successful return of `CompleteUsage(2, 0, 0, 1, 0)` plus `ExactCost(0.01, "reported")`; the usage constructor returns `(Usage, error)`, so check that error in fixtures. Define U as a dispatched attempt with both measurements unavailable. P is a pre-dispatch failure with no measurements. Supply retryable failures before the last accepted result as needed; pure accumulator tests also cover sequences without a successful final output.

| Case | Sequence/input | Required provider accounting |
| --- | --- | --- |
| A01 | U then M | Three known tokens, usage partial; $0.01 subtotal, cost partial, one known and one unknown cost observation. Final result ledger is M. |
| A02 | M then U | Same aggregate amounts/coverage as A01; if the final output is accepted without accounting, its result ledger remains unavailable. |
| A03 | U then U | Usage unavailable; cost unknown, zero known subtotal/observations and two unknown observations. |
| A04 | P then M | Usage complete and cost exact; no unknown cost observation. |
| A05 | M then M | Six tokens, $0.02, complete/exact and two known observations. |
| A06 | A measured zero-token, exact-zero-cost observation then U | Zero amounts with partial coverage, one known and one unknown cost observation. Measured zero is not missing data. |
| A07 | Complete usage with unavailable cost; unavailable usage with an exact reported cost | Track each dimension independently. Missing cost becomes unknown; missing usage is not inferred from cost completeness. |
| A08 | An already partial/unknown cost observation | Retain its existing unknown-observation count; do not add another merely because the status is not exact. |
| A09 | 503 with a complete JSON object containing usage and reported cost | No accepted output; retain usage/cost and server status. Retry only if policy permits. |
| A10 | The same object followed by a body-read error; then a truncated-object control | Complete decoded facts survive the read error. The truncated JSON contributes no guessed measurements. |
| A11 | Explicit invalid usage plus valid reported cost, and the inverse | Terminal `ACCOUNTING_INVALID`; retain the independently valid dimension and previous totals. The invalid dimension cannot enter arithmetic or cause a retry. |
| A12 | Checked-addition overflow after prior valid observations | Terminal accounting error; prior amounts remain. Never replace the accumulated ledger with an empty ledger. |

A concrete A09 body for the Responses protocol is:

```json
{"error":{"code":"unavailable"},"usage":{"input_tokens":2,"output_tokens":1},"cost":0.01}
```

For A11 on the interrupted 2xx read path, use a complete object with `status: "completed"`, a nonblank `output_text`, a negative token field and a valid reported cost, then return `io.ErrUnexpectedEOF` after those bytes. Assert nil output, the accounting error, retained valid cost and no next attempt. This exercises the current production branch that discards `normalizeDecodedResponse` errors.

Add a cache-replay control: cached result accounting is preserved, provider accounting is unavailable, and no accumulator observation is added for a hit.

#### R01.S05 — Implement accounting extraction and aggregation

**Protocol extraction:**

1. Extract the usage/cost part at the start of `normalizeDecodedResponse` into one helper in `internal/providers/normalize.go`, for example `normalizeResponseAccounting(prepared, response)`.
2. Reuse `normalizeUsage` and `normalizeCost`; do not introduce parallel field readers or price calculations. Normalize invalid dimensions to unavailable in the returned partial ledger while returning the bounded accounting failure separately. A valid explicitly reported cost can survive invalid usage.
3. Call this helper from ordinary normalization and from non-2xx/partial-read paths when a complete JSON object is available. A non-2xx body does not need a successful completion marker to report accounting.
4. For partial Responses streams, retain the existing collector's available response object and completion rules. Preserve accounting-normalization errors; do not reconstruct output from deltas or reinterpret a read failure as a completed result.
5. Combine the candidate transport/status failure and accounting failure using Section 5.1. An invalid accounting object is terminal even with a 503; merely missing accounting leaves the status authoritative. Retain status metadata when available.
6. On every failed-output path, clear output/search before returning. Keep validated accounting. Raw-envelope removal belongs to R03, so the current envelope contract must still compile and pass in this phase.

**Per-call aggregation:**

Add a small `ProviderAccumulator` in the existing `internal/accounting` package. Suggested interface:

```go
func NewProviderAccumulator() ProviderAccumulator
func (a *ProviderAccumulator) Observe(dispatched bool, observation Ledger) error
func (a ProviderAccumulator) Ledger() Ledger
```

Its private state needs the running ledger and a missing-usage flag. The ledger's usage status already distinguishes a known zero observation from no measurement; no additional public counts or persistence fields are needed.

Implement `Observe` as follows:

1. Validate usage and cost independently. Preserve valid dimensions even if the other is invalid. Return an accounting error for invalid input or arithmetic; never silently downgrade that failure to absence.
2. Add valid measured usage with the existing `AddUsage`. If an observed attempt lacks valid usage, remember missing coverage. An attempt is observed when dispatch is true or it supplies model-accounting facts; a pre-dispatch failure with no facts contributes nothing.
3. Add valid measured cost with `AddCost`. For observed work with unavailable/invalid cost, contribute one `UnknownCost("unknown")`. Existing partial/unknown observations already represent their uncertainty and must not be counted twice.
4. Commit only valid arithmetic results for each dimension; retain earlier amounts when that dimension cannot be added. A failed usage addition marks missing coverage. If a cost amount cannot be added, retain its prior subtotal and record unknown coverage using the existing checked cost arithmetic; still return the original accounting error. Do not force an invalid or overflowed aggregate into the public ledger.
5. `Ledger()` returns a value copy. If measured usage exists and missing coverage was observed, return usage status partial. If no measured usage exists, leave it unavailable. Cost coverage comes from the existing cost arithmetic.
6. Do not change `AddUsage`, `AddCost` or `AddLedger` so that their generic empty-ledger identity becomes unknown work. Dispatch-aware uncertainty belongs only to this accumulator.

In `runtime.Execute`, create one accumulator for provider attempts, feed it once immediately after each execution, and copy its ledger into `record.Accounting.Provider` even when the attempt fails. Replace the `hasProviderAccounting` skip path. Preserve the existing normalization of a fresh provider's absent ledger; malformed cached accounting is not repaired with defaults in R02.

Do not modify `result.Accounting` to contain cumulative totals. On success, `record.Accounting.Result` and cached accounting use only that accepted result's ledger. Convert an accumulator error to the existing bounded accounting provider error before retry/repair eligibility is evaluated, using Section 5.1 to combine it with any existing failure. It overrides an ordinary HTTP/read failure, while original parent cancellation/deadline and terminal endpoint/TLS/size failures retain their higher precedence.

#### R01.S06 — Close the phase

Run TEST-223, TEST-225 and TEST-226 selectors, plus the retained provider completion, runtime repair/budget and accounting arithmetic cases. Then run `make test-fast`.

Inspect the diff for duplicate Router/Jina classification and the removed runtime accounting skip path. Keep one protocol-accounting extraction function and one provider accumulator. Record the exact RED/GREEN outcomes for D01 and A01.

**Exit:** R01 cases and the fast gate pass. No terminal security/size/accounting case repeats; search-local timeout does not end an active parent call; unknown measurements cannot become complete aggregate coverage.

### Phase R02: Make cache replay lossless and validated

#### R02.S01 — Add replay/admission regressions

Use prefix `TestRecoveryIntegrityCacheAdmission` under TEST-224. Reuse `memoryCache`, the root client fixtures and runtime fixtures. Make cache corruption explicit by editing only the stored projection after a valid write.

For precision cases use this output, with an object schema that requires integer `id`, number `fraction` and rejects additional properties:

```json
{"id":9007199254740993,"fraction":0.12345678901234567890123456789}
```

Assert `json.Number` values and exact digits after the public call/replay. Do not compare through `float64` or reuse a JSON-equality helper that unmarshals into ordinary `any`; that would hide the defect.

| Case | Input or mutation | Required oracle |
| --- | --- | --- |
| C01 | Valid current cached text/structured output | Exact output/result accounting/search/producer, new call/trace IDs, zero execution attempts and no new provider accounting. |
| C02 | Precision object above | Fresh and cached values preserve both numbers; neither becomes a string or rounded float. |
| C03 | Text `"0012"`; supported structured `0` and `false` | Preserve values/types; do not equate zero/false with absent output. |
| C04 | Whitespace-only cached text or null output | `CACHE_INTEGRITY`, no published result, no execution/repair/write. |
| C05 | Structured output violating the original schema | Same terminal cache behavior, even with repair enabled and spare attempt slots. Fresh schema-invalid extracted output retains its existing repair behavior. |
| C06 | Invalid/missing accounting status or negative counts | Terminal integrity failure; do not normalize malformed persisted accounting into valid unavailable accounting. |
| C07 | Invalid search mode, URL, citation range or oversized entry-point HTML | Terminal integrity failure without changing or trimming stored data. |
| C08 | Cache-record schema/version/hash mismatch; malformed or trailing projection JSON | Terminal integrity failure; keep the existing obsolete-record rejection control. |
| C09 | Producer provider/protocol/endpoint/model differs | Reject. Do not accept an entry merely because its embedded hash string matches the lookup. |
| C10 | Different source profile ID with the same prepared semantic target | Serve the valid entry and retain its original producer profile; selected target remains the current selection. |
| C11 | Cache lookup itself errors | Propagate the read failure with zero provider/search requests; never convert it into a miss. |

For rejected rows assert `Served=false`, `Written=false`, nil output and no result source. Preparation/credential resolution may still run; do not claim cache hits skip those operations. Assert zero DNS/search/model dispatch where the actual transport boundary is instrumented.

#### R02.S02 — Implement decoding and shared admission

In `cacheAdapter.Get`, use a typed `json.Decoder` with `UseNumber`, decode the existing `cachedProviderProjection`, then require the next decode to return `io.EOF`. Keep identity checks. Do not decode via floats, coerce JSON strings/numbers or reserialize through a lossy map. No provider-specific decoder dependency is needed in this persistence adapter.

Extract the normalized-result checks from `runtime.Execute` into one admission function used by its fresh and cache-hit branches:

1. Validate canonical accounting without filling missing statuses on cached data.
2. Apply existing `acceptedOutput` behavior. This does not replace the provider normalizer's existing `empty_response` category or ordinary retry policy.
3. Validate optional canonical search metadata in `internal/runtime/search.go`, using the current OpenAPI/strict frontend contract: native/jina mode, unavailable search cost, non-nil source list of at most 50 entries, HTTP(S) URLs with a host and no userinfo, nonnegative citation start with end greater than start, and entry-point HTML of at most 32,768 Unicode code points.
4. Keep URL validity in one small helper shared with provider search normalization, which already filters source URLs. Providers already import runtime; runtime must not import providers. Do not add new requirements about title content, unique sources, `Executed=true`, citation/source membership or output-relative offsets.
5. For structured calls, invoke the original `ValidateStructured` callback exactly once. Preserve its existing schema telemetry wrapper and repair feedback capture; no second schema compilation or validation pass is required.
6. On a cache hit, compare the stored producer's provider/protocol/endpoint/model against `targetFromPrepared`. Require a nonempty producer profile ID but do not require equality or current catalog membership. Keep method/path/projection identity in the existing hash.
7. Set the cache hit/output/result-source fields only after admission passes. Wrap persisted identity/decode/admission failures in one bounded `CACHE_INTEGRITY` constructor shared between cache adapter and runtime. Do not expose cached output or raw validation text in that error.
8. For fresh results, keep schema failures eligible for the existing repair path. Accounting, malformed search metadata and unexpected missing normalized output remain terminal. A cache validation error never enters the attempt loop.

Keep `SelectedTarget`, key/projection versions, repair budget and the internal cache-write signature unchanged during R02. Removing storage sidecars is a separate R03 change.

#### R02.S03 — Close the phase

Run TEST-224, retained cache replay/version/hash/schema cases and `make test-fast`. Verify the precision assertions inspect exact values and C10 preserves alias reuse.

Delete only replaced admission checks; preserve separate decoding/identity checks at the persistence boundary. Record model/search/DNS counters for a hit and a rejected cache entry.

**Exit:** exact numbers survive replay, malformed entries cannot cause provider work, fresh/cache admission shares applicable checks, and the fast gate passes.

### Phase R03: Simplify storage and finish cache-write failure reporting

#### R03.S01 — Add storage and write-failure cases

Register source comments with TEST-227/228 and WEB-TEST-076. Use prefix `TestRecoveryIntegrityStorage` for migration tests and `TestRecoveryIntegrityCacheWrite` for runtime/public/trace tests. Name the real gateway persistence case `TestRecoveryIntegrityCacheWriteStoredRun` and keep its integration build tag.

| Case | Setup | Required oracle |
| --- | --- | --- |
| S01 | Fresh leased database | All migrations apply; final cache table has only the retained columns from R03.S02; `Ready` succeeds. |
| S02 | Database at migration 0006 with valid projection-v3 entries for two owners and the C02 precision output | After production `Store.Migrate`, retained result JSON, owner/key/version/timestamps are unchanged; new code can read/replay it without conversion or dispatch. |
| S03 | Upsert and concurrent readers/writers on the current schema | One row per owner/version/hash, valid canonical result, preserved `created_at`, advanced `updated_at`, owner/version isolation. Preserve the existing concurrency oracle. |
| S04 | Repeated/concurrent production migrations | Applied versions are current and unique, retained data is unchanged, readiness is correct. |
| S05 | Existing recovery-policy migration tests and invalid-document tests | Preserve real migration locking, concurrency, idempotency and rollback assertions; update only the intentionally retired cache fields as described below. |
| W01 | Cache miss, accepted provider result, `Set` returns a sentinel error | Public call succeeds, output/result/provider ledgers stay correct, `write_failed`, `served=false`, `written=false`; one model invocation and one write. |
| W02 | Refresh with the same write failure | Same result; zero cache reads and one write. |
| W03 | Successful writes; cache off; cache hit | Existing statuses/counts remain: miss/refresh with written true, off with no store calls, hit with no model/write calls. |
| W04 | Read/integrity failure or provider/schema failure | No best-effort conversion of these failures; no write when output has not been accepted. |
| W05 | Store returns cancellation/deadline after accepted output | Inference remains successful and write failure is recorded; cancellation before admission remains terminal with no write. |
| W06 | In-memory OTel export and root domain trace projection | The call succeeds, the write span fails, bounded cache status is preserved, and lookup/write observations are distinct. |
| W07 | Actual RunService + Postgres + root Client + local provider + failing cache-store wrapper | HTTP/run status succeeds; owner-scoped history and trace read-back retain output/accounting/`write_failed`; one provider request and one attempted cache write. |
| U01 | Valid run/history/trace wire fixture with `write_failed` | Existing strict decoder accepts it without an old-format adapter. |
| U02 | Shared trace projection and rendered component | Success indication/output stay visible; cache label/title explicitly report save failure, never "Unknown". |
| U03 | History restore/trace view with the same canonical result | Same cache-save message and success state; no second UI state owner or retry event. |

W07's failing wrapper is test-only: delegate reads/deletes to the supplied owner-scoped store and fail its write. Construct the real root Client in the existing `CallerFactory` fixture pattern; do not return a prebuilt successful `RunOutput`. Run/history/trace persistence must still use real Postgres. Assert actual local provider traffic separately from wrapper counters.

For S03, pass fixed ordered timestamps through `CacheRecord` to prove creation-time preservation and update-time advancement. Do not use a sleep or elapsed-time threshold to distinguish writes.

Place cheap runtime/API-shape assertions outside integration tags. `internal/gateway/run_test.go` is integration-tagged; an ordinary `go test ./internal/gateway` does not certify W07.

#### R03.S02 — Remove the redundant cache representation

Keep this edit order so compiler failures identify all consumers:

1. In public `cache.go`, remove `CacheRecord.Operation` and `RawProviderEnvelope`. Retain `SchemaVersion`, `CacheVersion`, `OperationHash`, `ProviderResult`, `CreatedAt` and the public `CacheStore.Get/Set/Delete` signatures.
2. In `internal/runtime/cache.go`, remove the operation argument from `Cache.Set`. Update `cacheAdapter.Set`, runtime call sites and test cache implementations. Keep operation construction/hash generation in preparation/runtime.
3. In `client_cache.go`, delete operation serialization and raw-envelope validity/copy requirements. Marshal one `cachedProviderProjection` containing only output, optional search, result accounting and producer.
4. Remove unused `RawProviderEnvelope` fields/copies from `runtime.ProviderResult` and `CallRecord`, and delete `rawProviderEnvelope`/`rawEnvelopeVersion` construction in providers. Keep `ParseFailureResponse`, `captureParseFailureResponse`, provider parse-error `RawResponse`, `mustJSON` where still used, and redacted artifact persistence.
5. In Postgres `CacheRecord`, remove `Operation`, `Envelope`, `Usage`, `Cost`. Update `PutCache`/`Cache` to validate/store the result object and identity/timestamps only. Delete unused `LatestCache`; retain `DeleteCache` and `CountCache`.
6. Simplify `ownerCacheStore.Set`: pass through the canonical `ProviderResult` JSON as `Result`; remove the second accounting decode and `{}` defaults. Its `Get` still supplies cache-record schema version 2. Dynamic stores continue delegating to the same owner binding.
7. Add `0007_cache_projection.sql` at this baseline, containing only the coordinated column removal:

```sql
ALTER TABLE llm_operation_cache
    DROP COLUMN operation,
    DROP COLUMN provider_envelope,
    DROP COLUMN usage,
    DROP COLUMN cost;
```

The retained columns are `owner_id`, `cache_version`, `operation_hash`, `result`, `created_at`, `updated_at`. Preserve the primary key, owner foreign key and owner/update index. Do not add `CASCADE`, `IF EXISTS`, a table rebuild or an old-row conversion. The existing migration runner owns the transaction and applied-version record.

**Migration fixture procedure:**

1. Use `integrationtest.PostgresLease(t)`; never use an application database. Factor the existing `recoveryMigrationStore` setup into a test-only helper accepting an initial version, so old tests can still initialize through 0005 and S02 can initialize through 0006.
2. Seed historical cache rows using test-local SQL with that historical schema's four sidecar columns. Do not call the updated current `PutCache` against a pre-0007 schema, whose retired columns are still NOT NULL.
3. Snapshot all six retained columns before applying the new migration. Use Postgres JSONB equality or a `UseNumber` decoder for precision; the existing `jsonEqual` helper decodes floats and is not a valid C02/S02 oracle.
4. Invoke the real `Store.Migrate`; do not manually execute 0007 as a substitute for the runner. Verify absent columns with `information_schema.columns`, preserved rows, `AppliedMigrations`, readiness and repeat-run idempotency.
5. Exercise current `PutCache`/`Cache` and the owner cache through the root library on the migrated data. Compare exact output/accounting and zero provider traffic.
6. Preserve the existing `TestRecoveryMigration` full-chain concurrency test and invalid-document atomicity test. Update its expected version set for 0007. For its independent cache snapshot, explicitly compare the six retained columns on both sides; only the four now-retired fields are excluded. Keep every other table/column assertion and the original historical output fixture. S02 separately proves exact retained data and column retirement.
7. Keep historical migrations immutable. References to retired columns in 0001 or historical test seeding are expected; they are not runtime compatibility paths.

#### R03.S03 — Implement successful inference with a failed cache write

At the success branch of `runtime.Execute`, output, result accounting and result source have already been assigned. On `cache.Set` failure:

1. Set `record.Cache.Status = "write_failed"` and leave `Written=false`.
2. End the cache-write telemetry callback with that bounded status and the real error.
3. Return the accepted record with nil call error. Do not enter retry/repair scheduling, repeat the write or reset IDs/accounting.
4. Keep the normal successful-write path setting `Written=true`. Read/integrity failures and failures before result admission remain errors.

Do not add a new cache-warning object, public error field or retry toggle. Project the existing immutable cache facts through these consumers:

| Consumer | Concrete edit and assertion |
| --- | --- |
| `client.go` / gateway run result | Existing result projection carries `write_failed`; public error is nil and REST status is succeeded. Keep request/trace/run identity and both ledgers. |
| `internal/traces/traces.go: Project` | For final `write_failed`, retain the prior lookup outcome as miss for cache mode or refresh for refresh mode. Append one `cache.write` observation with outcome `failure`; never emit write success. Keep contiguous sequence numbers and overall trace success. Derive this from recorded mode/status, without new duplicate state fields. |
| REST history/trace | Verify stored canonical `RunOutput.cache` survives read-back. Keep gateway attempt observations in their current format; do not duplicate the root domain-trace event system in the gateway. |
| `internal/runtime/telemetry.go` | Add `write_failed` to `boundedCacheOutcome`. Write span is error; runtime/call spans remain success; cache-operation metric has operation write, cache outcome write_failed and error outcome. Never export raw store-error text as a label. |
| `api/openapi.yaml`, library docs | Describe the new status and success boundary, without changing `RecoveryPolicy`, `RunResult` version or turning the free-string cache status into a new enum. Document removal of public cache sidecars. |
| Phoenix projection | In `cache_status/1`, `cache_status_label/1`, `cache_status_title/1`, recognize the status. Use label `Cache save failed` and title `The response completed successfully, but it could not be saved to cache.` Keep overall success/output unchanged. |
| Shared component/CSS | Reuse the existing badge, title and aria label. Add `.ullm-cache-status-write_failed` to the existing muted-accent cache selector group; do not copy declarations or add a hook/control. |

For W06, reuse `TestOTelContract`'s process-owned in-memory span exporter and manual metric reader. Supply a sentinel storage error containing a synthetic sensitive marker; assert it is absent from labels, public errors and user-facing output while the bounded write outcome remains visible.

For WEB-TEST-076, tag new cases with `@tag :recovery_cache_write` and keep `async: true`/private Req ownership. Reuse `APIFixtures.run_result/0`; derive history/trace fixtures from the same modified result. In a trace fixture, update both `record` and `resources.response.payload`. Do not weaken strict decoder invariants to accommodate contradictory fixtures.

The decoder already accepts a nonempty cache-status string. Add acceptance coverage; do not add a legacy decoder or unnecessary special case. The shared trace component already consumes projection labels/title, so change component implementation only if the new projection cannot be displayed through that existing contract.

#### R03.S04 — Close the phase

Run TEST-228's fast selector and WEB-TEST-076's tagged frontend selector. Run `make test-integration` through the existing service-pool runner and confirm TEST-227 plus `TestRecoveryIntegrityCacheWriteStoredRun` actually executed. Then run `make test-fast`.

Search for retired fields/functions and inspect every remaining reference. Expected survivors are historical migration SQL, historical fixture seeding and explanatory docs. No production cache path may still construct/store them. Do not delete similarly named usage/cost fields from run statistics or the canonical result.

**Exit:** retained current rows replay without conversion or dispatch; the cache has one canonical result; write failure is observable without failing accepted inference; fast, frontend and real integration checks pass. An unavailable database leaves the phase incomplete.

### Phase R04: Certify the changed boundaries

#### R04.S01 — Audit scope and regression coverage

Use this closure map; it prevents a smaller implementation from fixing the happy path while omitting another consumer.

| Findings | Required evidence |
| --- | --- |
| RCA-001/002 | TEST-223, including model and Jina request-count cases |
| RCA-017 | TEST-225 D01–D06, including actual parent cancellation and search memo retention |
| RCA-005/010/016 | TEST-226 and retained checked-arithmetic/completion tests |
| RCA-003/004/006 | TEST-224, including exact numbers, schema failure and profile aliases |
| RCA-007 | TEST-227 and removal of redundant production construction/storage |
| RCA-011 | TEST-228 fast plus stored-run integration; WEB-TEST-076; trace and OTel assertions |

Confirm fixed-target repair, total-attempt budgeting, explicit false/zero/empty settings and host-owned state remain covered by the predecessor tests. Do not add tests or guards for the withdrawn internal-misuse scenarios.

Run `gofmt` on changed Go files and `mix format` on changed Elixir files. Review OpenAPI/schema versions, public cache contract notes and `git diff HEAD --check`. Account for any new untracked files explicitly.

#### R04.S02 — Run the final gate

Run `make test-release` with the pinned PATH. This is the cross-system certification gate for the implementation, not a production deployment. The existing manifest selects browser-free backend/frontend/integration/race tasks and `backend-verify-baseline`, which runs `make verify`.

Inspect the runner report, not only the shell exit code. Confirm nonzero selected tasks/tests, successful integration service setup/cleanup, and no skipped mandatory cases. Record the actual task count; do not hard-code an old count as proof. Do not run `make verify` again solely because it was already included in the successful release run.

If a gate fails, preserve its output, diagnose and fix the cause at the owning boundary, run the focused regression, then rerun affected gates. Do not retry an unexplained failure, raise timeouts to conceal a regression, weaken assertions or substitute a fake for a failed real boundary.

#### R04.S03 — Finish the handoff

Record the tested implementation SHA/worktree identity, per-phase completed steps, failing/passing regression names, migration version, request/write counts, exact-number/retained-row evidence and final runner report location. If only documentation changes after tests, distinguish that documentation checkpoint from the tested implementation rather than claiming a new application run.

Mark the plan implemented only when all required work and gates are complete. List any unfinished step explicitly. No deployment, browser layout or live-provider result follows from these local checks; the separately authorized production delivery is recorded in the execution evidence below.


## 7. Verification matrix and commands

Run commands from the repository root, except the explicit frontend subshell. These are implementation checks; editing this plan alone needs only Markdown/reference/whitespace checks. Proposed names below become committed cases, not substitutes for the broader gates.

Expose the repository-pinned toolchain before frontend/fast/release commands:

```bash
export PATH=/home/kirill/.local/elixir-1.20.2/bin:/home/kirill/.local/otp-28.4.3/bin:$PATH
```

| Proposed ID | Tier, owner and required invariant | Command |
| --- | --- | --- |
| `TEST-223` | T0/T1 provider transport precedence, terminal security/size failures and preserved transient behavior | `go test ./internal/providers ./internal/runtime -run '^TestRecoveryIntegrityTransport' -count=1 -timeout=60s -v` |
| `TEST-225` | T1 original parent versus attempt/search timeout, shared budget and dispatch/memo facts | `go test ./internal/providers ./internal/runtime -run '^TestRecoveryIntegrityTimeout' -count=1 -timeout=60s -v` |
| `TEST-226` | T0/T1 unknown-work coverage, checked arithmetic, failed-response facts and separate result ledger | `go test ./internal/providers ./internal/runtime ./internal/accounting -run '^TestRecoveryIntegrityAccounting' -count=1 -timeout=60s -v` |
| `TEST-224` | T0/T1 lossless cache decoding, shared admission, bounded producer validation and no dispatch on hits/errors | `go test . ./internal/runtime ./internal/cachekey -run '^TestRecoveryIntegrityCacheAdmission' -count=1 -timeout=60s -v` |
| `TEST-227` | T3 actual forward migration, retained result values, owner isolation and current store read/write | `make test-integration`; inspect the report for executed `TestRecoveryIntegrityStorage*` cases and retained `TestRecoveryMigration*` cases. |
| `TEST-228`, cheap cases | T0/T1 successful inference plus failed cache write across library/API shape/domain traces/telemetry; one model call and one write | `go test . ./internal/runtime ./internal/gateway ./internal/traces -run '^TestRecoveryIntegrityCacheWrite' -count=1 -timeout=60s -v` |
| `TEST-228`, persistence case | T3 real RunService/store/history/trace boundary with local provider | The same R03 `make test-integration` run must execute `TestRecoveryIntegrityCacheWriteStoredRun`. Default-tag gateway tests do not select it. |
| `WEB-TEST-076` | T1 `write_failed` accepted by strict API decoding and rendered correctly in live results/history/traces | `(cd frontend && mix test --only recovery_cache_write --seed 104729)` |

The existing tier runner owns integration services and shared-host scheduling. A standalone `go test -tags=integration` is not a provisioned command: `PostgresLease` expects the runner's service-pool configuration. Use `make test-integration` for the required R03 evidence; do not add another runner, point tests at an application database or hide a service boundary behind a fake. One successful R03 integration run can satisfy both TEST-227 and the stored-run part of TEST-228.

Use the following checkpoint order:

| Phase | Focused selection before broad checkpoint | Required checkpoint |
| --- | --- | --- |
| R01 | TEST-223, TEST-225, TEST-226 and retained transport/completion/repair/accounting cases | `make test-fast` |
| R02 | TEST-224 and retained replay/schema/hash cases | `make test-fast` |
| R03 | Cheap TEST-228, WEB-TEST-076, then TEST-227 and stored-run TEST-228 through the service runner | `make test-integration`, then `make test-fast` |
| R04 | No automatic repeat of already passing selectors without a new cause | `make test-release`, then `git diff HEAD --check` |

**How to evaluate a run:**

1. Inspect selected case names, not only `ok` or exit code. A Go package can report success with no matching tests. At least one relevant package must execute each proposed selector, and every case in its Section 6 matrix must be represented. A package listed for convenience may have no matching case; that does not invalidate cases actually selected in another package.
2. Confirm the tagged frontend run executed the new cases. Add `@tag :recovery_cache_write` to each relevant case; keep them in the normal unfiltered Phoenix suite as well.
3. For behavioral RED, record the failing assertion and actual value. Compilation, missing configuration/service and zero selection are not evidence of the defect. Fix those setup problems before evaluating the regression.
4. After implementation, use the same oracle and record GREEN. Retained controls can remain green throughout. Do not change assertions to make a failure disappear, retry an ambiguous run or suppress a required case.
5. Read the integration/release task reports and retain their paths in the execution log. Verify the required tests ran and service cleanup completed. Release already includes `backend-verify-baseline`/`make verify`; do not repeat it without a new reason.
6. Include untracked new source/test/migration files in review. `git diff HEAD --check` alone does not inspect an untracked file; check those explicitly before declaring the checkpoint clean.

Deleting a retired field assertion is valid only with explicit contract retirement; preserve its independent output, security and accounting assertions. No DOM emulator, browser or paid-provider call is required by this plan.

## 8. Migration and operational boundaries

The migration changes the cache table and coordinated code; it does not change operation hashes, accepted result JSON or historical run output. Verify old current-projection rows through the new single reader. Removal of cache sidecars is a package contract change; document it rather than retaining dead fields.

An older binary that queries dropped columns cannot run against the migrated schema. Record this compatibility boundary with the migration. Prefer a forward correction if implementation fails; do not prescribe restoring the whole application database or discarding newer runs to undo redundant cache-column removal. Deployment and any operational rollback require their own evaluated checkpoint and are outside this implementation plan.

Cache data may remain sensitive because generated output and search evidence are retained intentionally. Existing redaction and owner-isolation requirements still apply. The canonical projection is not a substitute for a broader retention policy.

No provider fallback/escalation, exactly-once guarantee, new timeout default, repair truncation, arbitrary internal-executor support, cross-session state protocol, or historical-accounting rewrite is included. Production delivery is recorded separately in the execution evidence below.

## 9. Definition of done and execution evidence

- Reproduced accounting and nested-timeout defects have committed passing regressions through their production owners.
- All in-scope RCA rows in Section 3 are implemented and verified; documenting a known defect in an ADR is not completion.
- One owner remains for each rule in Section 1. Cache hits and accepted provider output share applicable admission, and protocol completion remains provider-owned.
- Known amounts and uncertainty both survive retries; result accounting remains specific to delivered output.
- Cache decoding preserves JSON values; valid current entries survive column removal; cache errors cannot silently trigger provider work.
- Cache-write failure preserves accepted inference and is represented consistently in API, UI, traces and bounded telemetry.
- Every phase ends green. Final certification includes actual database integration and the browser-free release gate; deployment/browser/live-provider evidence is reported separately.

The following execution log is the completion record for this revision. Phase R04
was executed from the committed recovery source, the security-patched release was
executed from its committed patch source, and the production images were built
from the merged `main` application commit. The documentation and status record
then moved to the current `main` checkpoint below. These fields keep each tested,
deployed, and documented identity explicit; no result depends on a dirty worktree
or untracked implementation files.

```yaml
plan: PLAN-HLLM-RECOVERY-BOUNDARIES-002
version: 2.2.0
status: complete
tested_source_sha: a34e797449d7f6d9fee27485b0855070cce80a47
tested_branch: feat/recovery-policy
recovery_application_sha: 9b4a400bc8b47776134b2d181bb9b55e6292d852
security_patch_sha: c52c438b887bec6f3be06bf13a867cda06b32f11
application_source_sha: 60b74f7224ab8633acf8bb4ea7670307a1c15e18
documentation_checkpoint_sha: b71374d4ef4b8c3ad11220403c5a3e9fdf86da1e
branch: main
source_identity:
  phase_release_commit_sha: a34e797449d7f6d9fee27485b0855070cce80a47
  phase_release_branch: feat/recovery-policy
  security_release_commit_sha: c52c438b887bec6f3be06bf13a867cda06b32f11
  recovery_merge_commit_sha: 9b4a400bc8b47776134b2d181bb9b55e6292d852
  application_merge_commit_sha: 60b74f7224ab8633acf8bb4ea7670307a1c15e18
  documentation_checkpoint_sha: b71374d4ef4b8c3ad11220403c5a3e9fdf86da1e
  working_tree: clean
  untracked_files: []
completed_phases:
  - phase: R01
    status: complete
    completed_steps: [R01.S01, R01.S02, R01.S03, R01.S04, R01.S05, R01.S06]
    regressions:
      - TestRecoveryIntegrityTransport
      - TestRecoveryIntegrityTimeout
      - TestRecoveryIntegrityAccounting
      - TestRecoveryIntegrityAccountingProviderResponses
      - parent-cancellation-during-model-body-read case
    checkpoint: make test-fast (accepted=true)
  - phase: R02
    status: complete
    completed_steps: [R02.S01, R02.S02, R02.S03]
    regressions:
      - TestRecoveryIntegrityCacheAdmission
      - TestRecoveryIntegritySearchAdmission
      - exact-number, text-number, structured-zero/false, whitespace, trailing-JSON, producer-alias and cache-read-error cases
    checkpoint: make test-fast (accepted=true)
  - phase: R03
    status: complete
    completed_steps: [R03.S01, R03.S02, R03.S03, R03.S04]
    regressions:
      - TestRecoveryIntegrityStorage
      - TestRecoveryIntegrityCacheWrite
      - TestRecoveryIntegrityCacheWriteStoredRun
      - TestRecoveryIntegrityCacheWriteProjection
      - TestRecoveryIntegrityCacheWriteTelemetry
      - WEB-TEST-076 tagged projection and component cases
    migration_version: 7
    integration_report: tmp/test-feedback/recovery-r03-integration-report.json
    checkpoint: make test-integration and make test-fast (accepted=true)
  - phase: R04
    status: complete
    completed_steps: [R04.S01, R04.S02, R04.S03]
    release_command: PATH=/home/kirill/.local/elixir-1.20.2/bin:/home/kirill/.local/otp-28.4.3/bin:$PATH node scripts/run-test-tier.mjs --task release --output tmp/test-feedback/recovery-r04-final-a34e797.json
    release_result: {accepted: true, selector: release, task_count: 24, failure: null, cleanup_errors: []}
    release_report: tmp/test-feedback/recovery-r04-final-a34e797.json
    release_report_sha256: 8a516e6a625db38465e8ef6d06aff976f02a57ac3f51fb836ee5551762469402
    release_report_result: schema=1, seed=104729, all 24 task statuses 0, integration service pools started and cleaned, firstFailure=null
red_evidence:
  - accounting overlay reproduced unknown dispatched work being reported complete
  - nested Jina/attempt timeout reproduced as terminal timeout while the parent remained active
green_evidence:
  - known subtotals and uncertainty aggregate independently across retries, including measured zero and invalid-component precedence
  - parent cancellation/deadline, local timeout, endpoint policy and response-size precedence are preserved with request-count assertions
  - cache replay preserves exact JSON numbers and rejects malformed, trailing, schema-invalid, search-invalid and producer-mismatched projections without provider work
  - migration 0007 preserves owner/key/version/timestamps/result JSON and leaves only the canonical result columns
  - accepted inference remains successful after cache-write failure; API, history, traces, telemetry and Phoenix projections retain write_failed
request_and_write_counts:
  stored_run_provider_requests: 1
  stored_run_cache_writes: 1
  cheap_cache_write_cases: one provider execution and one attempted write per cache/refresh case
  cache_hit_cases: zero provider/search dispatch and zero cache write
retained_row_and_number_precision_evidence:
  - cache admission replays integer 9007199254740993 without float conversion
  - cache admission replays long fractional JSON numbers and text 0012 exactly
  - storage migration compares retained JSONB/result values exactly for rows owned by two users
issues_and_resolutions:
  - release smoke setup and service-pool ownership were executed by the canonical tier runner; no standalone integration database was used
  - the retained release report records every task status as 0, both integration service-pool tasks with cleanupError=null, and no first failure
  - deterministic frontend fold assertions now wait for the component-owned state save before opening a dependent fold; this preserves the real async lifecycle and removed a scheduler-dependent release failure
  - the first production replacement used a shell-sourced JSON environment and was rejected when the gateway failed its encryption-key configuration check; the accepted deployment used Compose's native --env-file parser, with no volume or data removal
deployment:
  production_url: https://harden-llm.prls.co/
  compose_project: harden-llm
  source_sha: 60b74f7224ab8633acf8bb4ea7670307a1c15e18
  deployed_at: 2026-09-16T05:36:16Z
  migration_version: 7
  gateway_image: sha256:7d75ff7b3e923301183a44a61ae19d750db9b7ae87ea53eafe0eda91f4775844
  gateway_container: 6d8f00b01e824e24ba71f3dfcde0505753c7471c7f89167ee8ab03530f888fe1
  web_image: sha256:cbb385010d370fb70a2efaa0acaa07deccb350b8e58ec55a9b972dbf2709ddb6
  web_container: 9c51e7f589e9a3b722c14a46e601b0d0610001d4cb3887506b5b63dd68fce8fe
  health: {gateway: healthy, web: healthy, api_healthz: 200, api_readyz: 200, frontend_healthz: 200}
  authenticated_read_only: {static_token_profiles: 200, static_token_history: 200, anonymous_history: 401}
  browser_layout_checked: false
  live_provider_called: false
  receipt: /home/kirill/.local/state/harden-llm-recovery-60b74f7/post-deploy.json
security_patch:
  advisory: GHSA-2v4p-qf9q-27wj
  dependency: google.golang.org/grpc
  fixed_version: 1.83.2
  pull_request: 48
  release_report: tmp/test-feedback/grpc-security-release-c52c438.json
  release_report_sha256: 93a2f4ff308c56789a4e2f79753cc2fd93e133c5689f7baa22ed5aee7c26590f
  dependabot_open_alerts_after_deploy: 0
remaining_work:
  - browser layout/native-event and live-provider certification were not run because AGENTS.md requires explicit user authorization for those gates
```
