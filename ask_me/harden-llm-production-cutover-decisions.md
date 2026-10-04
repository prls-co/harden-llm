# HLLM production cutover decisions

Accepted by the user on 2026-10-04. This replaces the preservation-oriented
questions previously recorded in this file.

## 1. One account, multiple logins

Use one ordinary Control Plane company account to own HLLM data. Different
people can sign in with their Control Plane credentials; the existing API token
can resolve to the same account UUID through `HARDEN_LLM_STATIC_TOKEN_ACCOUNT_ID`.
There are no HLLM guest/operator account types. `auth.Principal.OwnerID` is the
account UUID, rather than a login identity. Control Plane retains sole ownership
of credentials, sessions, membership and product grants.

Discard old HLLM product data that does not fit. Remove owner-rehome logic and
legacy identity schema support. Keep shared infrastructure and other products'
data outside the reset. The production account uses the existing company account
`PRLS verification` (`7d677c59-aac7-4cdd-8416-317b99ffa11c`).

## 2. Cut over when ready (2B)

A stop/reset/start outage is acceptable. Certify and build the candidate, prepare
the approved production configuration, stop HLLM gateway/web, clear the dedicated
HLLM database and artifact objects, rotate the exposed database credential,
initialize current schema and provision profiles, then resume service. No
scheduled maintenance window or legacy-data recovery bridge is required.

## 3. Deterministic and authenticated HTTP acceptance (3C)

Run `make test-fast`, browser-free `make test-release`, and production HTTP
checks for health/readiness, Control Plane sign-in, two logins/token sharing
account-owned profiles, empty legacy history, logout revocation and denial for
unentitled accounts. Record exact source SHA and component image IDs.

Do not run browsers or live providers. Browser layout and paid-provider behavior
are outside this release's acceptance. The canonical procedure is
[`docs/self-hosting.md`](../docs/self-hosting.md#clean-identity-cutover); the
architecture is [`ADR-HLLM-030`](../docs/adr/ADR-HLLM-030-control-plane-identity.md).
