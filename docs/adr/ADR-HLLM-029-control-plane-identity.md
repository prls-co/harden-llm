# ADR-HLLM-029: Control Plane Human Identity

- Status: Accepted for implementation; production data cutover pending explicit account mapping
- Date: 2026-09-30
- Requirements: REQ-010, REQ-011, REQ-012, `SPEC-HARDEN-LLM-PHOENIX-LIVEVIEW-001`
- Verification: `internal/gateway/auth`, `internal/postgres` migration tests,
  `WEB-TEST-104` through `WEB-TEST-108`, shared-client integration tests

## Context

Harden LLM previously duplicated human credentials and opaque API sessions in
its own database. That made account changes, password resets, and access
revocation product-specific, and required a separate durable token vault in the
Phoenix frontend. PRLS Control Plane now owns human accounts, authentication,
memberships, and current product access. Keeping a second user/session system
would allow these authorities to drift.

## Decision

- Control Plane is the sole human identity and current product-access
  authority. HLLM calls it through the shared `@prls/access` contract and fails
  closed when that authority is unavailable or denies access.
- HLLM owns product data: profile settings and encrypted provider credentials,
  workspace state, runs, traces, artifacts, and retention. Product owner keys
  are Control Plane account UUIDs; HLLM does not store passwords, emails as
  identity keys, memberships, or local human sessions.
- The Phoenix frontend uses the shared PRLS sign-in and account-selection
  components. It stores an encrypted session reference in the host-only
  `__Host-harden_llm_web` cookie. The cookie has `Path=/`, `Secure`, `HttpOnly`,
  `SameSite=Lax`, and no `Domain`; every PRLS product keeps a separate browser
  session.
- Phoenix sends the service bearer and current Control Plane session reference
  to the Go gateway. The gateway resolves the current account and HLLM access
  for every human request. The browser never receives the service bearer or
  calls the gateway directly.
- The existing service bearer supports a separate machine path only when
  `HARDEN_LLM_STATIC_TOKEN_ACCOUNT_ID` explicitly scopes it to one account.
  Profile provisioning likewise takes explicit account UUIDs; it never looks
  up guest/operator users by email.
- Remove HLLM's local login, password reset, bootstrap command, session tables,
  and DETS bearer vault. Existing local owner records are rehomed once to
  explicitly mapped account UUIDs, with encrypted credentials rebound to the
  new UUID and artifact objects verified before old object prefixes are
  deleted. The final schema migration removes the local identity tables.

## Consequences

Human sign-in, account selection, and access revocation have one owner. HLLM
retains a small product-specific encrypted cookie because a shared login UI does
not require a shared cookie. Access checks depend on Control Plane availability
and are intentionally not cached, so stale product sessions cannot keep access
after membership removal. Machine tokens remain explicit, account-scoped
deployment credentials and are rotated through deployment configuration.

The schema cutover is forward-only. The gateway must stay stopped while every
old owner is mapped and its product data is rehomed. Consolidating multiple old
owners into one account is rejected. Existing images that expect local user
tables cannot be rolled back after the final migration; recovery uses a
compatible forward release. Production and persistent preview cutovers remain
blocked until each local owner has an explicit Control Plane account mapping.

## Verification

Go auth tests cover current-session resolution, product denial, unavailable
identity service, and the separate scoped machine path. Phoenix tests cover the
shared sign-in/account-selection components and exact host-only cookie options.
PostgreSQL migration tests prove that schema removal refuses unmapped local
users and that rehoming cascades account UUIDs through dependent rows. Release
acceptance separately verifies Control Plane sign-in, current product access,
revocation, profile/run ownership, and retained artifact reads.
