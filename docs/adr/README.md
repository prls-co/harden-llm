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

The remaining planned deviation triggers are ADR-HLLM-003 through ADR-HLLM-007
in the canonical implementation plan. Create one of those records only when its
trigger occurs; do not pre-approve a deviation.
