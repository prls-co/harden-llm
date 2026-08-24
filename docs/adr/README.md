# Architecture Decision Records

This directory is the durable record for Harden LLM architecture decisions and
specification deviations. An implementation deviation is not accepted until an
ADR names the affected requirements, tests, operational impact, and rollback or
migration path.

| ADR | Status | Decision |
| --- | --- | --- |
| [ADR-HLLM-000](ADR-HLLM-000-certified-baseline.md) | Accepted | Preserve the specified single-host certification baseline while keeping stateless service boundaries. |
| [ADR-HLLM-001](ADR-HLLM-001-intentional-portability-differences.md) | Accepted | Define the intentional JS-to-Go security and persistence projections. |
| [ADR-HLLM-002](ADR-HLLM-002-root-public-api.md) | Accepted | Replace the JavaScript export inventory with one typed Go call surface. |
| [ADR-HLLM-008](ADR-HLLM-008-rest-process-contract.md) | Accepted | Complete the redacted profile-read, historical-run, and process-bind contracts. |
| [ADR-HLLM-009](ADR-HLLM-009-liveview-security-patch.md) | Accepted | Replace the planned LiveView 1.2.6 pin with the patched upstream release; the current exact pin is 1.2.9. |
| [ADR-HLLM-010](ADR-HLLM-010-overlay-mount-points.md) | Accepted | Use independent read-only mount points for frontend Caddy and Grafana overlays. |
| [ADR-HLLM-011](ADR-HLLM-011-go-security-patch.md) | Accepted | Replace the vulnerable Go 1.26.0 pin with the current 1.26.6 security-patched toolchain. |
| [ADR-HLLM-012](ADR-HLLM-012-frontend-parity-adaptations.md) | Accepted | Record the self-hosted adaptations required to complete the utility-llm frontend behavior without Firebase, browser provider calls, or a second UI/backend path. |
| [ADR-HLLM-013](ADR-HLLM-013-profile-catalog-seed.md) | Accepted | Embed the current 28-profile utility-llm catalog and insert missing presets per owner without credentials or overwrite. |
| [ADR-HLLM-014](ADR-HLLM-014-embedded-widget-runtime-parity.md) | Accepted | Preserve the reusable no-tabs widget while aligning combobox, cache, retry projection, nested upload, and explicit profile-save behavior with utility-llm. |
| [ADR-HLLM-015](ADR-HLLM-015-parallel-test-feedback-hierarchy.md) | Accepted for implementation | Establish one resource-aware T0-T5 test hierarchy, exact assertion oracles, measured budgets, and no initial synthetic DOM dependency. |

The remaining planned deviation triggers are ADR-HLLM-003 through ADR-HLLM-007
in the canonical implementation plan. Create one of those records only when its
trigger occurs; do not pre-approve a deviation.
