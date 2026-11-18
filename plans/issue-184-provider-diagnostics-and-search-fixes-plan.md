# Provider diagnostics and verified search fixes for issue 184

## 1. Objective and status

[PR #185](https://github.com/prls-co/harden-llm/pull/185) targets `dev`.
Its first implementation, `9c401a6`, exposed three defects during review.
The owner approved **1A** for native Responses answer selection and **2A**
for preset report qualification. Those changes and the attempt attribution
correction below are pending on the PR.

Make provider failures explainable from retained evidence, then apply the small
fixes demonstrated by the issue 156/184 qualification work. Reuse the existing
provider router, structured logger, diagnostic artifacts, preset matrix and
test fixtures. Keep one execution engine and the existing ownership boundaries.

The user accepted a small implementation that preserves existing diagnostics,
fills the actual capture gaps, and persists through existing artifacts. This
plan does not authorize another paid campaign, browser testing, or production
promotion by itself.

## 2. Evidence and limits

The local continued RCA at
`plans/evidence/harden-llm/issue-184/2026-11-16/continued/README.md` records 34
additional experiments, including 27 provider HTTP requests and 19 Jina
requests. The first RCA at
`plans/evidence/harden-llm/issue-184/2026-11-16/README.md` retains the original
CPA model errors and Haiku control. These evidence paths are locally retained
and ignored by Git; this tracked plan does not publish private provider bodies
or credentials.

- Five Jina text cases reproduced HTTP 200 followed by `RESPONSE_TOO_LARGE`
  before model dispatch. Snippet controls passed the search boundary across
  seven tested profile/mode cases.
- GPT-OSS text failed on Bedrock with no final content and on Groq with an
  unavailable-tool-call error. A single completed-retrieval status prefix
  produced A/B/A/B failure/success reversals on both routes, with all other
  decoded request fields unchanged and retained search evidence held constant.
- Haiku returned fenced JSON through CPA Responses. Native Messages through
  the same CPA host, model and credentials passed the original schema.
- Both original GPT-5.5 errors conclusively identify an unavailable model.
- Six fresh native Gemini calls succeeded. Of three streaming controls, one
  timed out at 30.1 seconds. Successful longer-header observations finished
  before 30 seconds; these do not justify changing production timeout policy.
- The original Gemini and GPT-OSS structured causes remain unidentified because
  their detailed errors were not retained. New results do not rewrite them.

The cumulative RCA cost accounting is $0.453134487 of the authorized $1,
including $0.27 of held unknown-call reservations. Equivalents are not invoices.
No additional paid work is needed to implement the deterministic changes below.

The PR review exercised `Service.Run`, loopback HTTP servers, the persisted
support bundle and the matrix reporter. A search timeout on attempt 1 appeared
on attempt 2's successful model operation. A fallback row qualified with only
one of its two operation records. Both JSON and streamed Responses could
return explicit commentary as the final answer. Under the race detector, 4 of
30 review cases passed at `9c401a6` and all 30 passed on a temporary prototype.
The prototype retained legacy ordering for unlabeled replies. Decision 1A
adds a stricter ordering case that still needs its own regression.

## 3. Design and reuse boundaries

Follow ADR-HLLM-015, 019, 021, 024 and 033/034. Extend the existing
[transport diagnostics work](transport-admission-diagnostics-plan.md) from its
CPA event coverage to the shared provider and search boundaries.

| Owner | Existing code to reuse | Small change |
| --- | --- | --- |
| Provider effects | `internal/providers/router.go`, `payload.go`, `transport_diagnostics.go`, injected `Config.Logger` | Retain finite observations before normalization; cover every protocol through one shared rendering path; preserve non-empty `connection.Provider` across `anthropic-messages` and `gemini-generate-content` |
| Search effect | `internal/providers/web_search.go`, hardened HTTP client and `Searcher` boundary | Use the same diagnostic vocabulary; retain search HTTP status, size, dispatch and deadline facts separately from model facts; emit search records on actual search execution rather than `SearchMemo` reuse |
| Reference composition | `stateful/client.go`, `run.go`, `artifact.go` | Join operation records to the existing run via a request-scoped context capture sink and include bounded redacted logs and `JinaAPIKey` scrubbing in the existing support bundle |
| Redaction and artifacts | `internal/redaction`, `internal/diagnostics`, `internal/traces` | Reuse redaction, `Input.Logs`, `Input.Secrets`, artifact kinds, bounds and publication failure reporting |
| Qualification | `preset_live_test.go`, `preset_matrix_helpers_test.go` | Persist existing attempts and the shared private diagnostics instead of retaining only the public error projection |

Use `slog` and the existing injected logger. The router already receives a
logger; add the missing operation observations there rather than introducing
a provider observer interface or telemetry SDK. Keep the hardened HTTP clients
shared and reusable. Keep `CPAAdmission.Logger` focused on admission/permit
events so existing single-record admission logger assertions remain intact when
`Client.Logger` is unset.

For reference artifacts, use a bounded, request-owned context capture of those
same structured records after redaction and pass it through the existing bundle
`Logs` field. Include both resolved provider credentials/headers and `JinaAPIKey`
in the existing `diagnostics.Build` secret-scrubbing boundary. Keep the context
collector private and scoped to that call; use the existing logger/redactor and
bundle bounds. No process-global capture registry, second log format, new
storage service or diagnostic database is needed. Concurrent calls must never
share captured records. Keep `out.Attempts[i].Stream == nil` on the public wire
when no response stream/body was observed (`no_headers`).

CPA admission remains the owner of permits, queueing and settlement. Its
existing event keeps those facts. Provider/search records describe external
operations and share correlation and fact extraction with admission; do not
create an independent transport classifier or alter admission behavior.

The stateless core continues to own execution through injected effects. It
gains no concrete logger, storage, provider construction or telemetry SDK.

### 3.1. Minimum retained facts

Reuse existing attempt fields before adding any new field. Keep new
effect-local facts out of public result DTOs and strict trace/cache schemas.

| Facts | Purpose |
| --- | --- |
| Operation stage, model/protocol, typed code/category, observed HTTP status | Distinguish search, transport, parsing, schema, accounting and upstream rejection |
| Header-write observation, headers received, bytes/events/output counts, terminal state | Distinguish never dispatched, waiting for headers, partial reply and completed reply |
| Existing finite transport class/phase, effective limits and context expiry | Distinguish local observations of header/search/call deadlines without inferring remote completion |
| Bounded actual backend and provider request/correlation ID when available | Identify OpenRouter routing and join provider-side evidence |
| Finite finish reason, final-text presence/size, reasoning presence and tool-call count | Explain an empty answer or unsupported tool attempt without saving reasoning or answer text in logs |
| Run/call/trace identity, source revision and input/schema/options hashes | Reproduce the exact selected configuration and join its existing trace |
| Capture/publication outcome, clipping or missing required facts | Distinguish missing evidence from an observed negative fact |

For Jina, preserve an observed HTTP 200 and the exceeded byte limit even when
the public typed error's status is zero. Distinguish Jina dispatch from model
dispatch, and emit the search operation record only on actual `Searcher.Search`
execution (not `call.SearchMemo` reuse). Anchor the model request timer in
`Router.Execute` immediately before `router.client.Do(request)` (after fallback
search completes) so `Stream.HeadersMs` and model transport latency do not
include Jina search duration.

Use reviewed finite values and bounded identifiers. Never log raw `Error()`,
provider messages, response bodies, reasoning text, query text, headers or
credentials. Existing redacted parse-failure artifacts remain the protected
mechanism for necessary response evidence. Unavailable facts stay unknown.

## 4. Implementation sequence

### P01. Add deterministic regressions before changing behavior

Extend the existing T0/T1 provider, admission, artifact and matrix tests. Use
table-driven local HTTP/TLS fixtures and synthetic credentials. Cover the
failures and acceptance oracles in section 5 before implementation. Register
new canonical `TEST-###` entries in
`from_utility-llm/harden-llm-self-hosted-test-spec.md` and
`../test/test-tiers.json`; extend TEST-025/455/542/548 where their existing
assertion owns the behavior. For this review, extend TEST-548/549 in place.
Update the eight affected subtests in
`TestPresetMatrixCauseRetentionAndCaptureStatus`. Seven need complete
operation fixtures for their claimed route. One must assert that missing
diagnostics fails qualification while preserving a successful call. Retain
their typed-error and public-projection assertions. Use the existing runner
and fixtures.

### P02. Complete capture and persistence through the existing paths

1. Reuse finite transport classification at the point raw errors are available.
   Emit one bounded terminal record for each actual search or model operation,
   including early failures. Do not invent provider operations for cache hits
   or `SearchMemo` reuse.
2. Retain safe upstream identity/response-shape facts before normalization
   clears output. Use one shared fact renderer for production and qualification.
3. Connect the existing injected logger, run correlation and bounded per-call
   context capture to support-bundle `Logs` and `Secrets` (including resolved
   connection credentials/headers and `JinaAPIKey`). Preserve original inference
   outcomes when diagnostic publication fails; expose the existing persistence
   diagnostic.
4. Test ordinary `/v1`, reference runs, CPA and non-CPA paths. Keep the existing
   public error, trace/cache shapes and unknown-outcome behavior unchanged.
5. Carry the attempt number supplied to `trackingObserver.StartAttempt` in the
   request context. Stamp each search/model record at emission for both the
   process log and the support bundle. Remove the positional join in
   `OperationDiagnostics.ReconcileAttempt`. A later schema or accounting error
   belongs to the numbered trace attempt. The operation record retains the
   outcome observed at the HTTP boundary.

Exit: a locally injected failure can be explained from its durable private
record without the temporary RCA overlays or an additional provider call.

### P03. Make the preset report retain that evidence

1. Retain existing attempt code/category/status, dispatch flags, timing,
   correlation and safe diagnostics instead of only `ProjectRunError`.
2. Capture the same provider/search records with the existing logger injection;
   use the existing private atomic report writer. Keep public API error codes
   as a separate summary, not the diagnostic cause.
3. **Decision 2A.** Keep the execution outcome or uncertainty in `status`.
   Write the verdict for each evaluated row in `qualificationStatus`.
   `recordLivePresetOutcome` computes it once for both the Go test and the
   private report. Record `invalid_execution`, `invalid_evidence` and
   `invalid_diagnostics` as qualification verdicts. A successful answer with
   missing model evidence yields
   `status=succeeded`, `diagnosticCaptureStatus=missing` (or `incomplete` for
   partial capture) and `qualificationStatus=invalid_diagnostics`. Only
   `qualificationStatus=passed` counts as a pass. Retain the configuration
   identity and copy `runId`, `callId` and `traceId` from the run.
4. Preserve started/unconfirmed rows, filtered unstarted rows, missing
   credentials and optional unobserved native search. Derive row counts from
   the shipped catalog. Never rerun an ambiguous case automatically.
5. For this single-attempt, cache-off matrix, validate record stage, order,
   attempt number, dispatch and required terminal facts against the route
   and outcome. A successful Jina fallback needs a successful search record
   followed by a model record. A terminal Jina failure before model execution
   needs just the failed search record. A native or search-off generation
   needs its model record. An optional native answer may report zero searches.
   Clipping, missing fields, duplicates, contradictions and publication
   failures must remain visible to the verdict.

Exit: TEST-455's normal report contains the evidence needed for the same RCA
that previously required a private probe harness. Its deterministic helper
tests exercise reporting without credentials or internet access.

### P04. Apply only the demonstrated corrections

| Change | Exact scope and acceptance |
| --- | --- |
| Native Responses final answer (decision 1A) | Use `finalResponseOutput` for returned text, citation attribution and diagnostic final-text presence/byte count in JSON and streamed Responses. Honor an explicit `final_answer`, including an empty one. Exclude `commentary`. When labels are absent and a web search completed, only assistant text after the last completed search is eligible. With only earlier text, return `empty_response`. A valid answer remains eligible when optional search never ran. Read search activity and usage from the full response. |
| Jina snippets | Set `X-Respond-With: no-content`; retain the 20-second search timeout, 512 KiB response limit, 64 KiB injected-context limit and fixed-host policy |
| Completed-retrieval context | Add the exact tested prefix from the continued RCA to the existing fallback context; preserve the user's prompt, strict schema, untrusted-material warning and source citation instructions |
| Haiku preset | Change only `CPA claude-haiku-4-5-20251001` to `anthropic-messages`, set `responsesTokensParam` to `null` and remove default `stream`; preserve non-empty `connection.Provider` in `buildPayload` so `SelectedTarget.Provider` and `Attempt.Target.Provider` retain `cpa` |
| Unsupported preset | Remove the `CPA gpt-5.5` starting preset until the upstream restores that exact ID; do not alias another model |
| Gemini pricing metadata | Correct Gemini 3.7 Flash's (and the other shipped Google Gemini presets') zero reasoning-token rate to its documented output-token rate; protect the arithmetic with a deterministic usage example |

Add the regressions before each correction. Record prompt/schema parity
explicitly: diagnostics change neither; the fallback treatment changes only
the declared trusted wrapper; the Haiku fix changes protocol/stream settings.
Do not convert reasoning to answer text, strip Markdown fences, relax schema
validation, pin an unqualified provider or increase retries/timeouts.

Keep Gemini runtime behavior unchanged. Track the unresolved historical
Gemini and GPT-OSS structured errors separately from these new qualifications.

### P05. Verify and deliver a reviewable implementation

Run focused affected tests, then `make test-fast` with the pinned toolchain.
Use focused race checks for concurrent capture and shared HTTP ownership.
Compile the live matrix with no selected tests; do not dispatch providers.
Run `git diff HEAD --check` and check diagnostic secret/cross-call isolation.

```sh
go test -race ./internal/providers ./internal/diagnostics ./stateful ./cmd/harden-llm-gateway
PATH=/home/kirill/.local/elixir-1.20.2/bin:/home/kirill/.local/otp-28.4.3/bin:$PATH make test-fast
go test -tags=live ./cmd/harden-llm-gateway -run '^$' -count=1
git diff HEAD --check
```

Update the canonical test catalog and existing ADR-HLLM-019 implementation
notes for the changed fallback/preset behavior. This preserves accepted
boundaries and needs no new ADR unless implementation introduces a real
contract departure. Reassess any such departure explicitly before expanding
the implementation.

Deliver the corrections on the existing PR. Update the
[qualification guide](../docs/preset-live-qualification.md) and known report
readers together. Document how to count passes in new reports and how to
interpret archived rows under their original format. Report local tests,
hosted checks and deployment separately. Production promotion requires
separate authorization.

## 5. Required test oracles

| Boundary | Lowest sufficient check and oracle |
| --- | --- |
| Final-answer selection | T1, identical JSON and SSE fixtures. Unlabeled text only before a completed search yields `empty_response` and zero final-text bytes. Unlabeled `ok` after the last completed search yields two bytes. Late commentary cannot replace a labeled final or supply text for an explicit empty final. Verify citations against the selected text and retain optional-search success without a search call. |
| Attempt attribution | T1, through `Service.Run` and a read-back of the support bundle. A search timeout on attempt 1, then successful search and model on attempt 2, yields operation ordinals `1, 2, 2`. A model retry using memoized search produces one search record total. A successful HTTP exchange followed by `STRUCTURED_SCHEMA` retains both facts under the same attempt number. |
| Matrix verdict | T0/T1, read back the private report. For a successful fallback, remove, duplicate, reorder or misattribute one record and assert successful inference plus failed qualification. Restore both valid records and assert `passed`. Cover a failed search before model dispatch and a dispatched header timeout with `status=unconfirmed`. Verify the persisted IDs match the run. |
| Cause retention | T0/T1: parse, schema, accounting, empty output, HTTP rejection and response-too-large errors remain distinguishable in the private report while public projection is unchanged |
| Timeout ownership | T1 TLS: header timeout while parent is live, parent expiry, cancellation and partial disconnect retain correct phase/dispatch facts, one attempt and unconfirmed remote outcome where appropriate |
| Jina before model | T1: HTTP 200 plus oversize/search deadline is retained; model dispatch remains false; header treatment accepts snippets under unchanged bounds |
| Response shape | T1: reasoning-only final output remains empty/error; final answer, finish reason and tool presence are observable without recording content |
| Shared capture | T1: CPA and non-CPA paths use the same rendering; simultaneous runs and scopes retain only their own records; success has no invented failure label |
| Artifact failure | T1: writer failure, clipping and missing capture are visible; accepted inference remains accepted, and matrix qualification reports missing evidence |
| Strict contracts | T0/T1: existing OpenAPI, trace, cache and decoder checks accept the unchanged shapes; no public transport-label fields appear |
| Haiku resolution | T0/T1: same CPA credential/admission origin; text remains optional native search, structured search selects existing Jina; strict schema survives native normalization |
| Catalog and prices | T0: remaining presets retain metadata and dynamic matrix coverage; unsupported starting entry is absent; saved-profile behavior is unchanged; reasoning costs use the qualified rate |
| Search/cache behavior | T0/T1: header/context treatment preserves original prompt/schema and evidence; cache hit invokes neither search nor model, refresh recomputes, and cached evidence is not relabeled as a fresh dispatch |

Use the existing fast/race tasks. Select T3 only if implementation changes an
actual storage boundary that existing local artifact tests cannot prove.
Browsers and a fresh live-provider campaign are not required for these
deterministic invariants. Existing live controls remain historical evidence,
not a pass for a later implementation SHA.

## 6. Compatibility and completion

- An unlabeled answer listed only before a completed native search will now
  yield `empty_response`. How often providers emit this ordering is unknown.
- New private report readers use `qualificationStatus` for pass counts.
  A reader that continues counting `status=succeeded` can overcount.
  Apply the new interpretation to new reports only.
- HTTP/OpenAPI, public Go result/attempt DTOs, strict trace/cache formats and
  client decoders retain their shapes. New private fields use the existing
  structured-log/support-bundle mechanisms, with no new dependency or service.
- Haiku's starting preset changes protocol and no longer requests streaming.
  Its structured search now needs the configured Jina credential; absence is
  reported through the existing configuration path. Ordinary callers explicitly
  selecting CPA Responses keep that protocol and its upstream limitations.
- GPT-5.5 disappears from the starting catalog. Existing saved immutable
  setups, drafts, history and artifacts are not rewritten or deleted.
- Jina's model-visible reference content and fallback wrapper intentionally
  change. Existing generic cache/refresh/off semantics remain authoritative;
  no search-specific TTL, automatic invalidation or data reset is introduced.
- Pricing corrections affect new configured-cost estimates, not historical
  invoices or saved immutable profile values.

Completion requires the shared diagnostics to be retained by the normal
qualification path, the focused regression oracles and fast gate to pass,
and the verified fixes to be reviewable on `dev`. It does not require a claim
of 100 percent provider reliability, a guessed historical cause, or an
all-provider pass while credentials remain unconfigured.
