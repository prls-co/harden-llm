# Architecture Decision Records

This directory is the durable record for Harden LLM architecture decisions and
specification deviations. An implementation deviation is not accepted until an
ADR names the affected requirements, tests, operational impact, and rollback or
migration path.

| ADR | Status | Decision |
| --- | --- | --- |
| [ADR-HLLM-000](ADR-HLLM-000-certified-baseline.md) | Accepted | Preserve the specified single-host certification baseline while keeping stateless service boundaries. |

The planned deviation triggers remain ADR-HLLM-001 through ADR-HLLM-007 in the
canonical implementation plan. Create one of those records only when its trigger
occurs; do not pre-approve a deviation.
