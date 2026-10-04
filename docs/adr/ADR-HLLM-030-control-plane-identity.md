# ADR-HLLM-030: Control Plane Human Identity

- Status: Accepted; login-owned implementation verified, production acceptance pending
- Date: 2026-09-30; ownership amended 2026-10-04
- Requirements: REQ-010, REQ-011, REQ-012, `SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001`
- Verification: TEST-022, TEST-024, WEB-TEST-104/105/108 through WEB-TEST-113
- Implementation: [login-owned plan](../../plans/login-owned-data-implementation-plan.md)

## Context

Control Plane replaced HLLM's duplicate credentials, session tables and bearer
vault. The first cutover used company-owned data with an account selector. The
user subsequently selected private data per login and direct entry to HLLM.
The shared-company production release remains historical evidence in the
[release journal](../release-certification.md).

## Decision

- Control Plane owns human credentials, identity, enabled status and sessions.
  Every enabled identity may use HLLM; no company selection or HLLM product
  grant is required. HLLM stores no passwords, local users or human sessions.
- Product data is private to the stable Control Plane `user_id`, carried as
  `auth.Principal.OwnerID`. Email, role and selected company do not change this
  key. Profiles/provider credentials, state, runs/history/stats, traces,
  artifacts, cache and deletion all use that same owner.
- Phoenix reuses shared PRLS login, access Plug and LiveAuth with
  `:require_access`, and configures its safe default return path as `/`.
  It mounts no company selector. The encrypted host-only product cookie remains
  `__Host-harden_llm_web`, `Path=/`, `Secure`, `HttpOnly`, `SameSite=Lax`, with no
  `Domain`. No service bearer or provider credential enters browser JavaScript.
- Phoenix forwards the service bearer and current Control Plane session
  reference. The gateway resolves fresh identity for every human request through
  the existing shared client's `Resolve()` API. Revoked/malformed sessions and
  identity outages fail closed, with no direct-token fallback.
- Direct API use of the deployment credential requires explicit
  `HARDEN_LLM_STATIC_TOKEN_USER_ID`. Production binds it to the verification/test
  identity. It retains its deployment-managed rotation/removal lifetime; human
  logout or disabling does not revoke that independent credential.
- Trusted provisioning takes `sync-profiles --user-id <id>` and explicit
  `HARDEN_LLM_PROFILE_USER_IDS`. IDs are opaque, not company UUIDs. Each owner
  receives separately bound encrypted credentials. Other new users receive
  default profiles without credentials.
- Retain current text owner columns, indexes, encryption bindings and schema
  version 11. The accepted cutover clears incompatible HLLM product data and
  dedicated artifact objects. No schema redesign, owner-rehome, dual-reader,
  local identity fallback or obsolete account configuration is retained.

## Consequences

Login identity and data ownership now agree. Multiple sessions for one person
share their dataset; different people have private datasets even in the same
company. Administrators do not inherit other people's HLLM data. Shared library
company support remains available to other products, without an HLLM mode flag.

Human checks depend on current Control Plane availability and are not cached.
LiveAuth revalidates before events and on its existing timer; identity-only
views ignore company switches and halt when identity changes or access ends.

The release uses a stop/reset/start outage limited to HLLM gateway/web, product
database and artifact objects. Other products, shared identity and storage
services, and observability data stay outside the reset.

## Verification

TEST-022 covers fresh identity, exact service/header validation, different-user
isolation, same-user sessions, explicit token ownership, and denial without
fallback. Real Postgres/Garage cases cover credentials/bundles, history/stats,
cache, artifacts and deletion. Shared library and deterministic Phoenix tests
cover direct login, safe return paths, ongoing identity and unchanged cookies.
WEB-TEST-106/107 are retired company-selector assertions, not weakened checks.

Production HTTP checks must prove private state with distinct markers, equality
between the verification user's data and token data, and logout revocation.
Identical seeded profiles or HTTP health alone do not prove data ownership.
Browser and live-provider checks remain explicit opt-ins.
