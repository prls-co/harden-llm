# ADR-HLLM-033: Stateless core and stateful reference libraries

- Status: Accepted, 2026-10-12; implementation proceeds through P00–P06.
- Supersedes: ADR-HLLM-031's profile-free/reference-owned persistence boundary.
- Contract: [PLAN-HLLM-REFERENCE-DECOUPLING-002](../../plans/restore-reference-workflows-decoupling-plan.md), v5.3.

## 1. Decision

Keep one stateless execution core with injected provider/cache/observer effects.
A reusable `stateful/` library owns concrete composition and all application
storage. One gateway exposes ordinary `/v1` and reference `/api/v1`, through one
validated bearer and server-derived `api_key_hash`. The core receives neither
incoming keys nor storage scope. No user/key registry or session introspection
is added. Portal admission remains Phoenix-host-owned.

Git presets are copied starting values. Explicit create-on-save freezes complete
provider/run setup into a fresh immutable profile. Credentials remain separate
explicit bindings: deployment environment or encrypted entered secrets. Drafts
and credentials use CAS. Saved definitions do not merge later Git changes.

All state is key-scoped; reference drafts additionally use a host-supplied
opaque draft key, `workspace:<stable Portal user ID>`. The same key shares other
records across logins/clients. New keys start fresh scopes; rotation handling is
manual. No compatibility reader, data migration or startup seeding is included.

## 2. Reuse and unchanged behavior

Restore the pinned pre-PR-97 UI and all 32 manifest capabilities. Only key-scoped
ownership and complete create-on-save profiles are accepted product changes.
Extract one `HardenLlmUI` package for client/components/styles. Host routing,
admission and async lifecycle stay outside it. Model Recall was reviewed
read-only for concrete host needs; its migration is not required for release.

## 3. Validation

TEST-420 freezes source provenance and canonical ownership. Core, storage,
HTTP, UI, composed and deployed tests prove their own boundaries in subsequent
phases. This ADR is a decision, not runtime or production evidence.
