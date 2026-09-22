# Architecture Decision Records

This directory is the durable record for Harden LLM architecture decisions and
specification deviations. An implementation deviation is not accepted until an
ADR names the affected requirements, tests, operational impact, and rollback or
migration path.

| ADR | Status | Decision |
| --- | --- | --- |
| [ADR-HLLM-000](ADR-HLLM-000-certified-baseline.md) | Accepted | Preserve the specified single-host certification baseline while keeping gateway and frontend ownership boundaries explicit. |
| [ADR-HLLM-001](ADR-HLLM-001-intentional-portability-differences.md) | Accepted | Define the intentional JS-to-Go security and persistence projections. |
| [ADR-HLLM-002](ADR-HLLM-002-root-public-api.md) | Accepted | Replace the JavaScript export inventory with one typed Go call surface. |
| [ADR-HLLM-008](ADR-HLLM-008-rest-process-contract.md) | Accepted | Complete the redacted profile-read, historical-run, and process-bind contracts. |
| [ADR-HLLM-009](ADR-HLLM-009-liveview-security-patch.md) | Accepted | Replace the planned LiveView 1.2.6 pin with the patched upstream release; the current exact pin is 1.2.9. |
| [ADR-HLLM-010](ADR-HLLM-010-overlay-mount-points.md) | Accepted | Use independent read-only mount points for frontend Caddy and Grafana overlays. |
| [ADR-HLLM-011](ADR-HLLM-011-go-security-patch.md) | Accepted | Replace the vulnerable Go 1.26.0 pin with the current 1.26.6 security-patched toolchain. |
| [ADR-HLLM-012](ADR-HLLM-012-frontend-parity-adaptations.md) | Accepted | Record the self-hosted adaptations required to complete the utility-llm frontend behavior without Firebase, browser provider calls, or a second UI/backend path. |
| [ADR-HLLM-013](ADR-HLLM-013-profile-catalog-seed.md) | Accepted | Embed the current 28-profile utility-llm catalog and insert missing presets per owner without credentials or overwrite. |
| [ADR-HLLM-014](ADR-HLLM-014-embedded-widget-runtime-parity.md) | Accepted | Preserve the reusable no-tabs widget while aligning combobox, cache, retry projection, nested upload, and explicit profile-save behavior with utility-llm. |
| [ADR-HLLM-015](ADR-HLLM-015-parallel-test-feedback-hierarchy.md) | Accepted; P07 merged, deployed, and certified | Establish one resource-aware T0-T5 test hierarchy, canonical runner, measured four-slot fast cap, exact assertion oracles, and no initial synthetic DOM dependency. |
| [ADR-HLLM-016](ADR-HLLM-016-widget-draft-and-data-contract.md) | Accepted | Keep drafts component-local, keep refresh saved-profile-only, and make model catalogs host-owned with a small default fallback. |
| [ADR-HLLM-017](ADR-HLLM-017-durable-frontend-sessions.md) | Accepted | Retain the encrypted server-side bearer-token vault across single-replica frontend releases without putting the token in browser session data. |
| [ADR-HLLM-018](ADR-HLLM-018-canonical-execution-accounting-and-recovery.md) | Accepted | Use one canonical execution/accounting record, one execution aggregate, strict frontend models, and a durable artifact recovery journal. |
| [ADR-HLLM-019](ADR-HLLM-019-cached-web-search-routing.md) | Accepted | Route explicit web search to native Responses search or a server-side Jina fallback while keeping cache lookup and refresh semantics unchanged. |

| [ADR-HLLM-020](ADR-HLLM-020-recovery-policy-and-execution.md) | Accepted for implementation | Use one complete recovery policy, selected target, execution loop and shared editor, with a finite standard data migration. |
| [ADR-HLLM-021](ADR-HLLM-021-recovery-boundary-ownership.md) | Implemented and locally certified | Assign failure, dispatch, completion, accounting, cache, host-policy and ordered-persistence facts to one existing owner. |
| [ADR-HLLM-022](ADR-HLLM-022-recovery-integrity-boundaries.md) | Accepted and implemented | Correct nested timeout and dispatched-accounting coverage, validate lossless cache replay, remove redundant cache sidecars, and preserve accepted inference after a cache-write failure. |
| [ADR-HLLM-023](ADR-HLLM-023-reusable-numbered-pagination.md) | Accepted and implemented | Use a neutral in-house Phoenix pager/state layer after the Petal dependency gate failed; add strict numbered History reads without breaking cursor clients. |
| [ADR-HLLM-024](ADR-HLLM-024-bounded-recovery-and-progress.md) | Accepted for implementation | Use one finite two-target repair shape per generation branch, one global budget/deadline, request-bound progress, and incremental provider terminal diagnostics; preserve existing accounting, cache, and ownership boundaries. |
| [ADR-HLLM-025](ADR-HLLM-025-recursive-profile-widget.md) | Accepted and implemented locally | Reuse the complete LLM profile widget for six fixed recovery nodes while keeping role capabilities, explicit bindings, and finite runtime execution. |
| [ADR-HLLM-026](ADR-HLLM-026-recovery-closeout-verification.md) | Accepted and implemented | Require independent candidate-release intent, preserve pending SSE outcomes under the existing deadline, render inherited retry controls through the shared widget, and record two-application production evidence without increasing timeouts. |
| [ADR-HLLM-027](ADR-HLLM-027-resource-ownership-and-measured-capacity.md) | Accepted for test-harness implementation | Add durable ownership and local-daemon coordination for managed test Docker resources, bounded redacted receipts, and opt-in capacity measurement; production SLO and architecture migration remain separate decisions. |
| [ADR-HLLM-028](ADR-HLLM-028-local-image-build-deployment.md) | Accepted and implemented | Retire GHCR publication from the active lifecycle; build and deploy the gateway image locally by exact source SHA, with a compressed historical publisher reference. |

The remaining planned deviation triggers are ADR-HLLM-003 through ADR-HLLM-007
in the canonical implementation plan. Create one of those records only when its
trigger occurs; do not pre-approve a deviation.
