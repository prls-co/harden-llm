# ADR-HLLM-008: Complete the REST and Process Contract

- Status: Accepted
- Date: 2026-07-13
- Requirements: REQ-007, REQ-009, REQ-011, REQ-012, REQ-019, REQ-020
- Verification: TEST-023, TEST-024, TEST-025, TEST-026, TEST-027

## Context

The stack route table omitted a collection read for profiles even though the
independent client must render and edit redacted profile state. Using bundle
export for that read would expose encrypted portability records unnecessarily.
The process configuration also omitted a bind address, and a profile foreign
key on historical runs would make profile deletion or atomic bundle replacement
conflict with the requirement to retain run history.

## Decision

Add authenticated `GET /api/v1/profiles`, returning only validated profiles and
public credential state in the standard envelope. OpenAPI remains canonical for
this additional client-neutral operation.

Treat `llm_runs.profile_id` as the profile identifier snapshot captured at run
time, not a foreign key to the mutable profile catalog. Runs remain owner-scoped
and are still deleted with their owning user.

Add `HARDEN_LLM_LISTEN_ADDRESS`, defaulting to `:8080`, as the process bind
setting. Caddy remains the only intended public ingress in the deployment.

## Consequences

Profile screens can load without receiving encrypted credential records.
Deleting or replacing profiles preserves history, while readers must not assume
that every historical profile still exists. Operators may override the bind
address without changing the REST contract.

These changes are included in the pre-release baseline migration and OpenAPI
document. A pre-release database created with a run/profile foreign key must
drop that constraint before upgrade. Restoring the foreign key would require
reconciling historical profile identifiers first; removing the list operation
after release would be a REST breaking change.
