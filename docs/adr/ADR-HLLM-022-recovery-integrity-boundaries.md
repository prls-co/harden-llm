# ADR-HLLM-022: Recovery Integrity Boundaries

- Status: Accepted and implemented.
- Date: 2026-09-15.
- Requirements: REQ-213 through REQ-224 in `plans/retries-repair-boundary-consolidation-plan.md`.
- Verification: TEST-223 through TEST-228 and WEB-TEST-076 in the canonical test specifications.
- Related: ADR-HLLM-018, ADR-HLLM-019, ADR-HLLM-020 and ADR-HLLM-021.

## 1. Context

ADR-HLLM-021 consolidated provider, runtime, accounting, cache and frontend
ownership, but the follow-up review found two reproduced boundary defects and
several untested consequences. An attempt deadline returned by the Jina search
path could be mistaken for the logical call deadline. A dispatched provider
failure with unavailable accounting was skipped by runtime, so later measured
work made the aggregate look complete. The review also traced cache replay that
skipped output/schema admission, lossy JSON number decoding, unchecked cached
producer attribution, duplicated cache columns/envelopes, and a cache-write
error that changed an already accepted inference into a failed call.

These are local source and test findings. Request dispatch remains local evidence
and does not prove remote execution or billing. No provider fallback, exactly-once
subsystem, new service, legacy reader or deployment policy is implied.

## 2. Decision

Keep the current one-library/one-gateway architecture and correct each rule at
the existing owner:

- The guarded transport owns endpoint policy, dispatch observation, bounded
  reads, HTTP status and `Retry-After`. Router and Jina use one transport
  classifier. The original call parent owns cancellation/deadline precedence;
  an attempt-local or Jina-local timeout is network while that parent remains
  active.
- Provider normalization extracts usage/cost before completion/output failures
  return. Complete facts from a rejected response remain observable; truncated
  JSON is not guessed. Invalid accounting is terminal and independently valid
  dimensions remain available.
- `internal/accounting` owns one per-call provider accumulator. Runtime supplies
  dispatch plus the normalized ledger for every attempt, including failed
  attempts. Unavailable usage preserves incomplete coverage; unavailable cost
  contributes one unknown observation for observed model work. The accepted
  result ledger remains separate from cumulative provider totals.
- One runtime result-admission function validates fresh and replayed output,
  accounting and canonical search metadata while the provider normalizer keeps
  protocol completion. Structured repair remains eligible only for invalid
  output from an otherwise completed fresh response.
- Cache replay decodes the existing response projection with `json.Decoder`
  `UseNumber`, requires one complete JSON value and compares stored producer
  provider/protocol/endpoint/model to the prepared target. Profile ID remains
  attribution and may differ for an equivalent semantic target. Existing
  response projection `v3`, operation namespace `v2`, cache-record schema `2`
  and operation hashes do not change.
- The cache stores one accepted result projection plus owner/key/version and
  timestamps. The forward migration removes the unused operation, raw envelope
  and duplicate usage/cost columns while preserving rows, keys and result JSON.
  Historical migrations stay immutable; no compatibility reader, purge or
  historical output transformation is added.
- After accepted output is assigned, a cache `Set` error is best effort: the
  call succeeds with `Cache.Status = "write_failed"` and `Written = false`,
  while output, result/provider ledgers and result source remain intact. Cache
  lookup/integrity errors and failures before admission remain terminal. API,
  history, traces, bounded telemetry and Phoenix use the same status.

No new public setting, cache warning object, adapter layer, fallback provider,
retry owner, completion flag or UI state owner is introduced.

## 3. Consequences

Attempts with missing measurements remain visible as uncertainty instead of
being silently converted into complete totals. A local timeout in nested search
can use a later attempt without extending the caller deadline. A corrupt cache
row fails closed without authorizing a new provider request or semantic repair.
Valid rows survive the sidecar-column removal because their canonical result and
identity remain unchanged. A cache write outage is visible to operators while
an already delivered inference remains usable.

Removing exported cache sidecars is an intentional package contract change. An
older binary that still queries the removed columns cannot run against the
migrated schema; deployment must use one evaluated checkpoint. Generated output
and search evidence remain potentially sensitive result data and continue to
use existing redaction and owner isolation.

## 4. Verification and rollback

TEST-223 and TEST-225 cover transport precedence and nested timeout ownership
with local request counters and synchronized contexts. TEST-226 covers ordered
and independent accounting, rejected/interrupted response facts and result
ledger separation. TEST-224 covers exact JSON replay, shared admission and
producer identity. TEST-227 covers the real forward migration and retained
rows through the runner-owned Postgres service. TEST-228 and WEB-TEST-076 cover
the accepted-inference/cache-write boundary through library, API, traces,
telemetry and Phoenix projections.

R01 through R03 each run focused selectors and `make test-fast`; R03 also runs
the real integration service-pool gate. R04 runs the existing browser-free
`make test-release` gate, which includes its configured `make verify` task. No
browser or live-provider test is part of this ADR.

If an implementation fails, correct the owning boundary and rerun its focused
regression before the affected gate. Rollback is a matching whole-checkpoint
restore; do not restore only application code after applying migration 0007.
