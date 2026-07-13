# ADR-HLLM-000: Certified Baseline and Scaling Boundary

- Status: Accepted
- Date: 2026-07-13
- Requirements: REQ-009, REQ-010, REQ-017, REQ-020

## Context

The backend and frontend specifications certify a fifteen-service, single-host
backend and an optional one-replica Phoenix service. The requested system must
also have production-grade boundaries that do not prevent later scale-out.

## Decision

Implement and certify the specified topology unchanged. Keep the Go gateway
stateless outside dedicated Postgres and Garage, use explicit timeouts and
bounded telemetry, and isolate the Phoenix token vault behind one module. Do not
claim high availability for the v1 Compose topology.

Horizontal Phoenix replicas require a shared, encrypted token-vault design and
a new ADR. Multi-node Garage, managed Postgres, regional routing, retention, and
backup automation likewise require deployment-specific plans and cannot alter
the deterministic v1 certification graph.

## Consequences

The code and contracts can be deployed behind production orchestration, but the
committed Compose profile remains the reproducible single-host baseline. A
Phoenix restart invalidates web sessions as specified. Scaling work cannot add
an unreviewed persistence path or weaken bearer-token confinement.

## Deviations

None. This ADR makes the specification's production boundary explicit.
