
# ADR-HLLM-031: OpenAI proxy and reference history ownership

- Status: Accepted for implementation
- Date: 2026-10-06
- Requirements: REQ-400–402, REQ-406–410, REQ-412–413
- Tests: TEST-400–409; WEB-TEST-114–116
- Supersedes conflicting profile/per-user-data decisions in ADR-HLLM-013, -016, -018, -025 and -030; historical evidence remains.

## Context

The gateway loads profile records and requires human ownership before inference. It also exposes product history and records every call. Phoenix is the only user-facing producer; direct API calls must stay outside shared history.

## Decision

1. Remove profiles, CRUD/bundles/catalogs/provisioning, profile IDs and per-owner runtime factories. Configure connections once. Requests select a connection and native model ID. One startup Go client and one engine serve both OpenAI protocols.
2. Use bearer-only gateway auth. `HARDEN_LLM_TOKEN` is the only incoming key and creates no owner. Use the existing value in `./.env` for production. `CPA_API_KEY` remains upstream-only.
3. Move shared history/stats to one Phoenix Ecto Repo/context on existing HLLM PostgreSQL hosting. The existing frontend task writes once after publishing an outcome. All enabled logins share access through Control Plane. Save failure never replays inference.
4. Keep optional Go cache separate. No generic store interface, alternate backend, pending rows, queue or reconciler.
5. Reuse ordered session/component drafts. Restore fills the form without dispatch.
6. Perform the authorized clean cut of active HLLM-owned per-login profiles/credentials, histories, traces and artifacts before enabling shared history. Start empty. Keep shared identity, other products, DB/Garage services and unrelated buckets outside the reset. Add no importer or legacy reader.

## Consequences and verification

The API/recovery defaults break old callers. Private history is not reclassified as shared. A crash can lose an outcome between publication and insert; a failed write cannot affect inference. TEST-400–409 verify source and service boundaries. Production records owned before/after counts and preserves an unrelated sentinel.
