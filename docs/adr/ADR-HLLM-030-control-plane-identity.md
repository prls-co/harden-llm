# ADR-HLLM-030: Control Plane Human Identity

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
  and DETS bearer vault. The authorized 2026-10-04 clean cut discards legacy
  HLLM product data and artifact objects. One fresh product-only schema replaces
  migrations 1–10; there is no owner-rehome command or compatibility path.
- The production HLLM account owns one dataset. Multiple Control Plane logins
  and the account-scoped API token use that same account UUID. Guest/operator
  are neither HLLM account types nor separate product owners.

## Consequences

Human sign-in, account selection, and access revocation have one owner. HLLM
retains a small product-specific encrypted cookie because a shared login UI does
not require a shared cookie. Access checks depend on Control Plane availability
and are intentionally not cached, so stale product sessions cannot keep access
after membership removal. Machine tokens remain explicit, account-scoped
deployment credentials and are rotated through deployment configuration.

Stop gateway and web, reset the dedicated HLLM database and its artifact bucket,
rotate the HLLM database credential, and deploy the certified images. Provision
profiles for the single entitled Control Plane account, then resume service.
Other products' databases, buckets, users, sessions and grants are outside this
reset. Old HLLM sessions retire; people sign in using Control Plane credentials.
An old schema ledger is rejected, rather than silently upgraded or served.

## Verification

Go auth tests cover current-session resolution, product denial, unavailable
identity service, and the separate scoped machine path. Phoenix tests cover the
shared sign-in/account-selection components and exact host-only cookie options.
PostgreSQL tests prove fresh schema creation, concurrent/idempotent migration,
rejection of retired ledgers, account isolation and cache precision. Auth tests
prove two logins and the configured token resolve the same product owner. Release
acceptance verifies Control Plane sign-in, current product access, logout
revocation, shared account-owned profile reads and empty legacy history.

The full Compose browser test supplies a minimal synthetic Control Plane HTTP
boundary for sign-in/sign-out, access-context resolution, and account listing
and selection. It uses a synthetic account and no Control Plane database or
HLLM-owned human account. This verifies that the Phoenix and gateway consumers
use the shared contract through the real stack; it does not certify Control
Plane authentication or persistence, which remain covered by Control Plane's
own tests and production release evidence.
