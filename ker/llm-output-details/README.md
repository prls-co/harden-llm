# LLM output details known error records

This folder contains the Problem Records and Known Error Records discovered
while auditing the production LLM trace details and aggregate stats widgets at
application release `fcda74b3824fc22a517df709f0a67939b8aa0b9c`.

Each record is intentionally independent and follows
`/home/kirill/p/llm-coding-tools/ker-generation-prompt.txt`. All seven records
are open: they document the current failure mode, evidence, bounded workaround,
recommended permanent correction, and the verification required before closure.

| KER | Surface | Impact |
| --- | --- | --- |
| [`20260901-output-details-misattributed-fallback-model-identity.md`](20260901-output-details-misattributed-fallback-model-identity.md) | Run and attempt identity | High |
| [`20260901-run-history-inconsistent-token-semantics.md`](20260901-run-history-inconsistent-token-semantics.md) | Token metrics | Medium |
| [`20260901-production-history-legacy-orphan-traces.md`](20260901-production-history-legacy-orphan-traces.md) | Retained production data | Medium |
| [`20260901-artifact-metadata-cross-store-crash-window.md`](20260901-artifact-metadata-cross-store-crash-window.md) | PostgreSQL and Garage consistency | Medium |
| [`20260901-reusable-trace-widget-duplicate-cache-id.md`](20260901-reusable-trace-widget-duplicate-cache-id.md) | Reusable component contract | Low |
| [`20260901-stats-load-failure-renders-zero-values.md`](20260901-stats-load-failure-renders-zero-values.md) | Stats availability state | Medium |
| [`20260901-unknown-cost-renders-zero-dollars.md`](20260901-unknown-cost-renders-zero-dollars.md) | Cost semantics | Medium |

Telemetry is deliberately not a widget data source. Product history and stats
come from application PostgreSQL, artifact bodies come from Garage, and the
OpenTelemetry pipeline remains a diagnostic side channel to Tempo, Langfuse,
Prometheus, and Loki. Separate PRLS receivers export traces to Laminar;
ClickHouse is internal to Langfuse.

## Shared target architecture

The seven plans converge on one ownership chain:

```text
provider adapters
  -> canonical root-library execution / accounting record
  -> thin gateway transport and persistence projection
  -> atomic application PostgreSQL aggregate
  -> OpenAPI REST contract
  -> strict pure Phoenix projections
  -> presentational, instance-scoped LiveView components
```

Garage remains the artifact-body store. A PostgreSQL operation journal and one
gateway artifact coordinator bridge the unavoidable cross-store boundary;
neither telemetry nor a second service owns recovery. PostgreSQL remains the
source for history and aggregate stats. Product records feed telemetry, never
the reverse.

The design intentionally creates reusable internal boundaries before creating
a separately distributed widget package:

- the root library owns selected and actual identity, result source, result and
  provider accounting, cache provenance, and call-global attempt semantics;
- the gateway supplies authenticated request context and owns versioned
  persistence and OpenAPI without reconstructing execution facts;
- `LlmStatsProjection` and `LlmTraceProjection` own strict pure view models;
- LiveView components own markup, DOM identity, ARIA, and root-scoped styles;
- host LiveViews own asynchronous resource state and instance-keyed events.

## Recommended delivery order

1. Define the canonical execution record: selected and actual attempt identity,
   provider/cache/none result source, result/provider accounting, and one
   call-global attempt budget. Cut cache v2 once.
2. Complete the stats contract: exact token vocabulary, usage completeness,
   cost coverage including the cached subset, and strict OpenAPI examples.
3. Cut over the frontend boundary together: explicit stats resource state,
   strict projections, semantic component inputs, instance-scoped events/DOM,
   ARIA, and root-scoped styles.
4. Add the artifact operation journal/coordinator and prove crash convergence.
5. Rehearse and run the historical reconciliation, then add structural
   run-to-trace ownership and remove independent writers/read fallbacks.
6. Run release and exact-revision production certification with redacted
   invariant reports. Destructive retained-data work occurs only after tested
   PostgreSQL and Garage restore evidence and an approved dry-run digest.

This sequence avoids duplicate migrations and compatibility paths: identity,
usage, and cost share one execution/cache version cut; frontend KERs share one
component contract cut; storage consistency precedes destructive historical
cleanup.
