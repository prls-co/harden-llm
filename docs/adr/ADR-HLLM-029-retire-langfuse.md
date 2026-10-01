# ADR-HLLM-029: Retire Langfuse

- Status: Accepted
- Date: 2026-09-30
- Requirements: REQ-017 and GitHub issue #83
- Verification: source and production topology reconciliation

## Context

Harden LLM product run history and redacted trace artifacts are stored in its
application Postgres and Garage bucket. The separate Langfuse deployment added a
web/worker pair, Postgres, ClickHouse, Redis, MinIO, persistent volumes, secrets,
and a public route. New HLLM gateway traces are already exported to Laminar.

## Decision

Remove Langfuse from the HLLM deployment and shared ingress. Delete its running
containers, dedicated data volumes, and service credentials. Remove its Compose
fragments, image entries, environment variables, smoke dependencies, and public
Cloudflare/Caddy route.

## Consequences

The Langfuse UI and its stored traces, observations, and uploaded objects are
permanently removed. Recreating the services does not restore this data. New
HLLM gateway traces continue to Laminar. HLLM product runs, history, profiles,
and redacted artifacts in application Postgres and Garage are outside this
decision and remain intact.

There is no Langfuse rollback path or export/import migration. Any future
observability UI or retention requirement needs a separate decision and new
source of truth.
