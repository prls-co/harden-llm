# Request status lookup implementation plan

- Document: `PLAN-HLLM-REQUEST-STATUS-001`, version 1.0.
- Date: 2026-10-18.
- Status: ready for implementation; no runtime behavior or deployment is established by this plan.
- Tracking: [issue #136](https://github.com/prls-co/harden-llm/issues/136).
- Related: [CPA execution issue #134](https://github.com/prls-co/harden-llm/issues/134) and [broader architecture](cpa-durable-execution-architecture.md).
- Inspected source baseline: `f1ace14f9b0252ce8a49f7def5b776fbf524864a` (`origin/main` when planned); design branch was `e05ce9ee90c96bb5757948061950a45368e59def` before this document.
- Canonical contract: `api/openapi.yaml`.
- Canonical test catalog: `plans/from_utility-llm/harden-llm-self-hosted-test-spec.md`, `SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001`.

## 1. Deliverable and scope boundary

Add an optional `X-HLLM-Tracking-ID` header to ordinary generation requests and
an authenticated `GET /v1/requests/{trackingID}` lookup. The caller supplies the
UUID before submission, so another connection can poll while the original POST
is waiting or running. The POST keeps its existing OpenAI-compatible body,
JSON/SSE/error response, hardening policy and cancellation behavior.

Use the existing stateful PostgreSQL pool and one metadata table so lookup works
across gateway replicas and restarts. Retain only bounded execution facts. The
root Go core stays stateless; the stateful wrapper owns tracking persistence;
the gateway owns HTTP validation and lifecycle integration.

This delivery does not add background execution, response/result retrieval,
request replay, an inference retry policy, cancellation endpoints, list/search
endpoints, batch SDK compatibility, a new Temporal workflow, a scheduler,
another database, Garage objects or Phoenix UI. It neither implements nor proves
the broader CPA admission design. Existing `/v1/jobs` work remains separate.
Do not change upstream CPA settings or run real provider calls for this feature.

The earlier architecture remains a separate proposal. For this implementation,
do not implement its independent attempt workflows, permit ledger, longer
generation deadlines, shared admission adapter or V1/V2 migration. The agreed
first delivery is status lookup only.

## 2. Read these source locations first

| Existing file | Relevant responsibility / fact |
| --- | --- |
| `internal/gateway/httpapi/httpapi.go` | `Config`, `API`, `router`, existing writer wrapper; current ordinary maximum is 60 seconds |
| `internal/gateway/httpapi/reference_access.go` | `authorize` matches one configured key before deriving scope; `scopeFromContext` exposes trusted hash/client |
| `internal/gateway/httpapi/handlers.go` | Both ordinary generation handlers, `execute`, timeout resolution, response construction and error mapping |
| `internal/gateway/httpapi/requests.go` | Strict ordinary request parsing; background execution remains unsupported |
| `internal/gateway/httpapi/stream.go` | Existing final-only Chat/Responses SSE; preserve event order, body and flush semantics |
| `types.go` | Existing `Request.Progress`, `ProgressEvent`, `Result` and diagnostics |
| `client.go` | Nonblocking, caller-owned progress channel; the core never closes it |
| `internal/runtime/observer.go` | Existing `Observer.StartRetry` supplies classification and the actual planned wait before sleeping |
| `internal/runtime/execute.go` | Real retry wait and cumulative progress generation; do not infer a future wait from a past 429 |
| `stateful/client.go` | `newEffects` composes concrete provider effects and the existing telemetry observer |
| `stateful/service.go` | One shared `Service`, injected pool and clock; `ClientForKey` |
| `stateful/store.go` | `ApplySchema`, 30-second DDL budget, 600-millisecond request transaction budget, 500-millisecond statement budget |
| `stateful/schema.sql` | Reference schema version 1; existing tables must survive the additive change |
| `cmd/harden-llm-gateway/server.go` | Existing shared service/pool, shutdown owner and 75-second HTTP write timeout |
| `cmd/harden-llm-gateway/reference_config.go` | Existing explicit `schema` command and shared database configuration |
| `internal/gateway/httpapi/openai_contract_test.go` | Real official Go SDK + synthetic TLS upstream; retries disabled in fixture |
| `internal/gateway/httpapi/openapi_contract_test.go` | Exact route allowlist that must gain only the new lookup route |
| `stateful/storage_integration_test.go` | Leased PostgreSQL, schema preservation and constraint assertions |
| `stateful/schema_budget_test.go` | Cheap regression for separate DDL/request acquisition budgets |
| `test/test-tiers.json` | Existing unit/API/integration/race task registration |

Some files differ on the unmerged CPA branch. Resolve symbols using `rg` on the
chosen implementation base. Do not copy its batch implementation or unrelated
dirty files to obtain a tracking feature. New file names below are proposals,
not claims that those files already exist.

## 3. Freeze the HTTP contract before coding

### 3.1. Submission header

Support the header only on `POST /v1/responses` and
`POST /v1/chat/completions`. Its absence preserves the existing path, including
zero tracking SQL, progress collector or tracking goroutine. Do not generate an
implicit ID for untracked requests.

Accept exactly one nonempty, canonical lowercase, hyphenated UUID string with
36 characters. Parse with the existing UUID dependency and compare its canonical
string; reject nil UUID, lists, multiple values, URN/compact forms and malformed
values. Do not restrict callers to one UUID version. HTTP header names remain
case insensitive. Return an ordinary OpenAI-shaped `400` with
`code=invalid_tracking_id`, `param=X-HLLM-Tracking-ID` for invalid input.

Authenticate first. Perform the handler's existing request validation before
creating a record. Validation failures before registration create no record.
Core validation failures after registration finish that record as failed.

Atomically reserve `(api_key_hash, tracking_id)` before invoking the core.
Duplicate IDs under the same key, across both POST routes and all replicas,
return OpenAI-shaped `409`, `code=tracking_id_conflict`, with no new execution.
Same UUID under another configured key is independent. Reuse becomes possible
only after the old metadata retention expires; callers should always use fresh
UUIDs. The ID is correlation, not a key, request-body fingerprint or replay token.

The normal POST continues returning its original completion/response. Never
substitute a job object, `202`, NDJSON or custom SSE progress. Do not forward the
tracking header upstream, add it to cache identity, or alter provider input.

### 3.2. Lookup route and errors

Register `GET /v1/requests/{trackingID}` behind the same `authorize` wrapper.
Validate the path UUID by the same rules. Support no query parameters in v1.
Existing scope-selector rejection still runs before route handling.

| Condition | Response |
| --- | --- |
| Known unexpired ID under matching key | `200`, `RequestStatus` below |
| Missing/invalid bearer | Existing OpenAI-shaped `401` |
| Malformed ID / unsupported query | OpenAI-shaped `400` |
| Unknown, expired or other-key ID | Identical OpenAI-shaped `404`, `code=request_not_found` |
| Storage missing/unavailable | OpenAI-shaped `503`, `code=request_status_unavailable` |

All lookup responses, including errors, use `Cache-Control: no-store`. Set
`Retry-After: 2` on nonterminal successful observations as polling guidance; it
does not claim that provider capacity returns in two seconds. No redirect,
signed URL, prompt, output or artifact download is returned. Polling never
dispatches inference or updates a heartbeat.

A first lookup may race authentication/body validation/registration and return
404. Document this explicitly. A caller can briefly retry GET while its original
POST is still outstanding. GET cannot prove that a timed-out submission was
never dispatched; it must not cause an automatic POST resubmission.

### 3.3. `RequestStatus` schema

Use snake_case fields and explicit nullable unknown values. Required fields:

| Field | Type / meaning |
| --- | --- |
| `id` | Caller UUID |
| `endpoint` | One of the two ordinary POST paths |
| `status` | `running`, `waiting`, `completed`, `failed`, `cancelled`, `unconfirmed` |
| `stage` | Nullable allowlisted execution phase; never a raw provider string |
| `reason` | Nullable allowlisted waiting/uncertainty reason |
| `created_at`, `deadline_at`, `retention_expires_at`, `observed_at` | Server UTC RFC3339 timestamps |
| `updated_at`, `last_worker_heartbeat_at` | Last committed execution-metadata update / liveness |
| `last_progress_at` | Nullable last actual phase/attempt/byte change |
| `finished_at` | Nullable time the owning execution finalized its observation; not proof that an unobservable upstream stopped |
| `next_attempt_at` | Nullable known retry/admission eligibility timestamp |
| `elapsed_ms` | Nonnegative logical-call elapsed time; frozen at recorded finish |
| `retry_after_ms` | Nullable nonnegative remaining known wait, recomputed on GET |
| `attempts_used` | Known local logical attempt count; zero initially |
| `observation_stale` | True when a nonterminal heartbeat is older than 15 seconds |
| `outcome_confirmed` | Nullable boolean: null while pending, true for a known final outcome, false for uncertain execution |
| `error_code` | Nullable fixed safe code; no raw error text |

Initial record: `status=running`, `stage=preparing`, no next attempt and no
progress timestamp. Valid stages are `preparing`, `provider_attempt`,
`validation`, `retry_wait`, `response_projection`, plus null when unknown.
Valid reasons are `provider_throttled`, `retry_backoff`, `execution_unobserved`,
plus null. Add other reasons only with a real source event and a contract/test
update. In particular, do not report `queued`, capacity wait, queue position,
percentage complete, ETA or token limits without an implemented source.

Example while an actual retry wait is scheduled:

```json
{
  "id": "a60f21da-604f-4fe3-a039-23382ec6b313",
  "endpoint": "/v1/responses",
  "status": "waiting",
  "stage": "retry_wait",
  "reason": "provider_throttled",
  "created_at": "2026-10-18T12:00:00Z",
  "deadline_at": "2026-10-18T12:01:00Z",
  "retention_expires_at": "2026-10-19T12:01:00Z",
  "observed_at": "2026-10-18T12:00:42Z",
  "updated_at": "2026-10-18T12:00:40Z",
  "last_worker_heartbeat_at": "2026-10-18T12:00:40Z",
  "last_progress_at": "2026-10-18T12:00:30Z",
  "finished_at": null,
  "next_attempt_at": "2026-10-18T12:00:55Z",
  "elapsed_ms": 42000,
  "retry_after_ms": 13000,
  "attempts_used": 1,
  "observation_stale": false,
  "outcome_confirmed": null,
  "error_code": null
}
```

Pending work has no final outcome, so `outcome_confirmed` is null. A known
terminal failure can have `outcome_confirmed=true` without a successful answer.
Clear the retry timestamp/reason when leaving that wait. Once a known wait
timestamp has passed without a newer event, report remaining wait zero, not a
new wait.

## 4. Data model, storage behavior and retention

Add `stateful/request_tracking_schema.sql` and embed it from the existing
schema lifecycle. Create one additive table `request_tracking`, with typed
columns rather than a request/result JSON blob:

```text
api_key_hash text, tracking_id uuid                 composite primary key
owner_id uuid                                     server-generated invocation nonce
endpoint text                                     checked two-route enum
status text, stage text?, reason text?             checked contract enums
created_at timestamptz, deadline_at timestamptz
retention_expires_at timestamptz
updated_at timestamptz, heartbeat_at timestamptz
last_progress_at timestamptz?, finished_at timestamptz?
next_attempt_at timestamptz?, error_code text?
attempts_used integer, revision bigint
outcome_confirmed boolean?
```

Add nonnegative counts, positive revision and timestamp-order constraints.
Use the existing 64-lowercase-hex hash check. Index
`(api_key_hash, retention_expires_at, tracking_id)` for bounded cleanup. Do not
store provider credentials, API keys, prompts, schemas, messages, tool arguments,
generated text, prepared operations, arbitrary error bodies or full progress
events. Do not insert reference history or write Garage artifacts.

Keep one invocation nonce per accepted tracking record. Every mutation is
conditional on scope, tracking ID, nonce, increasing revision, unexpired
retention and `finished_at IS NULL`. Finalization updates the terminal fields
atomically. Duplicate/older snapshots cannot revive a terminal row or overwrite
another invocation after expiration/reuse. A repeated finish with the identical
nonce/revision is metadata-only readback, never execution permission.

Use the existing short SQL transaction helper; no transaction spans provider
I/O. Start must receive a positive insert/COMMIT acknowledgement, or a positive
readback of the exact nonce reserved by this invocation, before calling the core.
An unknown COMMIT plus missing/failed readback returns `503` and dispatches
nothing. Never retry the insert as replacement work. A concurrent duplicate
gets `409`; other SQL faults get `503`, not a false conflict.

Once execution has started, tracking write failure must not fail/retry inference,
replace its successful result, or change its response body. Keep the newest
bounded local snapshot for the next regular flush. If final metadata cannot be
committed within one bounded cleanup budget, log a safe failure and stop. GET
returns `503` during a database outage and may later show stale/unknown metadata;
do not invent a final result from a heartbeat or an HTTP log.

Retention is an explicit metadata policy: set `retention_expires_at` to the
recorded execution deadline plus 24 hours at insertion. This is separate from
the deadline and never extends execution. GET treats expired rows as absent.
Before a new registration, clean at most 100 expired rows in that same key
scope, including the requested ID first if expired. Use a bounded, indexed
DELETE. An authenticated lookup may do the same bounded cleanup if desired,
but must not update live rows. Idle-key expired rows may remain physically
present until subsequent scoped cleanup; do not promise exact-time erasure.
This is deliberate tracking-metadata retention, not permission to delete jobs,
history, results or any other product data. No new cleanup daemon is needed.

## 5. Execution and observation rules

### 5.1. Deadlines and request ownership

Resolve the existing effective timeout once, after handler validation and before
registration: the minimum of the existing configured maximum, requested
`harden.timeout_ms` when nonzero, and the incoming context deadline. Current
maximum remains 60 seconds; zero retains existing default meaning. Registration
SQL consumes this same execution budget. Do not start a fresh full timeout after
registration, increase server write/shutdown timeouts, or detach inference.

The original HTTP handler remains the only execution owner. Cancelling it
cancels the same provider context. Tracking collectors may use a bounded
metadata-only cleanup context; they never own, restart or extend inference.

### 5.2. Reuse current events; do not add a second execution engine

Attach a small tracking session to the request context in the stateful layer.
Use the existing `Request.Progress` channel for cumulative stage/counter facts.
Progress is best effort and nonblocking; do not require delivery of its terminal
event. A successful core result plus handler projection checks are the
authoritative final outcome.

For retry waits, decorate the existing `Observer` in `stateful/client.go`.
`StartRetry` already receives the classified reason and actual planned duration
before `waitForRetry` sleeps. Record wait entry/exit through the session carried
in context. Forward every observer method, returned context and completion
callback to the existing telemetry observer exactly once. Without a tracking
session, decoration does no tracking work. Do not replace telemetry or put
tracking SQL, API-key hashes or a telemetry SDK into the root core.

`StartRetry` with a rate-limit classification sets `waiting`,
`reason=provider_throttled`, `stage=retry_wait` and a known next-attempt time.
Other scheduled waits use `retry_backoff`. A returned 429 without a scheduled
retry is a failed outcome, not a waiting state. Wait exit clears wait fields
if that same wait is still current. A stale callback must not clear a newer
phase. Provider/validation observer facts can improve stages, but do not claim
that entering a provider adapter proves bytes reached upstream. Native CPA
internal attempt count is unavailable and must not be fabricated.

Use one bounded progress channel (capacity 64) and at most one collector
goroutine per tracked live request. A session mutex protects a small projected
snapshot/revision shared with observer callbacks; callbacks only update memory.
The collector drains/project events and flushes the newest snapshot at most
once every five seconds, plus registration and one authoritative final update.
Do not issue SQL or log a record per token or poll. Phase changes may coalesce;
a short retry can finish between flushes. State this observation resolution.

Advance `last_progress_at` only on a real phase, attempt or received-byte/event
change; repeated identical snapshots and heartbeat ticks do not advance it.
Advance heartbeat only when the execution collector writes liveness. GET's
`observed_at` is response read time and never evidence of worker progress.
Whitelist and copy fields immediately; never persist `ProgressEvent.Attempts`,
accounting payloads or arbitrary event data wholesale.

Use the existing structured logger for one registration and one final summary,
plus bounded metadata-write failure diagnostics. Include validated tracking ID,
fixed endpoint/stage/outcome, elapsed time, known retry wait and the existing
call/trace correlation when available. Never log raw provider errors, key/hash,
nonce, input or output. Do not put a UUID into metric labels or create a
per-token/per-poll log stream; the existing HTTP logger already records polls.

On owner completion, drain pending progress, apply the authoritative outcome,
prevent any later heartbeat update, and finish once. A total metadata-only
shutdown/final-write budget of 600 milliseconds bounds collector cleanup; use
one budget, not a new budget per retry. Stop tickers and join the collector.
An independent metadata watchdog ends it no later than the original deadline
plus one second if the handler cannot finish it. Neither mechanism calls the
provider or reports cancellation as successful generation.

### 5.3. Final outcome and stale observations

| Evidence | Tracking projection |
| --- | --- |
| Core success and valid, size-checked normal response projection | `completed`, confirmed |
| Known local validation, terminal refusal, invalid model output or projection failure | `failed`, confirmed, fixed safe error code |
| Cancellation/deadline with positive proof no provider execution began | `cancelled`, confirmed |
| Timeout, disconnect or ambiguous transport after provider execution may have begun | `unconfirmed`, not confirmed |
| Lost observation/worker before an outcome was recorded | Stale current state, then derived `unconfirmed` after deadline plus 30 seconds |

Do not classify an outcome from HTTP status alone or infer absence of execution
from a dropped progress event. Existing core retries/repair policy is unchanged;
tracking itself never adds a retry. Use existing result/attempt/stream evidence;
where evidence cannot prove a known outcome, choose `unconfirmed`.

`completed` means a valid normal response was ready. It does not prove that the
client received every byte. A subsequent SSE write failure does not erase that
known generation/projection result. An invalid final projection must be marked
failed, so do not finalize success inside `execute` before the handler builds
and size-checks its public response. Preserve existing wire errors.

Before deadline plus 30 seconds, a missing heartbeat only sets
`observation_stale=true`; it does not prove worker death. Afterwards, GET may
derive `status=unconfirmed`, `reason=execution_unobserved`,
`outcome_confirmed=false`, keeping `finished_at=null` if no owner finish was
recorded. Do not mutate the row, declare provider termination, release admission
capacity, or start inference. A later committed authoritative finish may replace
this derived observation. Persisted owner-finalized outcomes remain immutable.
This lookup is not a permit ledger, execution recovery service or result store.

### 5.4. Handler algorithm to implement

```text
ordinary POST handler:
  authenticate and derive trusted key scope using existing middleware
  parse optional tracking header and perform existing request validation
  resolve one existing execution deadline
  if tracking header is present:
    reserve UUID + fresh invocation nonce in that key scope
    if conflict: return existing-shaped 409; do not call the core
    if storage outcome is not positively known: return 503; do not call the core
    start one metadata session/collector and install its deferred cleanup
    attach session to context and its progress channel to the core request
  call the existing core once, preserving its configured recovery policy
  if the core returns an error:
    finalize metadata from actual evidence, if tracking is active
    write the existing OpenAI-shaped call error and return
  construct and validate the existing endpoint's public response
  if response construction/size validation fails:
    finalize metadata as failed, if tracking is active
    write the existing projection error and return
  finalize metadata as completed, if tracking is active
  write the existing JSON or final-only SSE response
  finish metadata cleanup once; do not replay on any write/storage failure

GET lookup handler:
  authenticate using the same middleware and validate path/query
  read unexpired row using trusted key scope + UUID
  return the fixed 404 or 503 if appropriate
  project read time, elapsed/wait remaining and stale-derived uncertainty
  return no-store status JSON; never call an execution function
```

Use one helper/session abstraction for the two generation handlers; do not copy
their inference implementation into new tracking-specific handlers. A guard on
finalization makes the success/error path plus deferred cleanup safe to call
together without a second SQL finish or overwritten terminal outcome.

## 6. Proposed file changes and small interfaces

| Proposed surface | Implementation task |
| --- | --- |
| `stateful/request_tracking.go` | Payload-free start/snapshot/record types, validation, projection, clock/retention helpers |
| `stateful/request_tracking_store.go` | `Service` methods to reserve, conditionally update/finalize and read scoped metadata |
| `stateful/request_tracking_schema.sql` | Additive DDL and checked columns/index |
| `stateful/request_tracking_observer.go` | Context session and delegating retry/phase observer |
| `stateful/store.go` | Apply additive tracking DDL on fresh and existing valid v1 databases |
| `stateful/client.go` | Decorate existing telemetry observer once in concrete composition |
| `internal/gateway/httpapi/request_tracking.go` | Header parsing, collector/session owner, lookup handler and safe wire projection |
| `internal/gateway/httpapi/httpapi.go` | New authorized GET route and narrow tracking-store field defaulting to the shared service |
| `internal/gateway/httpapi/handlers.go` | Reuse one deadline, register only tracked validated requests, finish at the response-projection boundary |
| `api/openapi.yaml` | Header parameters, GET path, typed status schema and error codes |
| Existing tests / new `*_test.go` files | Cases in Sections 7 and 8 with canonical IDs |
| `README.md` / existing API usage documentation | Two-connection example, lifecycle and retention limits |
| `docs/adr/ADR-HLLM-032-openai-inference-contract.md` | Clarify optional metadata observation while preserving stateless inference and unsupported background/response storage |

Use a small gateway-private store interface with reserve, update and lookup
methods using stateful payload-free values. Production implementation is the
existing `*stateful.Service`; same-package handler tests inject a narrow stub.
Do not add a generic repository framework, new public Go client registry,
separate status service or a test-only alternate production implementation.

The session handle contains nonce, current revision, mutex/snapshot and a
nonblocking event sink. It must not retain the full request/result, API key,
credentials, response writer or a provider execution closure. Keep scope/nonce
out of the returned JSON. Test clock/ticker dependencies locally; no global
mutable clock or process-global tracking map.

## 7. Ordered implementation tasks

Execute each phase in order. Write the named failing regression before its
implementation; confirm an assertion failure, not a compile/setup error or zero
matched tests. Fix the smallest surface, rerun the same assertion, then move on.
Keep passing wire/cancellation/history assertions intact. Mark checklist items
only with source/test evidence. This document does not allocate canonical
`TEST-###` numbers; do that from the implementation base to avoid conflicts.

### P0. Prepare an isolated implementation checkpoint

1. [ ] Read `AGENTS.md` and all of `docs/liveview-go-testing-guidelines.md`.
2. [ ] Record source branch/SHA and inspect dirty files; create a feature branch
   from current `dev`/`main` policy without carrying unrelated edits.
3. [ ] Locate every symbol in Section 2 on that base. Record whether batch
   PR #127 has merged; no dependency on it is required for lookup.
4. [ ] Allocate unused canonical test IDs for header/contract, reducer/session,
   SQL/migration and real transport cases; enter them in the catalog and the
   appropriate existing tasks in `test/test-tiers.json`.
5. [ ] Add a short implementation evidence section to this plan recording each
   phase's commands, results, SHA and unresolved checks; leave future phases open.

Exit: source and test ownership are explicit; no runtime changes yet.

### P1. Add the contract and parsing regressions

1. [ ] Add failing header parser cases: absent, valid UUID, nil, malformed,
   compact/URN/uppercase, empty, duplicate fields and comma-separated values.
2. [ ] Add failing OpenAPI schema/route assertions for the new GET, optional
   header on exactly two POSTs, same bearer and fixed error shapes.
3. [ ] Add the OpenAPI definitions from Section 3 and implement only the pure
   header/path parser in the proposed gateway tracking file.
4. [ ] Update the exact existing route allowlist without weakening its length
   or unexpected-route assertions. Add only the intended new path.
5. [ ] Validate the complete example against the actual OpenAPI schema; test
   null timing fields, every status and rejected unknown enum values.

Exit: parsers/contract pass; ordinary handler behavior is still unchanged.

### P2. Implement the metadata reducer and wire projection

1. [ ] Add a table-driven transition test for start, provider/validation phase,
   actual retry enter/exit, result projection, finish and repeated stale events.
2. [ ] Implement payload-free stateful types and a pure transition reducer with
   increasing revision and fixed allowlists. A terminal owner finish is final.
3. [ ] Add fake-clock assertions for elapsed time, remaining retry delay,
   frozen terminal elapsed time and nonnegative values under clock skew.
4. [ ] Add separate heartbeat/progress tests: identical snapshots/ticks cannot
   advance progress; GET time cannot advance heartbeat.
5. [ ] Implement pure stale projection: 15-second liveness threshold and
   deadline-plus-30-second derived uncertainty, without modifying persistence.
6. [ ] Assert that finalization clears retry fields, and stale wait-exit callbacks
   cannot clear a newer phase or overwrite terminal status.

Exit: all transition meanings are deterministic and testable without services.

### P3. Add one table through the existing schema command

1. [ ] Add real PostgreSQL RED cases: fresh schema gains the table; existing v1
   schema gains it with sentinel profiles/history/cache untouched; repeat apply
   is idempotent; incompatible base schema still rejects without mutation.
2. [ ] Embed the new SQL and invoke it inside the existing bounded schema
   transaction after base schema validation/creation. The current early return
   for existing v1 must no longer skip additive tracking DDL.
3. [ ] Preserve the accepted reference schema version and its existing readers;
   this additive table does not justify resetting data or inventing a new
   migration CLI. Never run DDL on a normal HTTP request.
4. [ ] If a same-named table exists incompatibly, detect missing/mismatched
   required shape and return an operator error; do not silently reset it or
   treat `CREATE TABLE IF NOT EXISTS` as proof of compatibility.
5. [ ] Add SQL constraint/index assertions and keep existing DDL/request budget
   tests passing. Existing schema/rollback tests keep their original oracle.

Exit: explicit `schema` command upgrades valid existing storage additively.

### P4. Implement scoped reserve/update/read operations

1. [ ] Add T1 store-interface tests for failure mapping, then T3 tests of real
   reservation uniqueness across two clients/replicas and two key scopes.
2. [ ] Implement reserve with one fresh invocation nonce; map only the exact
   unique-key conflict to `tracking_id_conflict`.
3. [ ] Add a transaction fault test for ambiguous COMMIT. Confirmed readback
   of this exact nonce may acknowledge registration; absent/failed readback
   cannot authorize the core. Assert no replacement insertion or inference.
4. [ ] Implement revision/nonce-fenced snapshot and final updates. Test stale
   revision, duplicate finish, different nonce and terminal immutability.
5. [ ] Implement scoped lookup and logical expiry; other-key IDs have the exact
   same response as absent IDs. Never expose hash/nonce in public projections.
6. [ ] Implement indexed, bounded expired-row cleanup. Test the requested
   expired ID can be reused even when more than 100 other rows are expired;
   the old nonce cannot update the new record. Other keys/live records survive.
7. [ ] Test nil pool, acquisition timeout, query failure and read outage produce
   a safe storage error. Test cleanup does not call any artifact/history API.

Exit: database races and retention are proved at the actual SQL boundary.

### P5. Wire ordinary request registration and authoritative completion

1. [ ] Add RED handler tests using a synthetic blocked provider and injectable
   tracking store: a validated tracked POST reserves once before core entry;
   invalid input, conflict and unavailable registration dispatch zero calls.
2. [ ] Extract the existing timeout resolution into one helper reused by both
   tracked/untracked execution; create the deadline once before tracked reserve.
   Keep existing timeout defaults, maximum and earlier-parent precedence.
3. [ ] Pass a tracking session/progress channel only for header-bearing requests.
   Without a header, assert zero tracking-store calls and no collector.
4. [ ] Add deferred failure finalization for every registered exit path, including
   core validation, provider failure, projection error and handler panic cleanup.
   A panic is rethrown to the existing server recovery; tracking must not hide it.
5. [ ] At successful projection and size validation, finalize known completion
   before beginning the existing JSON/SSE body. Do not finalize on a dropped
   runtime terminal event. Avoid changing JSON/SSE encoding to obtain status.
6. [ ] On metadata final-write failure, preserve the already known wire result
   and provider dispatch count. Use one bounded metadata cleanup context.
7. [ ] Assert the header is absent upstream and does not affect cache identity:
   fresh tracking IDs can observe a cache hit without another provider call.

Exit: ordinary API contracts remain intact with optional metadata tracking.

### P6. Connect real progress/retry facts and bound lifecycle overhead

1. [ ] Add observer-delegation RED tests proving all seven methods preserve
   the wrapped observer's contexts/callbacks and invoke each exactly once.
2. [ ] Implement the stateful observer decorator/session context. Test no-session
   calls preserve existing telemetry and do no tracking work.
3. [ ] Test retry enter/exit using `StartRetry` with a controlled wait; then use
   the real core with a local 429/Retry-After provider and controlled release.
   Poll while the original request is blocked and observe a genuine wait.
4. [ ] Test a final 429 with no scheduled retry becomes failed, and generic
   retry backoff uses the correct different reason. Do not alter retry policy.
5. [ ] Implement the one-collector/five-second coalescing rule with injectable
   clock/ticker; no real multi-second sleeps in unit tests.
6. [ ] Inject slow/failing storage and a full progress channel. Prove provider
   execution/callbacks are nonblocking, snapshots remain bounded and no raw
   payload enters storage/logs. All SQL still has its short acquisition budget.
7. [ ] Race heartbeat/progress/finish/cancel. Prove final status wins, the same
   request has one collector, and completion/shutdown/watchdog join it and stop
   its ticker. Do not make the core close a caller-owned progress channel.

Exit: meaningful waiting/progress is observable without new inference control.

### P7. Add authorized GET and real transport checks

1. [ ] Add RED lookup tests for auth, two keys/same UUID, missing ID, expired ID,
   invalid path/query, storage errors and `Cache-Control: no-store` on every case.
2. [ ] Add the route through `api.authorize`, read its derived scope and call
   the shared store. Do not accept hash/owner/user/pool selectors from callers.
3. [ ] Test two gateway instances against the same real PostgreSQL lease: POST
   through A, observe running/finished through B. Do not rely on an in-memory map.
4. [ ] Test actual client cancellation/disconnection during a held provider call.
   Await provider-start and handler-exit acknowledgements. Poll through a fresh
   connection; classify uncertainty from evidence and assert tracking introduced
   no provider dispatch/replay. Keep configured core attempts explicit.
5. [ ] Test process-observation loss using a persisted active record, a second
   API instance and injected time. It first becomes stale, then derived unknown;
   no provider construction/start or DB terminal mutation is performed by GET.
6. [ ] Test a complete valid result followed by actual response-write failure:
   completed describes known execution/projection, not proven client delivery.

Exit: status is useful across connections/replicas and truthful under failures.

### P8. Prove compatibility, isolation and representative scale

1. [ ] Extend the existing official Go SDK fixture with the optional header
   through its existing per-call/header mechanism. Cover both POST routes,
   JSON and final-only SSE, tools, structured output and an OpenAI-shaped error.
2. [ ] Preserve existing untracked fixtures byte/shape/event assertions and
   dispatch counts. Never enable real providers or SDK retries in these tests.
3. [ ] Query actual SQL after tracked and untracked ordinary calls: reference
   history/statistics/profiles are unchanged, no artifacts were written and
   tracking rows contain no prompt/output/credential sentinels.
4. [ ] Locally exercise 1,000 distinct tracked calls with bounded test-generator
   concurrency, two keys and polling. Use only owned synthetic providers and
   the real shared test PostgreSQL; no Temporal/Garage dependency is needed.
5. [ ] Verify distinct-ID count, no duplicate execution introduced by tracking,
   terminal counts, cleanup/nonce fencing and cross-replica reads. Record total
   SQL operations, peak collectors, CPU/RAM and latency against an equivalent
   untracked local run. Do not claim this measures CPA capacity.
6. [ ] Do not require 1,000 simultaneous OS connections as a unit-test oracle;
   bound the generator and record actual concurrency. Investigate regressions
   with evidence rather than inventing a universal performance target.

Exit: useful-scale tracking is measured without changing inference semantics.

### P9. Documentation, verification and delivery

1. [ ] Document header generation, two-connection polling, initial 404 race,
   polling interval, fixed retention, same-key lookup, duplicate `409` and the
   difference between metadata completion, delivery and unknown execution.
2. [ ] State that polling cannot extend client/proxy/server timeouts and cannot
   retrieve output. Do not advertise background execution or batch support.
   Update ADR-HLLM-032's boundary language to distinguish tracking metadata from
   reference history or retained responses; the root execution core stays stateless.
3. [ ] Run the commands in Section 9 at their proper boundaries. Record failures
   and remaining gates; do not mark a Docker or transport gate passed from a stub.
4. [ ] Commit/push coherent verified checkpoints and update issue #136 with
   exact source and evidence. Preserve unrelated work and accepted data.
5. [ ] Prepare rollout instructions: run the additive schema command with the
   matching build before enabling tracked requests; reuse the same pool/network.
   An older binary ignores the new table, but rolling backward while callers
   expect tracking removes that feature. Drain active ordinary calls first.
6. [ ] Only when deployment is separately undertaken, record branch/source SHA,
   API image identity/environment URL and browser-free health/auth/status
   acceptance. Never infer live status support from this document or a draft PR.

Exit: local implementation is reviewable and remaining operational gates explicit.

## 8. Acceptance matrix for the later implementer

These are plan case labels, not substitute canonical executable test IDs.

| Case | Tier | Required invariant |
| --- | --- | --- |
| RS-01 | T0 | Strict optional header/path parsing; same canonical UUID rule |
| RS-02 | T0/T1 | OpenAPI header/route/schema/examples and unchanged existing routes |
| RS-03 | T0 | Monotone reducer, wait ownership, finish fencing and timing projection |
| RS-04 | T1 | Auth first, invalid input no registration, absent header no tracking |
| RS-05 | T3 | Same-key uniqueness across replicas; different keys independent |
| RS-06 | T3 | Unknown reserve COMMIT requires same-nonce readback before execution |
| RS-07 | T3 | Revision/nonce/terminal CAS and expiry/reuse races |
| RS-08 | T3 | Additive schema, idempotent apply, preserved existing data/budgets |
| RS-09 | T1 | Original POST JSON/SSE/errors, tools/schema and cache identity unchanged |
| RS-10 | T1 | Actual scheduled retry reported; terminal 429 is not fabricated waiting |
| RS-11 | T0/T1 | Heartbeat != progress != GET time; stale-derived unknown does not mutate |
| RS-12 | T1/race | Bounded nonblocking collector, failure isolation and cleanup |
| RS-13 | T3 | POST A / GET B through the shared real PostgreSQL lease |
| RS-14 | T1/T3 | Real transport cancellation and completed-result delivery ambiguity |
| RS-15 | T3 | Storage/log allowlist and no ordinary history/artifacts |
| RS-16 | T3 | 1,000 local IDs, accurate counts, bounded overhead and cleanup |
| RS-17 | T1 | GET auth/404 isolation/503/no-store and no inference side effects |

Use channel acknowledgements and fake clocks where possible; real HTTP and SQL
are retained for their distinct boundaries. Every newly allocated canonical ID
must appear in its test comment, catalog and correct runner task. Do not add a
browser test, DOM dependency, public-network fixture or new test framework.

## 9. Verification commands and evidence discipline

Focused fast checks after each affected implementation phase:

```sh
go test ./internal/gateway/httpapi ./stateful ./internal/runtime -count=1
go test -race ./internal/gateway/httpapi ./stateful -count=1
git diff HEAD --check
```

Run existing OpenAPI/SDK and observer/retry holdouts even if only one tracking
case was edited. At a coherent implementation checkpoint run the established
browser-free aggregate with the pinned toolchain:

```sh
PATH=/home/kirill/.local/elixir-1.20.2/bin:/home/kirill/.local/otp-28.4.3/bin:$PATH make test-fast
```

For the real SQL/replica/transport cases use the repository's integration pool
and runner, not an unconfigured bare integration invocation:

```sh
make test-integration
```

Select the existing integration-race task when verifying SQL races; inspect its
current Makefile/runner command on the implementation base. Full `make verify`
can be used as the aggregate deterministic backend gate when Docker is available.
No browser, paid-provider test, full release certification or production
promotion is implied by these commands or this plan.

Evidence must distinguish: contract documented; code implemented; deterministic
checks run; real SQL/HTTP checks run; source pushed; environment deployed; live
HTTP acceptance. Planning checkboxes start unchecked. A missing required gate
remains outstanding rather than being replaced with a weaker oracle.

## 10. Completion checklist and implementation record

- [ ] Optional header and lookup route implemented exactly as documented.
- [ ] Existing untracked clients have unchanged bodies, wire formats and policy.
- [ ] Same-key ID reservation is race-safe before execution; duplicates are `409`.
- [ ] Shared metadata survives gateway changes without recovering/replaying work.
- [ ] Waiting derives from an actual scheduled wait; liveness/progress stay distinct.
- [ ] Cancellation, stale metadata and unknown outcomes remain truthful.
- [ ] Retention, cleanup, nil-storage and outage behavior are tested and documented.
- [ ] Root core remains stateless; no reference history/output/artifact persistence.
- [ ] Canonical test registration, focused/race/fast/SQL gates and evidence complete.
- [ ] Source/checkpoint linked from issue #136; deployment state stated accurately.

Implementation record: **not started by this planning change**. Add dated phase
entries here during implementation. Do not mark the broader CPA scheduling issue
complete when this status-only feature ships.
