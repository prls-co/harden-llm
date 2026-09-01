# ADR-HLLM-018: Canonical Execution, Accounting, and Recovery Contract

- Status: Accepted
- Date: 2026-09-01
- Requirements: REQ-008, REQ-010, REQ-011, REQ-019, REQ-020, REQ-021, and GitHub issue #46
- Verification: TEST-057 through TEST-061 and WEB-TEST-060 through WEB-TEST-063

## Context

The production output-details audit found seven related defects: selected model
identity was presented as the terminal producer, token labels and formulas
diverged between surfaces, retained history contained runless trace subtrees,
Garage and PostgreSQL had crash windows, trace widgets retained global DOM and
event identity, asynchronous stats failures rendered authoritative-looking
zeroes, and unknown or partial cost rendered as a measured dollar amount.

The defects share one architectural cause: execution facts are projected more
than once. Runtime, pricing, gateway run output, domain traces, PostgreSQL JSON,
aggregate SQL, telemetry, and Phoenix each own fragments of identity or
accounting semantics. Fixing each display symptom independently would preserve
the split ownership.

## Decision

### One canonical execution record

The root Go library owns one canonical internal execution record and returns one
typed public result derived from it. The record contains:

- the immutable selected target snapshot;
- a call-global ordered attempt sequence;
- the immutable prepared target and exact provider-used state for each attempt;
- a discriminated result source: provider attempt, cache producer, or none;
- result accounting and current provider accounting;
- cache facts, output, status, timing, repair state, and redacted artifact
  projections.

The gateway supplies authenticated catalog/request context and the run ID, but
does not reconstruct selected or actual execution semantics after the call.
Public result metadata, persisted product data, domain trace projections,
artifacts, logs, metrics, and spans all derive from the canonical record.

`maxAttempts` is one call-global provider-invocation budget. Backup and repair
attempts consume that budget. Retry-local numbering may remain diagnostic, but
the persisted/public sequence is globally unique within the call.

### One accounting owner and two ledgers

`internal/accounting` is the only owner of normalized usage, checked arithmetic,
cost certainty, and display-group formulas. Existing overlapping runtime,
pricing, and stats arithmetic is removed or reduced to adapters.

Canonical usage has five exclusive components: input, cache read, cache
creation, output, and reasoning. Prompt, completion, and total are derived.
Completeness is explicit; unavailable or inconsistent data is never normalized
to authoritative zero.

Every execution carries two accounting views:

- result accounting describes the returned result and its producer;
- provider accounting describes provider work performed for the current call.

A cache hit may carry result accounting from its immutable cache producer while
current provider accounting is exactly empty. Failed/retried provider attempts
remain in provider accounting even when they did not produce the result.

Cost certainty is `exact`, `partial`, `unknown`, or `unavailable`. Known
subtotals survive unknown attempts. Product cost is trace-attributed diagnostic
cost, not a billing ledger. Current mutable profile prices and telemetry are
never historical backfill sources.

### One execution aggregate in PostgreSQL

`llm_runs` is the retrofit aggregate root. New execution truth is persisted
once in the run's versioned result document plus typed query columns required by
history and stats. `llm_traces` becomes a one-to-one execution identity child;
attempt/event rows and artifact metadata remain children. New writes do not
persist a second trace record containing duplicate execution facts.

The database enforces this ownership structurally: `llm_traces.run_id` is not
nullable, the exact owner/run/trace tuple references `llm_runs`, and deleting
the run cascades all relational children. `SaveExecution` inserts the run first
and is the only production aggregate writer; independent run, trace, artifact,
and relational delete methods are not part of the production store API.

The trace REST endpoint projects from the execution aggregate. The Garage trace
JSON is an immutable redacted export, not a source of product truth. Standalone
domain traces are unsupported until a separate producer, lifecycle, and REST
contract are explicitly introduced.

Retained v1 documents are read through one bounded version-aware normalizer and
render missing facts as not captured. The public `/api/v1` schema is cut over
atomically with Phoenix; no duplicate old/new wire fields or alternate endpoint
is retained. Cache envelopes move once to v2 and old entries are invalidated.

### Durable artifact coordination

The gateway owns one artifact coordinator backed by a PostgreSQL operation
journal. Typed publication/deletion intents carry owner, run, trace, artifact
kind, key, digest, size, and content type; identity is never recovered by
parsing an object key.

Garage PUT and DELETE operations are idempotent. Publication, deletion, partial
multi-delete, ambiguous responses, process termination, and restart converge
through one bounded in-process reconciler. The reconciler is a lightweight
gateway maintenance loop, not a second service or workflow engine.

A separate bounded read-only inventory command compares Garage listings with
live metadata and incomplete journal operations. It reports only aggregate
counts and never deletes unknown objects. Forward convergence remains journal
driven; reverse inventory is an operator verification and incident tool.

Execution commits use a shared owner advisory lock. Clear-history planning uses
the matching exclusive owner lock, and single-run deletion uses an ordered
per-execution lock. Object operations occur after a durable plan and do not hold
a database transaction across network I/O.

### Strict frontend boundary

`HardenAPI` remains the only REST client and strictly decodes the OpenAPI
contract into typed frontend data. Pure trace/stats view-model modules format
already validated data; they do not own transport validation or backend
arithmetic.

LiveViews use `Phoenix.LiveView.AsyncResult` for stats lifecycle. The component
renders loading, available, refreshing, stale, or unavailable independently of
numeric values. A successful empty population is an authoritative empty result;
unknown values on existing runs are not zero.

Pure projections carry semantic fields only. Function components own DOM IDs,
ARIA, classes, data attributes, and root-scoped styles. Host LiveViews own
instance-keyed events and state. A separately distributed cross-framework UI
package remains deferred until a second consumer defines its requirements.

### Telemetry remains a projection

PostgreSQL and Garage remain product sources of truth. Tempo, Loki, Prometheus,
Langfuse, Laminar, and Langfuse-owned ClickHouse consume bounded projections of
the canonical execution record and never supply widget values, reconciliation
decisions, or historical backfills.

## Consequences

This is a coordinated contract migration rather than seven presentation
patches. Runtime/public types, cache, persistence, OpenAPI, Phoenix, fixtures,
telemetry, specifications, and release evidence change together. Retained data
requires an explicit audit/reconciliation phase before the structural
run-to-trace constraint can be enabled.

The design adds an artifact operation journal and a bounded maintenance loop,
but avoids a distributed transaction, maintained stats projection, second
application database, second object store, workflow service, or telemetry
dependency. Normal concurrent runs are not serialized by clear-history safety.

## Migration and rollback

1. Introduce canonical execution/accounting and cache v2 with deterministic
   tests; invalidate cache v1.
2. Add typed execution query fields and version-aware retained-data reads.
3. Cut OpenAPI and Phoenix atomically.
4. Add the artifact journal/coordinator and run it in audit mode before
   mutation.
5. Reconcile retained history from a tested PostgreSQL/Garage restore, then add
   structural run-to-trace ownership and remove independent writers/read
   fallbacks.

The release binary supports that ordering directly: `reconcile-history`
applies at most migrations 1-4, while normal startup applies the full set.
Migration 5 rejects runless or mismatched rows and remains retryable after a
failed precondition.

Each pushed checkpoint is deployable only when its current schema and code are
compatible. Rollback returns the whole checkpoint image set; it never restores
invalidated cache entries, fabricates retained identity/cost, or reintroduces a
second persistence or telemetry path.

## Verification

- TEST-057: canonical execution identity, attempt budget, and result source.
- TEST-058: canonical usage/cost, result/provider accounting, and cache v2.
- TEST-059: typed execution persistence, OpenAPI, stats, and mixed-version data.
- TEST-060: artifact journal, idempotency, crash convergence, and lock policy.
- TEST-061: retained-history reconciliation and structural execution ownership.
- WEB-TEST-060: strict trace/stats frontend data models.
- WEB-TEST-061: stats lifecycle and cost/usage certainty.
- WEB-TEST-062: multi-instance trace component identity and event isolation.
- WEB-TEST-063: rendered execution identity, accounting, and legacy states.

Completion additionally requires `make test-fast`, `make verify`,
`make test-browser`, `make test-release`, exact pushed revision/image identity,
tested backup and restore before retained-data mutation, authenticated deployed
canaries, post-deployment invariants, and canary cleanup.
