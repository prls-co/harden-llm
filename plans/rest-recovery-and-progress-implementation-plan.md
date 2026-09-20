# 1. REST-first recovery, progress, and diagnostics implementation plan

- Document ID: `PLAN-HARDEN-LLM-RECOVERY-004`.
- Version: 1.0.0.
- Date: 2026-09-18.
- Status: implemented; deterministic and PostgreSQL/Garage integration gates verified on 2026-09-18. Browser, deployment, and paid-provider certification remain intentionally unrun.
- Inspected baseline: `main`, `1b7c281beccc524dd70471103ffa721431f3d6ae`.
- Audience: an implementing agent, including GPT-5.6 Luna, working one checkpoint at a time.
- Scope: this repository only. No changes to agent-platform, `ops`, or `utility-llm` repositories.
- Authority: the agreed recovery flow and REST-first product direction. This plan does not authorize deployment, live-provider calls, browser testing, or production data migration.

Implement explicit initial/escalated JSON repair and one fresh generation rerun,
with bounded execution and useful diagnostics. Keep the existing Go execution
engine, REST gateway, PostgreSQL history, Garage artifacts, and telemetry.
Do not build a workflow platform or a second observability system.

## 2. Read first and preserve

Before changing code, read these files completely:

1. [Repository instructions](../AGENTS.md).
2. [LiveView and Go testing guidelines](../docs/liveview-go-testing-guidelines.md).
3. [Architecture and ownership](../docs/architecture.md).
4. [Canonical accounting and recovery](../docs/adr/ADR-HLLM-018-canonical-execution-accounting-and-recovery.md).
5. [Existing recovery policy](../docs/adr/ADR-HLLM-020-recovery-policy-and-execution.md).
6. [Recovery boundary ownership](../docs/adr/ADR-HLLM-021-recovery-boundary-ownership.md).
7. [Recovery integrity boundaries](../docs/adr/ADR-HLLM-022-recovery-integrity-boundaries.md).
8. [Timeout change policy](../ker/timeouts/README.md).

This plan supersedes ADR-HLLM-020's **single-target-only** restriction, not its
single-loop, strict validation, complete-policy, or ownership decisions. Record
that narrow change in a new ADR before implementation. ADR-HLLM-024 is available
at the inspected baseline; recheck numbering before creating it.

Use these canonical specifications throughout:

- [Backend implementation](from_utility-llm/harden-llm-self-hosted-implementation-plan.md).
- [Go/REST/storage](from_utility-llm/self-hosted-go-stack-spec.md).
- [Backend tests](from_utility-llm/harden-llm-self-hosted-test-spec.md), document `SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001`.
- [Frontend](from_utility-llm/phoenix-liveview-frontend-spec.md), document `SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001`.

Preserve unrelated working-tree edits. At inspection, the browser canary file
`frontend/test/browser/deployed_canary_test.exs` was already modified; it is not
part of this work. Start implementation on the repository's normal development
branch, not by making unrequested changes to production `main`.

### 2.1 Verified current implementation and gaps

| Area | Current source and behavior | Required delta |
| --- | --- | --- |
| Policy | `internal/retry/retry.go`: `maxAttempts`, `retryOn`, `repairInvalidOutput`, `backoff`; default four attempts | Two explicit repair targets; optional fresh-rerun target with its own instance of the same repair structure |
| Execution | `internal/runtime/execute.go`: one loop; prepared repair work survives transient failures | Carry stage, target, generation identity, and repair history together through that loop |
| Repair input | `internal/runtime/repair.go`, `internal/providers/payload.go`: previous output and feedback | Flat complete history for the active generation branch, including failed initial repair output |
| Provider streams | `internal/providers/router.go`: bounded whole-body read before parsing SSE | Incremental events/counters; finish on a valid protocol terminal event, without waiting for EOF |
| Timing/history | `internal/gateway/run_service.go`: final duration, attempts, wait totals, run persistence | Live progress; exact actual waits; phase, deadline, origin, and partial stream measurements |
| REST | `POST /api/v1/run`: synchronous final JSON; effective execution cap at most 60,000 ms | Opt-in SSE on the same POST; unchanged JSON transport by default; no detached jobs |
| Cache | `client_cache.go`, `internal/runtime/cache.go`: producer identity checked against generation target | Separate generation identity from actual successful repair producer |
| UI | Shared profile state/component, embedding harness, result and trace components | Target-only picker mode; shared repair editor; strict diagnostics/progress decoding |
| Versions | Profile/client state/bundle v2; result v3; cache record v2; response projection v3 | Explicit coordinated changes in Section 10 |
| Timeouts | `TEST-039` checks `ker/timeouts/baseline.json` and RCA evidence | Preserve it; provide evidence rather than changing the baseline |

There is no need for a new database to record durations: runs already have
canonical durable results. Garage holds larger artifacts. Existing telemetry
exports observability data, but is not the authoritative product-history reader.
Live status is the missing contract; an external dashboard cannot supply that
contract to an in-flight REST caller by itself.

### 2.2 Explicit non-goals

- No ClickHouse deployment, broker, Temporal integration, detached task store, or polling service.
- No general provider failover graph, recursive policies, or unlimited repair loop.
- No heuristic JSON salvage, numeric coercion, semantic critic, or additional validation LLM.
- No repair-result cache and no repair-policy component in generation cache identity.
- No character-to-token estimate or consumer-side wrapper claiming precise token enforcement.
- No new UI framework, npm/Hex package publication, or cross-framework widget port.
- No timeout increase, provider-limit reduction as a timeout workaround, or assertion weakening.
- No production configuration guesses for Astra. Configuration availability is a release prerequisite.

## 3. Product contract and acceptance requirements

REST is the principal integration surface. The Go library owns execution; the
gateway adds authentication, owner-scoped resources, persistence, and transport.
Phoenix is one API consumer, not a required part of headless execution.

Every executable setting visible in the UI must serialize to the same documented
API contract. A future agent host can submit that contract without running
Phoenix, and a future Phoenix host can reuse the presentation components without
copying execution rules. Extracting a distributable UI package is separate work.

Reserve the following requirement IDs; confirm no collisions before registration
in `docs/requirements-traceability.md` during checkpoint P01.

| ID | Required acceptance condition |
| --- | --- |
| REQ-301 | Exactly the bounded stage order in Section 4, with one global attempt budget and deadline |
| REQ-302 | Both generation branches use the same two-target repair type and implementation |
| REQ-303 | Escalated repair sees original input, schema, failed generation, failed initial repair, and both diagnostics |
| REQ-304 | Fresh rerun receives original task/schema, not failed answers or repair instructions |
| REQ-305 | Selected recovery profiles are leaves; their own recovery settings never execute |
| REQ-306 | Target options, credentials, reasoning, and capabilities belong to that target, not the failed model |
| REQ-307 | Transient retry repeats identical prepared work; only completed invalid structured output advances recovery |
| REQ-308 | Cache lookup/write follows the generation branch; result producer and accounting remain truthful |
| REQ-309 | Canonical final/history/trace diagnostics identify stage, timing, origins, and bounded stream measurements |
| REQ-310 | Optional REST SSE and library progress report the same execution without introducing another executor |
| REQ-311 | Provider terminal semantics, cancellation, partial accounting, and received-byte bounds survive incremental parsing |
| REQ-312 | Clients distinguish soft expectations from immutable hard caps and retain performance failures after overruns |
| REQ-313 | Headless REST, workspace, saved profiles, exports/imports, and embedded widgets agree on policy |
| REQ-314 | Strict v3 schema migration preserves evidence and owner isolation; legacy v2 reading is bounded to the compatibility boundary, never mixed with a structured plan, and current writers never emit it |
| REQ-315 | Progress/logs omit raw prompts, outputs, credentials, and high-cardinality metric labels |
| REQ-316 | Existing fast-test/timeout-RCA policies remain enforced; no paid calls or browser required for deterministic acceptance |

## 4. Exact recovery flow and inputs

### 4.1 Names and default targets

- `I`: original system/user input, including existing task instructions.
- `S`: original normalized output schema and the existing application validation contract.
- `G0`: caller-selected original generation target.
- `O0`, `D0`: completed original output and its strict syntax/schema diagnostic.
- `O1`, `D1`: completed first repair output and its diagnostic.
- `R0`, `E0`: completed fresh-rerun output and its diagnostic.
- `R1`, `E1`: completed rerun's first repair output and its diagnostic.

`L` and `H` are UI labels for the profile's portable `lowest` and `highest`
reasoning settings. They are not literal native-provider parameter values.
The current CPA Luna profile maps `highest` to native `xhigh`; do not replace it
with a guessed `high` mapping.

| Order | Stable stage ID | Default target | Semantic input |
| --- | --- | --- | --- |
| 1 | `original.generate` | Original selected profile/model/options | `I + S` |
| 2 | `original.repair.initial` | CPA GPT-5.6 Luna L | `I + S + [(O0,D0)]` |
| 3 | `original.repair.escalation` | CPA GPT-5.6 Luna H | `I + S + [(O0,D0),(O1,D1)]` |
| 4 | `rerun.generate` | CPA GPT-6 Astra L | Fresh `I + S` |
| 5 | `rerun.repair.initial` | CPA GPT-6 Astra L | `I + S + [(R0,E0)]` |
| 6 | `rerun.repair.escalation` | CPA GPT-6 Astra H | `I + S + [(R0,E0),(R1,E1)]` |

Every row validates the completed result. Return immediately on success. An
invalid last repair returns a structured failure. There is no second rerun and
no independent rerun-generation escalation to another model.

```text
original I,S
  -> original generation
  -> initial JSON repair: original failed output + error
  -> escalated JSON repair: original and initial-repair outputs + errors
  -> fresh rerun of I,S
  -> rerun initial JSON repair: rerun failed output + error
  -> rerun escalated JSON repair: rerun and initial-repair outputs + errors
  -> failure

At every arrow: proceed only after invalid completed structured output,
only if configured, and only with attempt/deadline budget remaining.
At every model: success stops; transient failure retries that same work.
```

### 4.2 Full history without recursive prompt growth

Represent repair history as an ordered list of records containing stage,
attempt number, exact extracted output, and bounded validation feedback. Build
each repair prompt from `I`, `S`, and this list. Do not embed the previous
serialized repair prompt, duplicate `I` inside each entry, or summarize away
the failed first repair. A transient failed transport attempt has no completed
invalid output and adds no repair-history entry.

The rerun branch starts a new repair history. The full run trace still includes
both branches and the trigger linking them. Original-branch failures must not
leak into the fresh rerun prompt or its subsequent repair prompts.

Previous output is untrusted data: delimit it and explicitly instruct the model
to return only a value satisfying `S`. It cannot change a target, enable tools,
override credentials, or choose the next stage. Keep direct schema-constrained
output; do not restore the removed `{repair, data}` response envelope.

Reuse existing feedback bounds, response-size bounds, and request admission
limits. Account for the complete encoded repair request. If a known enforced
input limit cannot accommodate required history, return a specific
`repair_input_limit_exceeded` failure with size/limit diagnostics. Do not
silently truncate outputs, invent a token estimate, or retry a larger prompt.
If provider token capacity cannot be measured precisely, report it as unknown;
keep byte bounds and let the existing typed provider error terminate as defined.

### 4.3 Search and evidence

Repair is formatting/schema recovery, not a new research step. Disable native
search and Jina dispatch for both repair stages. Preserve the active generation
branch's already-collected search evidence/citations in the repair context and
successful result, without fabricating new search calls or double-counting cost.

Fresh rerun uses the original task's `webSearch` setting and existing native/Jina
routing rules. Preserve existing memoization semantics for identical fallback
searches; do not force a second Jina charge just because a branch changed.
Record search provenance against the actual generation branch and actual search
operation. A cache hit must invoke neither model nor search.

## 5. Policy, target selection, defaults, and validation

### 5.1 One policy type, not policies nested inside targets

Replace `repairInvalidOutput` with `jsonRepair` and `rerun`. Keep `maxAttempts`,
`retryOn`, and `backoff` at the root. All five keys are required in a complete
policy; `null` explicitly disables `jsonRepair` or `rerun`.

```json
{
  "maxAttempts": 6,
  "retryOn": ["network", "rate_limit", "server_error", "empty_response", "provider_retry"],
  "backoff": {"baseDelayMs": 500, "maxDelayMs": 8000},
  "jsonRepair": {
    "initial": {
      "source": "profile",
      "profileId": "CPA GPT-5.6 Luna",
      "reasoningEffort": "lowest"
    },
    "escalation": {
      "source": "profile",
      "profileId": "CPA GPT-5.6 Luna",
      "reasoningEffort": "highest"
    }
  },
  "rerun": {
    "target": {
      "source": "profile",
      "profileId": "CPA GPT-6 Astra",
      "reasoningEffort": "lowest"
    },
    "jsonRepair": {
      "initial": {
        "source": "profile",
        "profileId": "CPA GPT-6 Astra",
        "reasoningEffort": "lowest"
      },
      "escalation": {
        "source": "profile",
        "profileId": "CPA GPT-6 Astra",
        "reasoningEffort": "highest"
      }
    }
  }
}
```

Define the following Go types in the existing policy owner and export aliases
through the root public package. Do not create a new policy service/package.

- `RepairPlan`: required `initial: RecoveryTarget`, required `escalation: RecoveryTarget | null`.
- `RerunPlan`: required `target: RecoveryTarget`, required `jsonRepair: RepairPlan | null`.
- Profile target: `source: "profile"`, required nonempty `profileId`, optional
  `modelId`, `reasoningEffort`, and `providerOptions` with the same meanings and
  admission rules as the original target controls.
- Generation-relative repair target: exactly `{"source":"generation"}`.
  It resolves to the active branch's generation target and effective options.
  This preserves a useful same-model choice and provides the migration mapping
  in Section 10. It is permitted only in `RepairPlan`, never as `rerun.target`.
- A target has no `recoveryPolicy`, `jsonRepair`, `rerun`, cache setting, search
  setting, or escalation child. Reject those keys instead of ignoring them.

The same named profile may fill several roles with different reasoning/options.
Do not require users to duplicate the underlying saved profile for L and H.
Using Astra L for rerun generation and initial repair does not merge those two
settings: each remains independently editable.

### 5.2 Effective settings and preflight

Snapshot the owner-visible catalog and policy for one call. Resolve all enabled
targets relevant to the call type statically before any provider dispatch. Validate profile access,
credential references, endpoints, schemas, option types, and profile-declared
capabilities using existing owners. Preparation must not perform DNS or network
I/O; guarded transport still owns attempt-local DNS/connect behavior.

JSON repair/fresh rerun apply only to structured calls. Text calls keep their
existing success/transient-retry semantics and do not resolve or execute these
unused structured-recovery targets. Disabled branches likewise need no target
lookup. Preserve existing prompt-based structured output where a provider lacks
native schema enforcement; this change must not invent a new capability ban.

For a profile target, resolve its own profile defaults, its selected model,
its own reasoning map, and its explicit overrides using the existing documented
precedence. Never copy original-target native overrides over a different repair
or rerun target. For a generation-relative target, copy that branch's effective
generation settings, then apply only the mandatory repair tool/search disabling.
Reject conflicting native reasoning overrides through existing admission rules.

Keep `runtime.Profile` free of an executable recovery policy. Resolving a saved
profile as a leaf reads its provider configuration, not its recovery graph.
A target referencing the original profile is legal and cannot cause recursion.
Cycles between saved profiles' policies cannot execute because leaf resolution
never traverses those policies.

Use strict decoding at every write/execute/import boundary. Required nullable
keys need presence checks separate from non-null checks; the current
`decodeRequired` helper rejects null and cannot be reused unchanged for them.
Reject unknown fields, mixed old/new policy keys, wrong types, and partial plans
with precise field paths such as `recoveryPolicy.rerun.jsonRepair.initial.profileId`.

### 5.3 Defaults and the Astra prerequisite

One backend constructor owns the new full-recovery preset above. The six-slot
preset deliberately permits the six semantic calls; this is an attempt-budget
change for **new defaults**, not a timeout increase. It leaves no spare transport
retry slots. Keep the existing `1..10` valid range; preserve explicit stored
budgets and show when a configured chain may be cut short by the chosen budget.
Do not automatically raise a user's four-attempt policy to six or ten.

The inspected bundled catalog contains CPA Luna, but not CPA Astra. Before
enabling this default for real callers, obtain approved nonsecret CPA Astra
configuration/capability/reasoning metadata through the existing profile/config
workflow. Do not invent pricing, supported parameters, endpoints, or native
reasoning mappings, and do not substitute a direct OpenAI profile. Synthetic
local profiles may cover all implementation tests while this prerequisite is
unresolved. Missing enabled targets produce a clear preflight configuration
error; they must not silently fall back or spend an original-model call first.

Return defaults through the existing backend profile/defaults response. Phoenix
must not maintain a second JSON preset. Migrate/validate shared configuration,
profile bundles, and client state as well as the obvious run request.

## 6. Single-loop execution and failure classification

### 6.1 State carried through the loop

Introduce a small stage planner in `internal/runtime/recovery.go`, alongside the
existing `execute.go` and `repair.go`. It is a finite deterministic transition
function, not a general workflow engine. Carry one work value containing:

- Stage ID and `original`/`rerun` branch.
- Effective leaf target and prepared operation.
- Branch generation operation/target, kept separate from the repair operation.
- Immutable original input/schema and flat branch repair history.
- Trigger attempt number and input-history attempt numbers.

The transition function chooses the next configured semantic stage. The executor
still owns the only loop, retry classification, waiting, dispatch, validation,
cache boundaries, accounting, and global attempt counter.

```text
resolve and validate call/policy/targets without network
start original generation branch; check its cache if enabled
while global budget and parent context allow:
    execute current prepared work in one numbered attempt slot
    accumulate actual dispatch, timing, stream and accounting evidence
    if successful completed result passes admission:
        accept; cache under branch generation operation; return
    classify failure using existing ownership rules
    if configured transient failure:
        wait within remaining parent deadline; repeat identical prepared work
    else if completed structured output failed syntax/schema:
        append output and diagnostic to this branch history
        if parent ended or attempt budget exhausted: return with that stop reason
        choose next configured semantic stage, or return exhausted
        if entering fresh rerun: reset branch history; check rerun cache
        prepare chosen target/work; do not apply transport backoff
    else:
        return terminal failure
return attempt/deadline exhaustion with the last real failure preserved
```

A skipped disabled stage consumes no attempt. A cache lookup consumes no attempt.
Do not enter a new rerun branch after exhaustion just to probe its cache; the
original cache check still occurs before the first attempt as usual.
An admitted execution slot keeps the existing distinction between attempt and
actual provider dispatch: a connection failure can consume a slot without
proving the provider received or billed work. Preflight consumes neither.
Never renumber attempts per stage or give repair/rerun a fresh maxAttempts.

### 6.2 Outcome table

| Outcome | Action |
| --- | --- |
| Valid complete result | Return immediately |
| Completed extracted structured output fails syntax/schema | Advance to next configured semantic stage, if budget remains |
| Retryable network/rate-limit/server/empty/provider-retry category | Repeat same stage, target, repair history, and prepared payload if allowed |
| Attempt-local timeout while parent is alive | Existing network classification; same-work retry if allowed |
| Parent cancellation/deadline | Terminal immediately; no further stage or dispatch |
| Refusal/authentication/static configuration error | Terminal; no escalation as availability failover |
| Malformed transport envelope, incomplete terminal response, received-output limit | Existing typed terminal failure; never repair partial or envelope text |
| Invalid final configured repair or no attempts remaining | Terminal, with last error plus explicit stop reason |
| Cache lookup/integrity failure | Terminal under ADR-HLLM-022 |
| Cache write failure after accepted output | Keep success and `write_failed`; no extra model call |

Keep current error classification tests authoritative for protocol-specific
details. Do not match diagnostic text to decide transitions. Syntax/schema
failure requires evidence that a completed provider response was extracted.

Apply backoff/Retry-After only to transient repeats, not to semantic transitions.
Measure planned and actual waits separately. Cancellation during a wait records
the elapsed portion and prevents the next attempt. Check the parent context
before every network operation and after every wait/transition.

## 7. Cache and accounting ownership

### 7.1 Generation key, actual producer

Use the original generation operation as the original branch's cache key. If a
repair succeeds, cache that accepted structured result under the original key.
If recovery reaches fresh rerun, prepare/check its generation key before any
rerun search or provider call. A successful rerun repair writes only that rerun
generation key. Do not backfill the original key with a rerun answer.

Keys continue to include semantic generation inputs/options/model/search and
the canonical response-projection version. Recovery policy, repair prompts,
repair targets, diagnostics, client origin, and progress settings are excluded.
Cache refresh/bypass behavior remains the caller's existing cache-mode choice
and applies consistently to both generation branches.

Extend the cached provider projection with explicit `generationTarget` and
`completedBy: "generation" | "repair"`; retain `producer` for the actual
successful model. Validate the stored operation hash and generation identity
against the requested generation operation. Require producer/generation match
when `completedBy` is `generation`; when it is `repair`, validate the producer
snapshot and provenance without requiring it to equal the generation target.
Do not disable the existing provider/protocol/endpoint/model integrity checks.
Profile alias differences alone remain permitted as in ADR-HLLM-022.

The cache envelope already owns the operation hash; do not add a second full
operation or redundant provider-envelope sidecar to PostgreSQL. The projection
version bump makes prior cache payloads unreachable for new calls, rather than
turning ordinary old cache hits into integrity failures. No broad cache purge.

### 7.2 Required observable cases

- Original cache hit: zero current model/search calls, even with repair disabled.
- Original repaired cache replay: same generation key even if current repair
  target changed or its old producer profile was subsequently removed.
- Original miss, failures, rerun cache hit: preserve all already-incurred current
  provider costs; do not reset attempts or pretend this was a zero-cost run.
- Repair success: selected target remains the original user selection;
  `generationTarget` identifies the successful branch; actual result producer
  identifies the repair model.
- Provider ledger includes every real attempted provider operation with the
  existing known/unknown coverage semantics. Result ledger describes the
  returned result, including cached attribution. Never price all work using G0.
- Unknown usage on dispatched failed attempts remains unknown, not zero, even
  when a later provider returned complete usage.

## 8. Canonical diagnostics, traces, and origins

### 8.1 Final records first

Extend the existing canonical call/attempt records and derive gateway results,
history, artifacts, and telemetry from them. Do not create a second mutable
analytics record updated independently on every provider delta. Save the
execution aggregate once using the existing persistence/recovery machinery.

Add these fields with explicit units and nullability to result v4:

| Location | Fields/meaning |
| --- | --- |
| Run | `generationTarget`, `origin`, `stopReason`, `totalActualWaitMs`, `diagnostics` |
| Run diagnostics | Effective hard execution limit/deadline, elapsed execution, attempts used/remaining, branch-cache decisions, current/final stage |
| Attempt | `stage`, `branch`, `triggerAttemptNumber`, `inputAttemptNumbers`, `transportRetryOfAttempt` |
| Attempt diagnostics | Start/end timestamps, elapsed ms, effective reasoning, local dispatch evidence, first event/output latency, last event/output time, stream counters, terminal-event state |
| Wait | Planned delay ms, actual waited ms, reason, relevant Retry-After value |
| Accounting | Existing result/provider ledgers and coverage, with partial-stream evidence retained |

Define exact OpenAPI fields in P01 and make strict fixture examples for all
nullable values. Retain current `wait` and `duration` nanosecond fields during
this coordinated version transition to avoid an unrelated accounting/UI rewrite;
label them accurately. Existing `totalWaitMs` is planned wait; new
`totalActualWaitMs` is measured wait. Never silently reinterpret historical data.
Prefer deriving equivalent ms presentation fields rather than storing another
independently updated timer.

Separate execution duration from persistence/response-delivery duration. The
call hard cap bounds execution. Existing bounded final persistence may continue
briefly afterward; keep the existing 5-second persistence/shutdown margin and
explicitly test deadline-edge completion rather than increasing it.

### 8.2 Stream measurements and honest limitations

Track transport bytes, parsed event count, output text bytes, and Unicode code
points separately. Counters say exactly what they count; none is named tokens.
Count newly received output deltas once; do not count terminal response text as
new output again. For a non-streaming response, expose unavailable first-token
or delta measurements as null, not fabricated zero.

Provider-reported token usage remains a separate measurement with a source and
coverage state. If precise tokenizer-based received-text protection is added
later, it belongs in the provider/library owner and needs model/tokenizer parity
evidence. This plan does not install a tokenizer or guess tokens from characters.

Incremental parsing must enforce the existing received-response byte limit while
reading, even for a stream that never terminates. Preserve useful partial counts,
last activity, dispatch evidence, and any known usage on limit/timeout/cancel.
Do not accept accumulated text as a complete output when the terminal event is
missing. A stream can be active and still be runaway; report growth and stop
reason instead of interpreting activity as evidence to extend the hard cap.

### 8.3 Provenance and privacy

Add optional request `origin` with typed bounded string fields: `client`,
`component`, `operationId`, `parentRunId`, `jobId`, `testRunId`, `testId`, and
`sourceRevision`. Limit each supplied value to 256 UTF-8 bytes and the serialized
object to 2 KiB; reject unknown fields. These are correlation labels, not trusted
identity. They cannot override authenticated owner, generated run/call IDs, or
authorization. Map them consistently into the existing library observability
context and canonical record rather than replacing existing context fields.

Continue/implement standard W3C trace-context propagation through the existing
OpenTelemetry owner. Use the existing library parser/propagator, not a custom
traceparent parser. Client-provided trace context does not grant access to another
owner's trace. Keep canonical IDs and external-parent context distinguishable.

Persist stage/attempt ancestry and references to exact authorized input/output
artifacts, including the first failed repair response. Use the existing Garage
artifact lifecycle, integrity, ownership checks, and redaction. Do not overwrite
old immutable artifacts during migrations. Raw prompt/output content belongs
only in the existing authorized content/artifact surfaces, not routine logs,
metrics, heartbeats, or progress events. Audit the exact prepared request through
redacted artifacts/hashes; never serialize credential headers into a trace.

Use low-cardinality stage/outcome/provider dimensions for existing metrics.
Run IDs, test IDs, prompt text, and arbitrary origin values belong in traces/log
fields, not metric labels. Do not add an analytics table or per-delta SQL writes.

### 8.4 Field definitions to carry into OpenAPI

Use these names rather than inventing different fields in each layer:

- `stopReason`: `succeeded`, `attempts_exhausted`, `recovery_exhausted`,
  `deadline_exceeded`, `canceled`, `terminal_error`, `configuration_error`,
  `repair_input_limit_exceeded`, or `response_limit_exceeded`. The existing
  underlying error code/category still explains the failure. Historical unknown
  is null; a cache success is `succeeded` with its existing cache result.
- `RunDiagnostics`: `stage`, `effectiveTimeoutMs`, `deadlineAt`, `elapsedMs`,
  `attemptsUsed`, `attemptsRemaining`, `branchCaches`. The library may have no
  parent deadline, in which case timeout/deadline are null. REST always has one.
  `branchCaches` is an ordered array of `{branch, generationTarget, cache}` using
  the existing canonical cache-result shape for each generation branch reached.
- `AttemptDiagnostics`: `startedAt`, `finishedAt`, `reasoningEffort`,
  `dispatchObserved`, `stream`, `wait`. Derive elapsed milliseconds from the
  existing canonical duration for completed attempts; live snapshots provide
  `elapsedMs` for the current attempt. Dispatch means local request-header write,
  not proof of server receipt or billing. Preserve `providerUsed` separately.
- `StreamDiagnostics`: `receivedBytes`, `eventCount`, `outputBytes`,
  `outputCodePoints`, `firstEventMs`, `firstOutputMs`, `lastEventAt`,
  `lastOutputAt`, `terminalState`. Terminal state is `not_streaming`,
  `awaiting_terminal`, `completed`, `incomplete`, `failed`, or `missing`.
  Receipt counts cover the body, not HTTP headers; output counts cover text
  deltas, not reasoning/tool deltas. Unavailable measurements are null.
- `WaitDiagnostics`: `reason`, `plannedMs`, `actualMs`, `retryAfterMs`.
  Reason is the transient retry category or null when no wait occurred.
  Zero measured wait and unknown historical wait must remain distinguishable.
- New attempt ancestry fields use integer attempt numbers; no ancestry means
  null for scalar references and `[]` for a known-empty input list. Historical
  unknown input ancestry is null, not a falsely certified empty list.
- `RunProgressData`: `diagnostics` (the run snapshot above), `attempts`
  (completed canonical attempt summaries, at most ten), `currentAttempt`
  (number/stage/branch/target/reasoning/elapsed/stream/wait or null), and
  `accounting` (existing ledger/coverage projection, not estimated billing).

All timestamps are UTC RFC3339. Durations/counters are nonnegative integers
when known. For newly executed attempts, stage/branch/ancestry are known;
historical projections may use the explicit unknown values. Current run/attempt
records own these facts; adapters only serialize or derive presentation units.
Progress snapshots must exclude raw output even though the final successful
result naturally contains the requested output.

## 9. Live status, SSE, and client/test time budgets

### 9.1 Transport decision

Implement opt-in `Accept: text/event-stream` on `POST /api/v1/run`, using the same
JSON request body and authentication as the default JSON call. SSE means
Server-Sent Events: a streaming HTTP response containing named events. Use an
authenticated streaming HTTP client/fetch; browser `EventSource` does not fit
this authenticated POST contract and is not required.

Do not add polling in this phase. There is no detached live-job resource to poll.
The existing history API remains useful after a persisted completion/failure;
it is not an in-flight status store. A future durable/background run API would
require a separately approved lifecycle, idempotency, cancellation, and ownership
design. SSE here is request-bound, not resumable job execution.

All admission/auth/static validation errors before accepting a run retain normal
JSON HTTP error responses. After SSE headers are committed, report execution
failure as a terminal SSE event, not by attempting to change HTTP status. HTTP
200 or a heartbeat alone never means the generation succeeded.

### 9.2 Event contract

Define SSE envelope version 1, separate from result v4. Every data event includes
`schemaVersion`, increasing `sequence`, `runId`, `callId`, `traceId`, `type`, and
`data`. SSE `id` is the sequence for correlation only; it does not promise replay.
Generate IDs before the first accepted event, without duplicating execution.

| Event | Payload |
| --- | --- |
| `run.started` | IDs, origin, effective hard limit/deadline, configured max attempts |
| `run.progress` | Self-contained bounded snapshot: stage/target/reasoning, completed attempt summaries, current attempt, counters, waits, remaining budget |
| `run.completed` | The same canonical successful result v4 returned by JSON mode, including persistence/artifact states |
| `run.failed` | Existing structured error envelope plus safe final canonical diagnostics/result and persistence/artifact states |

For `run.completed`, `data` is the existing successful API envelope
`{state, result, error}` with result v4 and `error: null`. For `run.failed`, use
the same named envelope fields with a non-null existing API error and the final
safe result v4 when available. Unlike today's JSON error transport, this new
streaming failure can carry the final diagnostics; do not silently change JSON
error-envelope behavior. Error messages must not include failed raw output.

Exactly one terminal event when delivery remains possible. EOF without a terminal
event is a transport failure, not an empty success. A failed stream may still
have a persisted run retrievable by its ID; never automatically resubmit the POST.
No `Last-Event-ID` resume behavior in this version. Reject resume requests clearly
before execution rather than treating them as a new billable call.

Heartbeat comments at a fixed small interval may keep proxies informed. They
prove connection liveness only and never advance model-progress timestamps.
Coalesce ordinary output-counter snapshots to at most four per second; emit
stage/wait changes promptly. This is status, not raw token/output streaming.

### 9.3 Library and gateway concurrency

Expose a public typed `ProgressEvent` and optional caller-owned send-only progress
channel on the Go request. Runtime publishes with a nonblocking send and never
closes a caller-owned channel. Consumers must not close it while a call is active.
Progress is best effort: snapshots are cumulative, sequence gaps are legal,
and the returned canonical result is authoritative. An unread progress channel
must not slow or deadlock provider execution. No goroutine per delta/event.

Gateway streaming owns one bounded progress channel (32 snapshots is sufficient
for the initial implementation), one worker result channel, and one HTTP writer.
The worker calls the same run service once. The writer selects progress, heartbeat,
completion, and request cancellation. It serializes all response writes, drains
already-queued progress before the terminal event, and uses the terminal worker
result rather than relying on delivery of an optional progress snapshot.
The writer waits for the service's admitted/start signal before committing SSE
headers. A preflight error received first uses the existing JSON error path.
The library emits its initial snapshot after static admission and before its
first cache lookup, so a cache-only success also has proper start/terminal IDs.
Do not run admission/preparation a second time merely to choose the HTTP format.

Keep the HTTP request/disconnect context distinct from its hard-limited execution
child context. Reaching the execution deadline stops model work, but does not by
itself prevent the writer from delivering the worker's final failure during the
existing bounded persistence/write margin. A real disconnect cancels both paths.
Never detach execution from the original hard limit or extend the transport
margin to guarantee a terminal event after an arbitrarily slow persistence call.

Use the existing bounded HTTP write/deadline mechanisms. A blocked client cannot
create an unbounded queue or keep model work alive beyond the hard cap. On client
disconnect/write failure, cancel execution, close provider bodies, and let the
existing bounded `context.WithoutCancel` persistence path record what occurred.
Do not launch an unbounded background job. Test worker/writer cleanup and channel
ownership using local servers and explicit synchronization.

Preserve default JSON request behavior and the existing no-automatic-retry client
policy. SSE and JSON must share error classification, cost accounting, cache,
target selection, and final result construction. Verify flushing through the
configured proxy separately before claiming deployed progress works.

### 9.4 Incremental upstream provider SSE is mandatory

Adding gateway events around the current whole-body provider reader is not enough.
Refactor the provider parser so receipt is observable while the call is active.
Support existing protocol adapters, split chunks, CRLF, comments, multi-line data,
bounded event sizes, and UTF-8 boundaries according to their current contracts.

For Responses, consume the completed terminal response object as the authoritative
output; deltas are measurements, not a substitute final answer. Incomplete/failed
terminal responses remain errors. For other existing streaming protocols retain
their own completion markers. Stop reading and close the body once a sufficient
terminal event has been validated; a server holding the socket open must not
delay success until the request timeout. Non-SSE responses keep the bounded JSON
path. Malformed events and premature EOF are typed failures with partial evidence.

### 9.5 What a test may and may not extend

The API currently caps execution at 60,000 ms. `timeoutMs` can lower that cap,
not increase it; provider attempts and repair stages inherit the same parent.
The Go library inherits its caller's context. Do not add an extend-deadline API,
reset a context on a new stage, or alter the gateway/environment maximum here.

A caller may choose **before starting**:

- A soft expected duration for the case, for example 10 seconds.
- A larger already-approved hard observation/execution cap, for example 30 seconds.
- A suite-level hard cap, preventing many individually bounded cases from adding
  up to hours of unplanned observation.

These example numbers are not new repository timeout defaults. At the soft
threshold a client can continue collecting evidence inside the original hard
cap. Record the decision, recent useful progress, elapsed/remaining time, and
growth/wait state. If the test asserts a 10-second performance target, it stays
failed even if the result arrives at 15 seconds; observation does not turn it green.
Heartbeats alone, or continuously growing unfinished output, are not evidence
that the expected-duration assertion should be relaxed.

Choose this minimal initial implementation: a dependency-free Node reference
client and pure budget-decision helper under `scripts/`, with local-server tests.
It enforces case/suite hard caps and distinguishes functional completion from
performance overrun. No adaptive timeout framework, background daemon, or generic
cross-language SDK. Phoenix needs progress display, not autonomous deadline
extension. Document the same rules for Go/other REST clients.

Extend [the existing RCA template](../ker/timeouts/rca/TEMPLATE.md) documentation
with how to obtain evidence from a run, but do not loosen its required fields.
Capture phase-start proof, failed/comparable-success timings, p95/max, configured
timeout/headroom, and diagnosed cause. Unknown or differently configured samples
must not be mixed into a misleading p95. Diagnostics assist investigation; they
do not automatically authorize a baseline change.

## 10. Versioning, saved data, and compatibility

### 10.1 Coordinated versions

| Surface | Current | Proposed | Reason |
| --- | --- | --- | --- |
| Profile/client-state/profile-bundle documents | 2 | 3 | Complete recovery policy shape changes |
| Canonical REST/live/history/trace result | 3 | 4 | Stage/generation/progress diagnostics |
| Stored cache record | 2 | 3 | Separate generation target and producer |
| Structured response projection | 3 | 4 | Invalidate incompatible cached projections semantically |
| Trace artifact schema | `harden-llm.trace.v2` | `harden-llm.trace.v3` | New stage/input ancestry and origin fields |
| Stats response | 2 | 2 | Existing aggregates and ledger meaning remain unchanged |
| Outer cache namespace | `operation-v2` | `operation-v2` | No unrelated namespace migration |
| SSE event envelope | absent | 1 | New opt-in response representation |

Strict current writers use current versions and current structured readers reject
partial or mixed policies. A bounded compatibility reader accepts the complete
legacy v2 policy only at the Go/Phoenix boundary while old callers are retired;
it never combines legacy and structured fields, and no v3 writer emits the old
boolean. The migration maps mutable stored rows before they re-enter the current
writer path. Update owned callers and examples together. Original stored
requests remain historical evidence, not automatically executable new requests.

Historical artifact downloads retain their original bytes/schema. If an artifact
viewer interprets them, route by its explicit artifact schema; this is read-only
historical rendering, not a second current-run result decoder. Normal live/history
result decoding stays unified on result v4 after migration.

### 10.2 Ordinary transactional migration

Use the existing migration transaction/lock/version system. At this baseline the
next file is `internal/postgres/migrations/0008_recovery_stages.sql`; recheck before
creating it. Do not edit applied migrations or add a conversion service/CLI.

For existing mutable v2 policies:

1. Preserve `maxAttempts`, `retryOn`, and `backoff` exactly.
2. Map `repairInvalidOutput: false` to `jsonRepair: null`, `rerun: null`.
3. Map `repairInvalidOutput: true` to initial and escalation targets both
   `{"source":"generation"}`, with `rerun: null`.
4. Remove the retired boolean and write document v3.
5. Do not silently opt existing callers into CPA, Astra, or extra attempts.

This intentionally narrows old repeated same-model semantic repair to at most
two repair stages. A legacy four-attempt run formerly could make three semantic
repairs; the new finite contract does not preserve that extra repair. Call this
out in release notes and migration preview evidence. Existing budgets can still
be consumed by transport retries. Operators explicitly select the new full CPA
preset when they want the six-stage behavior. Do not keep a hidden legacy loop.

For historical result v3, preserve output, selected target, producer, attempts,
ledgers, cache facts, and existing timings. Add unknown new measurements as null
or explicitly unavailable; do not infer exact stages/ancestry/actual wait from a
legacy repair boolean. Preserve immutable request/trace/artifact bindings,
credential ciphertext, owner IDs, and unrelated records. Update the result
schema constraint consistently with the existing canonical-history prerequisites.

Migration fails atomically on malformed/unexpected source shapes, with row
identity/field diagnostics and no secret values. Add pure transformation fixtures
and a real PostgreSQL transaction/rollback test. Do not claim rollback safety from
SQL string inspection alone. Coordinate old-writer shutdown, backups, config
conversion, matching API/frontend deployment, and rollback before real cutover.
An image rollback alone cannot revert a migrated database or synchronized config.

## 11. Reusable UI and REST parity

Use the existing `ProfileWidgetState`, `ProfileWidgetComponent`, host event routing,
`llm_result_components.ex`, and `llm_trace_components.ex`. Extend them; do not clone
the picker for initial repair, escalation, and rerun.

Add an explicit target-only widget mode. It accepts a target draft and host-owned
catalog, renders the existing model/reasoning/native-option controls, and emits
namespaced target edits. It must not expose profile recovery controls recursively.
Search/cache ownership remains at the generation call, not independent repair
controls. The original full widget retains its current settings/cache/search UI.

Extract one shared repair-plan editor used twice:

```text
Recovery
  Global attempts / transient categories / backoff
  Original response JSON repair [enabled]
    Initial repair target     [profile widget, target-only]
    Escalated repair target   [profile widget, target-only] [enabled]
  Fresh rerun [enabled]
    Generation target         [profile widget, target-only]
    Rerun response JSON repair [enabled]
      Initial repair target   [same editor and target-only widget]
      Escalated repair target [same editor and target-only widget] [enabled]
```

Host paths/IDs include instance and role, for example
`left.recovery.rerun.jsonRepair.initial`. Two embedded widgets and all their
children must remain independent. Label generation-relative migrated targets
as “Use this branch's generation model”; do not overwrite them with new defaults
on mount, save, refresh, or round-trip serialization.

Update strict frontend wire decoding before making new responses visible.
Workspace and embedding hosts pass policy/progress/results; components do not
call providers or own credentials, fetch history independently, or guess defaults.
Preserve the existing one-in-flight/latest-pending state writer so rapid changes
cannot persist older policy over newer edits.

Keep Phoenix API authentication server-side. Add a server-side streaming client
adapter to `HardenAPI`; do not expose vault bearer tokens to JavaScript. The host
owns task cancellation, reduces progress into a pure display state, and passes
it to transport-free components. A final result replaces progress. Show stage,
target/reasoning, elapsed/remaining budget, attempts, last output activity,
received counters with units, and terminal error/stop reason. History/trace use
the same final record, including actual repair producer and generation identity.

No new browser/DOM-emulator work is necessary for these state/serialization
invariants. Use LiveViewTest, private Req/local-stream ownership, the embedding
harness, and plain Node for client decisions. Do not claim pixel/layout validation.

## 12. Implementation sequence for a smaller model

### 12.1 Working method and dependency order

Implement one checkpoint at a time. For each checkpoint:

1. Read its owning files and associated current tests; inspect current diff.
2. Add the specified deterministic regression before implementation. Record a
   failure showing the missing behavior, not an unrelated compile/setup error.
3. Implement only that checkpoint, keeping one owner per rule.
4. Run focused tests, then the broad `make test-fast` gate when code is runnable.
5. Review the diff for duplicate policy logic, option leakage, sensitive data,
   changed test oracles, and altered budgets.
6. Record changed files, exact commands/results, and unresolved prerequisites.
   Do not mark the next checkpoint complete because an interface stub compiles.

Dependency order:

```text
P01 contracts/fixtures
  -> P02 policy and target resolution
  -> P03 bounded stage execution and prompts
  -> P04 cache/accounting integrity
  -> P05 canonical diagnostics and incremental provider parsing
  -> P06 persistence/schema migration
  -> P07 REST SSE and reference client
  -> P08 reusable UI and strict REST parity
  -> P09 end-to-end deterministic certification and handoff
```

These are checkpoints on one development line, not independently deployable
schema combinations. Do not deploy intermediate mixed versions. Runtime and UI
contract fixture updates may be staged together when strict decoders require it,
but do not mark consumers verified until their real paths pass.

### 12.2 P01 — contract freeze and red fixtures

Primary files:

- `api/openapi.yaml`.
- New `docs/adr/ADR-HLLM-024-bounded-recovery-and-progress.md` and ADR index.
- Canonical specifications and `docs/requirements-traceability.md`.
- Existing policy/run/cache/trace/frontend contract tests; add small fixture files
  in their current fixture locations rather than a new fixture framework.

Tasks:

1. Register REQ-301..316, TEST-236..259, WEB-TEST-083..089 after collision check.
2. Specify complete policy/target union, nullable semantics, result v4 fields,
   profile/state/bundle v3, `origin`, and the SSE content type/event envelopes.
3. Document the specific ADR020 restriction superseded and the retained ADR021/022
   invariants. Record deliberate compatibility/migration and default-budget changes.
4. Add exact JSON fixtures for the full preset, disabled repair/rerun, same-generation
   repair, invalid recursive target, six-stage success/failure, and partial failure.
5. Add tests proving the new policy is accepted, partial/mixed/unknown-key
   policies are rejected, and a complete legacy document is accepted only by the
   bounded compatibility path. Add current strict frontend decoder fixtures
   before UI rendering edits.

Acceptance: the implementation target is unambiguous; all examples agree on
CPA profiles, portable reasoning, schema versions, field types, and stage IDs.
Do not claim that changing OpenAPI alone implements the endpoint.

### 12.3 P02 — policy/defaults and leaf target resolution

Primary files:

- `internal/retry/retry.go`, its tests, root `types.go` and `client.go`.
- `internal/profiles/profiles.go`, catalog/default tests, profile fixtures.
- `internal/gateway/profile_resources.go`, `resources.go`, `runtime_profiles.go`.
- `internal/gateway/shared_profiles.go`, `scripts/shared-profiles.mjs`, and their tests.

Tasks:

1. Add the types/presence-aware strict decoding from Section 5. Preserve explicit
   zero backoff, empty retry list, and null-disabled stages.
2. Add the single backend default preset. Keep existing profile values separate
   from new defaults; update profile/default response contract.
3. Resolve enabled targets against one owner-scoped catalog snapshot. Ensure
   modelId and providerOptions overrides apply only to the specified leaf.
4. Validate all targets without dispatch/DNS. Reuse existing credential and
   endpoint owners; error fields identify the role that failed.
5. Reject policy/search/cache fields inside leaf native options. Keep runtime
   profiles free of recursively executable policies.
6. Update shared-config/bundle schema readers and tests. Record Astra metadata
   as unresolved if unavailable; do not fabricate a usable production catalog row.

Tests: TEST-236, TEST-237, TEST-238. Use synthetic profiles A/B/C with distinct
credentials/options/reasoning maps to detect settings leaked from the original.

Acceptance: frontend and headless consumers can obtain/serialize the same
complete policy; profile cycles cannot recurse; invalid configuration dispatches
zero requests. Implementation can proceed with synthetic Astra fixtures.

### 12.4 P03 — stages, repair input, and transport retry preservation

Primary files:

- `internal/runtime/execute.go`, `types.go`, `repair.go`.
- New small `internal/runtime/recovery.go` and `recovery_test.go`.
- `internal/providers/payload.go`, `web_search.go`, and their existing tests.

Tasks:

1. Add typed stage/branch constants and a table-tested transition function.
2. Carry stage/leaf target/prepared work/history/generation operation together;
   replace fixed-target assumptions without introducing a nested executor loop.
3. Implement exact prompt histories and fresh-rerun input reset. Separate complete
   invalid response evidence from envelope/transport/partial-output errors.
4. Preserve prepared repair work through any transient repeats. An attempted
   repair that times out locally must retry the repair, not original generation.
5. Disable repair search/tool dispatch and preserve generation evidence/results.
6. Check global attempt/deadline bounds before every call; apply wait only for
   transient repeats and record actual interruption of a wait.
7. Keep the last real provider/validation error plus an explicit exhaustion reason.

Tests: TEST-239..243. Use deterministic executor scripts and injected clocks/
waiters where available; do not use real provider calls or multi-second sleeps.

Acceptance: scripts can prove all six stages, every early success, disabled
branches, maxAttempts 1/4/6/10, and a transient failure at each stage with no
target/history reset. First-attempt success still makes exactly one call.

### 12.5 P04 — generation cache identity and accounting

Primary files:

- `internal/runtime/cache.go`, `execute.go`, accounting tests.
- Root `cache.go`, `client_cache.go`, and public cache adapter tests.
- `internal/cachekey/`, provider projection construction, `internal/postgres/cache.go`.

Tasks:

1. Add generation identity/completedBy to cached projection; retain actual producer.
2. Prepare/cache-check rerun generation only upon entering that branch.
3. Write repaired successes to their generation key; never cache repair operations.
4. Bump cache record/response projection versions consistently across adapters.
5. Preserve strict replay admission, lossless numbers, EOF checking, owner scope,
   and cache-write-failure success semantics.
6. Assert current provider ledger and result ledger independently for repaired
   output and rerun cache hits after expensive failed original attempts.

Tests: TEST-244, TEST-245. Extend existing cache-integrity regressions instead
of replacing them with a permissive producer comparison.

Acceptance: keys are invariant under repair-policy edits; original/rerun keys
remain distinct when semantic generation differs; wrong generation identity
still fails; no old cache purge or extra SQL sidecar is introduced.

### 12.6 P05 — diagnostics and true incremental provider streams

Primary files:

- Root `types.go`, `client.go`; `internal/runtime/types.go`, `telemetry.go`.
- `internal/providers/router.go` and protocol stream tests; optionally extract
  its incremental SSE reader into a focused `internal/providers/sse.go`.
- `internal/traces/traces.go` and artifact/trace tests.

Tasks:

1. Implement the canonical diagnostics and public nonblocking progress channel.
2. Add event/counter callbacks inside the provider boundary; callbacks publish
   measurements only and cannot decide retry/escalation.
3. Replace whole-buffer SSE processing with bounded incremental parsing. Preserve
   current successful-output extraction and protocol-specific terminal contracts.
4. Retain partial result/accounting evidence when reading fails. Close provider
   bodies on completion, limit, cancellation, and parse failure.
5. Emit self-contained bounded progress snapshots with monotonically increasing
   sequence and no prompt/output text. Final records remain authoritative.
6. Add trace artifact input/output/parent references without credential leakage.
7. Enrich existing spans/metrics from the same measurements; no telemetry reads
   in the execution control path and no high-cardinality metric dimensions.

Tests: TEST-246..249. The key local-server test sends a terminal response and
keeps its socket open; the call must finish without EOF. Another server sends
continuous deltas without terminal completion; it must stop at its original
bound, preserve growth counters, and return no accepted output.

Acceptance: observing no progress cannot block a call; streaming parsing has
bounded memory; protocol validity/usage oracles from existing tests are unchanged.

### 12.7 P06 — canonical persistence and ordinary migration

Primary files:

- `internal/gateway/run_service.go`, run output/history/trace handlers and tests.
- PostgreSQL canonical run serialization and new `0008_recovery_stages.sql`.
- `internal/gateway/telemetry.go`, `telemetry_runtime.go` as needed for origin.
- Existing artifact lifecycle and integration fixtures.

Tasks:

1. Project result v4 once from canonical runtime data, including failures. Keep
   normal history/detail/trace readers consistent and stats response v2 unchanged.
2. Validate/bind request origin without letting it alter authentication or owned IDs.
3. Add the bounded actual-wait and execution-deadline fields; preserve planned
   historical wait and distinguish persistence timing.
4. Implement the exact mutable/history migration from Section 10, updating
   schema-version constraints and bundle/shared-config fixtures consistently.
5. Preserve the existing single SaveExecution aggregate and immutable Garage
   artifact recovery flow; do not persist every progress event.
6. Produce redacted migration preview fixtures showing old enabled/disabled
   policies and the explicitly narrowed old same-model repair sequence.

Tests: TEST-250, TEST-251. Pure mapping/serialization belongs in T0/T1; transaction,
owner isolation, artifact references, and rollback proof belong in T3.

Acceptance: current JSON run, history, and trace return one strict result schema;
unknown historical facts remain unknown; all-or-nothing migration and preserved
credential/owner/accounting evidence are tested with real PostgreSQL when available.

### 12.8 P07 — REST streaming and bounded reference client

Primary files:

- `internal/gateway/httpapi/resources.go`, `routes.go`; new focused SSE writer file.
- `internal/gateway/run_service.go`, `api/openapi.yaml`.
- `cmd/harden-llm-gateway/server.go` only if existing write/flush integration needs
  adaptation; do not raise its timeout constants.
- New `scripts/run-progress.mjs`, `scripts/run-progress-core.mjs`, and
  `scripts/test/run_progress_test.mjs`.
- `test/test-tiers.json`, existing test runner registration, `docs/api-and-library.md`.

Tasks:

1. Negotiate JSON vs SSE with the same admitted input and exactly one service call.
2. Generate/bind IDs early and serialize run.started, bounded progress, and terminal
   envelopes. Avoid half-started SSE for normal admission errors.
3. Handle disconnect, slow writer, failed final persistence, deadline, and partial
   data without HTTP-status rewriting or background re-execution.
4. Keep normal JSON response/error transport unchanged apart from explicit versioned
   data changes. Test JSON and SSE against the same scripted provider outcomes.
5. Implement the Node reference parser/client and pure soft/hard/suite budget
   decision helper. Consume credentials from environment or existing secure
   injection, never command-line flags/logs; sanitize stored failure evidence.
6. Print compact run/trace IDs, phase/target, elapsed/remaining time, attempts,
   growth/last-activity, terminal outcome, and a machine-readable redacted report.
   Exit nonzero on functional failure, missing terminal, or asserted soft-budget
   overrun; continuing observation never retries the run.
7. Document how other REST clients send the same body/settings and interpret
   terminal status. Inspect proxy flushing configuration; certify actual proxy
   delivery only with an appropriate local integration boundary.

Tests: TEST-252..257. Register new dependency-free Node tests in the existing
fast task list; do not create a second test scheduler or increase task timeouts.

Acceptance: one POST yields one execution; callers see progress before completion;
EOF/heartbeat cannot masquerade as success; unread/slow streams stay bounded;
test observation cannot extend server, case, or suite hard caps.

### 12.9 P08 — shared widget and diagnostics consumers

Primary files:

- `frontend/lib/harden_llm_web/profile_widget_state.ex`, `profile_defaults.ex`.
- `frontend/lib/harden_llm_web/live/profile_widget_component.ex`.
- Workspace, profile-editor, and embedding LiveViews and their tests.
- `frontend/lib/harden_llm_web/harden_api.ex`.
- `frontend/lib/harden_llm/llm_diagnostics_wire.ex`.
- Existing result/trace components; pure JS client core only for existing hook
  behavior genuinely requiring it, not to move authenticated API calls to browsers.

Tasks:

1. Finish strict v3/v4 policy/result and v1 progress decoders; reject malformed
   required fields before state/rendering decisions.
2. Add target-only mode and one shared repair-plan editor with namespaced events.
3. Preserve backend defaults and explicit disabled/null choices through draft,
   save/reload, import/export, generated cURL, and run payloads.
4. Add the server-side streaming adapter and host-owned cancellation/progress state.
   Do not retry a streaming POST after an error or reconnect.
5. Render truthful generation vs repair producer, timing units, cache branches,
   missing/unknown usage, and phase ancestry in existing stats/output/history/trace
   components. Do not add a new analytics dashboard.
6. Prove two embedding instances with all nested roles remain independent and
   rapid edits retain the existing ordered state-writer guarantees.

Tests: WEB-TEST-083..089. Run targeted Mix tests then `make test-fast` with the
pinned toolchain. No browser or jsdom/Happy DOM installation.

Acceptance: a standalone REST request and a UI-generated request are equivalent;
reuse is demonstrated in both existing hosts; no recursive picker, copied recovery
logic, client-side bearer exposure, or falsely reported browser layout check.

### 12.10 P09 — certification and implementation handoff

Tasks:

1. Run the full scripted outcomes matrix below through the lowest sufficient tier.
2. Run `make test-fast` and focused race checks for the modified stream/channel
   code. Run T3 integration for actual migration/cache/artifact boundaries when
   Docker is available; report a blocker if it is not.
3. Verify no production/test baseline timeout or retry assertion changed merely
   to accommodate new failures. Keep `TEST-039` active.
4. Update canonical specs, ADR status, traceability, API examples, and operator
   migration/release notes to match implemented behavior, not aspirations.
5. Search for obsolete `repairInvalidOutput`, fixed-target assumptions, old
   schema constants, whole-body SSE processing, recursive recovery controls, and
   duplicated defaults. Historical migrations/docs may retain old names explicitly.
6. Document exact local SHA, focused/full commands, pass/fail counts, skipped
   external boundaries, Astra profile availability, and remaining cutover needs.

Tests: TEST-258, TEST-259 plus all existing relevant regressions. Do not run a
full release, production deployment, browser, or public provider smoke merely
because deterministic implementation is complete.

## 13. Canonical regression matrix

IDs below are reserved for implementation, not claims of existing passing tests.
Add canonical spec comments to each new Go/Node test file. Put subcases under
the listed ID rather than allocating an ID to each tiny permutation.

| ID | Tier | Concrete oracle |
| --- | --- | --- |
| TEST-236 | T0 | Full/disabled/generation-relative policies decode; absent nullable keys, unknown keys, recursive targets, old boolean, and invalid bounds fail with exact paths |
| TEST-237 | T0/T1 | Owner-scoped target resolution; missing/unauthorized relevant targets fail before dispatch; text/disabled branches do not resolve unused repair profiles; leaf policies and profile cycles never execute; no DNS in Prepare |
| TEST-238 | T0 | One backend preset: Luna L/H, Astra L generation and L/H repair; explicit four-attempt stored policy stays four; target-native options/reasoning do not leak |
| TEST-239 | T1 | Six scripted invalid/valid stages run in exact order; each early success stops; no second rerun or generation escalation |
| TEST-240 | T0/T1 | Each repair input contains exactly required branch history, including failed initial repair; original input/schema once; rerun input omits original failed answers; history-limit failure explicit; repairs perform no search and preserve generation evidence; rerun retains original search setting/memoization |
| TEST-241 | T1 | Transient failure injected at each stage repeats byte-equivalent prepared semantic work on same target with same input history; no new semantic-history entry |
| TEST-242 | T0/T1 | maxAttempts 1/4/6/10, disabled stages, cancellation during wait, exhausted budget/deadline, Retry-After, actual vs planned wait; no dispatch after parent end |
| TEST-243 | T1 | Only completed strict syntax/schema failure advances; malformed envelope, refusal, incomplete stream, output limit, auth, and parent cancellation do not; attempt-local timeout remains network |
| TEST-244 | T0/T1 | Original/repaired replay and rerun cache lookup/write use generation keys; recovery/origin edits do not affect key; no repair cache or original-key backfill; bad generation integrity rejected |
| TEST-245 | T1 | Repair target pricing/usage exact; failed original calls plus rerun cache hit retain provider ledger; unknown dispatched usage remains unknown; cache write failure preserves success |
| TEST-246 | T1 | Progress counters/timestamps/stage/IDs monotonic and typed; unread channel cannot block; no per-event goroutine or unbounded queue; final canonical result complete despite dropped snapshots |
| TEST-247 | T1 | Split/CRLF/multiline SSE and UTF-8; terminal before EOF succeeds; premature EOF/malformed/incomplete fails; usage precedes validation; existing protocol-specific oracles unchanged |
| TEST-248 | T1 | Never-ending deltas hit original byte/deadline bound, retain received counters/known accounting, no accepted partial output; heartbeat/no-output and output growth distinguished |
| TEST-249 | T0/T1 | Trace ancestry links complete failure/repair history and actual targets; redaction excludes secrets; current artifact schema explicit and old artifact bytes unchanged |
| TEST-250 | T0/T1 | JSON result/history/trace v4 identity; planned vs actual waits; origin round-trip/limits; current stats v2 still accurate; unknown historical values not invented |
| TEST-251 | T3 | Real transactional v2/v3 document/result migration; rollback on malformed row; owner/credential/request/accounting/artifact preservation; no copied cache sidecars |
| TEST-252 | T1 | Same scripted run via JSON/SSE has equivalent canonical result/ledgers and exactly one service/provider execution; progress is flushed before terminal |
| TEST-253 | T1 | Auth/validation errors before SSE keep HTTP JSON envelope; execution/persistence errors after headers use run.failed; exactly one terminal if deliverable; EOF is not success |
| TEST-254 | T1/race | Disconnect/slow-reader/backpressure stop provider work by original bound and record failure; no response write race, closed-channel panic, duplicate terminal, or leaked worker |
| TEST-255 | T0/T1 | Origin/traceparent cannot spoof owner/IDs; unauthorized history/trace stays denied; raw prompts/outputs/keys absent from progress/logs/metric labels |
| TEST-256 | T2 | Node parser handles split events and incomplete EOF; budget decisions preserve soft failure, enforce case/suite hard cap, ignore heartbeat as progress, never resubmit POST |
| TEST-257 | T1/T2 | Headless REST reference client works with local scripted gateway; redacted machine report has IDs, last stage, timings, units, stop reason; no Phoenix/provider key needed |
| TEST-258 | T1 | Full cross-boundary local-provider matrix original success/repair success/escalated success/rerun success/rerun repair success/exhaustion/cache/error; count exact dispatches |
| TEST-259 | T0/T1 | Timeout baseline/TEST-039 and fast-tier registration unchanged; defaults/OpenAPI/examples agree; no second recovery loop or model-specific frontend preset |

| Frontend ID | Tier | Concrete oracle |
| --- | --- | --- |
| WEB-TEST-083 | T0/T1 | Complete policy draft/serialize round-trip; null and zero retained; backend preset only; partial/unknown/mixed wire shapes rejected and complete legacy shape remains compatibility-only |
| WEB-TEST-084 | T1 | Target-only mode reuses picker without recursive recovery/search/cache controls; original full widget retains existing controls |
| WEB-TEST-085 | T1 | Original/rerun repair editors use same component/state; changing one role cannot mutate another; generation and initial repair remain separate |
| WEB-TEST-086 | T1 | Two embedded hosts and nested IDs independent; saved profile/run/cURL/import-export settings equal; latest pending save wins |
| WEB-TEST-087 | T1 | Strict result/progress decoder and reducer handle known/unknown fields, sequence gaps, terminal failure, cache hit, and partial accounting without fabricated zero |
| WEB-TEST-088 | T1 | HardenAPI server-side stream keeps credentials private, handles split events/cancellation/EOF, makes one POST, and never retries automatically |
| WEB-TEST-089 | T1 | Workspace/result/history/trace show stage, origin, generation vs producer, units, and stopped reason consistently; final record replaces progress |

### 13.1 Mandatory scripted end-to-end cases

For each script assert requests, resolved targets/options, repair input content,
stage order, attempt numbers, result producer, generation identity, ledgers,
cache operations, and terminal stop reason. Use symbolic outputs with one precise
schema error, not large real model transcripts.

```text
A: valid original                                      -> 1 call
B: invalid original; valid initial repair               -> 2 calls
C: invalid original; invalid initial; valid escalation   -> 3 calls
D: original branch all invalid; valid fresh rerun        -> 4 calls
E: original branch invalid; rerun invalid; repair valid   -> 5 calls
F: only rerun repair escalation valid                    -> 6 calls
G: all six invalid                                      -> 6 calls, failed
H: invalid original; repair transient; repair valid       -> 3 calls, same repair work repeated
I: original cache hit                                   -> 0 calls/searches
J: original branch invalid; rerun cache hit              -> 3 calls, their costs retained
K: maxAttempts=4, all completed outputs invalid          -> 4 calls, no hidden repair allowance
L: active stream never completes                        -> stop at existing cap, partial diagnostics only
M: repair/rerun disabled, invalid original               -> 1 call, failed
N: original repairs disabled, rerun valid                -> 2 calls
O: terminal completed SSE, server does not close         -> success before EOF
```

For H, repeat the transient-injection subcase at every semantic stage. For J,
also cover original transport retries before reaching the rerun cache. Refusal,
local timeout, parent timeout, malformed envelope, output cap, and cancellation
need separate typed tests; do not fold them into a generic “invalid JSON” fixture.

## 14. Verification commands and evidence boundaries

Use the pinned toolchain for every frontend/fast command on the reference host:

```bash
export PATH=/home/kirill/.local/elixir-1.20.2/bin:/home/kirill/.local/otp-28.4.3/bin:$PATH
make test-fast
```

Examples of focused backend commands (choose the packages touched):

```bash
go test ./internal/retry ./internal/runtime ./internal/providers ./internal/profiles -count=1
go test ./... -run 'TestRecovery|TestProgress|TestStream' -count=1
go test -race ./internal/runtime ./internal/providers ./internal/gateway/... -run 'TestProgress|TestStream' -count=1
make test-api
git diff HEAD --check
```

Do not treat a regex selecting zero tests as a pass. Choose actual test names
from the new tests and retain `make test-fast` as the comprehensive cheap gate.
The `./...` focused example includes root adapter tests. Node reference tests run
through their registered fast task and may also run with `node --test` directly.

From `frontend/`, use the exact affected files under `mix test`, then its complete
deterministic suite as required by `make test-fast`. Use `mix format --check-formatted`
and Go formatting gates for changed source. No browser flags.

Use `make test-integration` for real PostgreSQL/Garage migration and lifecycle
claims. A local proxy flushing check belongs to a justified T3/T5 boundary; it
does not authorize a browser or paid provider. Full `make test-release` is for
an explicitly requested release/cross-system certification, not every checkpoint.

Documentation-only creation of this plan needs link/structure/whitespace review,
not application builds or claims that the future test matrix passes.

## 15. Stop conditions, rollout, and completion checklist

### 15.1 Stop and report, do not guess

- Unknown CPA Astra profile capabilities: continue deterministic implementation
  with synthetic profiles; block enabling the real preset until approved metadata
  exists. Do not claim live Astra compatibility.
- Persisted policy shape not covered by migration: stop the affected cutover and
  report row identity/field without secrets; do not reset all user preferences.
- Actual caller requires more than 60 seconds: collect RCA evidence and request
  a separate coordinated timeout-policy decision. Do not raise a test timeout.
- Missing Docker or provider credentials: report the corresponding unverified
  boundary; do not label local substitutes as live integration/provider proof.
- Unclear product semantic error after valid schema: keep existing validators;
  a new semantic critic/validation project is not authorized by this plan.

### 15.2 Release preparation, only when separately authorized

Inventory owned REST/Go callers and shared profile configuration. Back up and
preview the ordinary migration. Stop old writable versions, update complete
configuration/documents, migrate, and start matching backend/frontend versions.
Keep owner/session/provider credentials isolated under existing deployment policy.
Use trusted `sync-profiles` for approved configuration, not interactive profile-save
probes. Have a database/config-compatible rollback, not just an old image tag.

For a requested deployment, report branch, source SHA, component image identities,
environment URL, HTTP/auth/health checks, proxy streaming evidence, and all unrun
browser/provider boundaries. No automatic paid-provider smoke test.

### 15.3 Verification record (2026-09-18)

The implementation was verified in this checkout with the pinned Elixir/OTP
toolchain. The deterministic and local integration evidence is:

- `make test-fast` — accepted, all 9 registered tasks.
- `go test ./... -count=1` — passed.
- `go test -race -p=1 ./... -count=1` — passed.
- `make test-integration` and `make test-integration-race` — accepted with
  local PostgreSQL/Garage; the migration rollback and mixed-policy cases passed.
- `make verify` — exit status 0: vet/build, Loki/static checks, 33 parity
  fixtures, 26 Node checks, unit/integration/race gates, and govulncheck (no
  called vulnerabilities; three required-module vulnerabilities were not called).
- `cd frontend && mix format --check-formatted && mix test` — 228 passed,
  5 excluded by the repository's browser/compose/deployed policy.
- `node --test scripts/test/run_progress_test.mjs` — 4 passed.
- `gofmt` and `git diff --check` — passed.

No browser, deployment, proxy-flushing, paid-provider, or real CPA Astra
certification was run. The Astra catalog/configuration prerequisite remains an
explicit release blocker for enabling the six-stage default operationally.
The pre-existing `frontend/test/browser/deployed_canary_test.exs` working-tree
edit was preserved and is not part of this implementation.

### 15.3.1 Astra prerequisite closeout (2026-09-19)

The historical verification above intentionally recorded the unverified
boundary at that time. The prerequisite was subsequently closed through the
approved external managed catalog, without changing the credential-free
embedded seed:

- CPA upstream `main` contains the Astra support commits
  `f375487d29a06bd4cb0ad204cc19dbcf6e7dfb6d` and
  `f447bf5cba7aa28f6a242284d166b338e37a4d47`; the running CPA image is
  `sha256:99bedd436cf04530451aeff67b88d3e76dfff2f2c48691dbf68d07e0c27c7288`.
- Trusted synchronization and authenticated readback passed for both accounts:
  32 managed profiles, 22 configured bindings, and the `CPA GPT-6 Astra`
  profile with lowest/middle/highest reasoning options.
- The deployed browser canary passed the Astra picker/reasoning assertions,
  release identity and health/login probes, bounded Luna web-search smoke, and
  history cleanup.
- One bounded live Astra structured request passed through the production REST
  API (HTTP 200, provider invoked, exact profile accounting, trace
  request/response resources available, and cleanup completed).

The application image was not rebuilt for this configuration-only closeout.
The external managed catalog remains required for a fresh environment; the
embedded 28-profile catalog is deliberately unchanged and remains
credential-free.

### 15.4 Definition of done

- [x] P01 contracts and ADR recorded; strict examples and canonical IDs registered.
- [x] P02 complete policy/defaults/leaf resolution implemented with no recursive recovery.
- [x] P03 all six semantic stages and exact repair histories implemented in one loop.
- [x] P04 original/rerun cache identities and actual-producer accounting verified.
- [x] P05 incremental provider parsing, bounded progress, diagnostics, and ancestry verified.
- [x] P06 current result/history schema and transactional migration verified.
- [x] P07 REST JSON/SSE parity and reference client's case/suite caps verified.
- [x] P08 existing UI hosts reuse picker/repair/results without policy duplication.
- [x] P09 relevant regression matrix, fast gate, and required real integration evidence recorded.
- [x] No timeout baseline increase, skipped required assertion, or unreported failed evidence.
- [x] Approved CPA Astra profile exists before the full default preset is enabled operationally (closed by external managed-profile synchronization and live/browser evidence on 2026-09-19; the embedded 28-profile seed remains credential-free and unchanged).
- [x] No deployment, visual layout, or paid-provider success claimed without corresponding evidence.

### 15.5 Copyable implementation handoff

```text
Implement plans/rest-recovery-and-progress-implementation-plan.md in this repository.
Read AGENTS.md and the full testing guide first. Work through P01-P09 in order,
one checkpoint at a time. Preserve unrelated edits. Add deterministic failing
regressions before implementation and run the lowest sufficient tier plus
make test-fast. Keep the single runtime loop, one global attempt budget/deadline,
strict JSON/schema admission, current accounting integrity, and generation-owned
cache identity. Do not implement recursive recovery, guess Astra capabilities,
increase timeouts, call paid providers, run browsers, deploy, or modify sibling
repositories. Record exact evidence and remaining prerequisites after each phase.
Do not mark a checkpoint complete if its asserted boundary was not actually tested.
```
