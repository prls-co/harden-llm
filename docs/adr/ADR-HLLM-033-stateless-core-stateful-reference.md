# ADR-HLLM-033: Stateless core and stateful reference libraries

- Status: Accepted 2026-10-12; implemented and deployed 2026-10-13.
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

## 4. Implementation refinements and acceptance (2026-10-13)

P01 gives `DiagnosticCapture` an `ErrorCode` so projection failure still invokes
the one terminal callback with a bounded diagnostic and no unsafe payload;
TEST-421/445 retain unchanged inference and detachment assertions. P02 adds the
explicit `Config.CacheTTL` field, retaining the already planned 86,400-second
default and TEST-439 expiry behavior. P04 moves client/error declarations with
the components before revising their behavior, so the package never imports its
host. The widget retains its local Save/refresh pending state; workspace async
orchestration remains host-owned. The accepted history interaction loads full
records on details expansion, retaining the original controls and full data.
TEST-428–431/446–448 and EVAL-424 cover these boundaries.

These refinements add no independent service, new permission model or fallback.
Rollback across the authorized schema/data clean cut is not image-only; see
ADR-HLLM-028. The exact source, CI and deployed acceptance are in
[P06](../../plans/evidence/harden-llm/reference-decoupling/phase-p06.md).
The [conformance audit](../../plans/evidence/harden-llm/reference-decoupling/plan-conformance-audit.md)
records the full comparison against the pre-implementation v5.3 plan.


## 5. Production UI/default acceptance correction (2026-10-13)

Issue #109 corrects invalid structured preset inputs, picker focus filtering,
missing deployment credential bindings and incomplete inherited leaf editing.
TEST-452 uses actual shipped data and enabled-submit assertions. Preset semantic
validation calls the existing core schema normalizer; completed user drafts keep
their existing persistence behavior. The shared profile renderer/client now
provides complete node-local editing/save while preserving fixed recovery roles.
See [the RCA](../rca/reference-preset-schema-and-recovery-ui.md). Earlier release
CI/HTTP proof did not cover these interactions and is not promoted to that claim.

The correction is merged and deployed at
`739cfda1ea6a67fb7a2ee737e31d8342c58f36d0`: 28 release tasks passed, scoped/full
configuration checks are equivalent, and all 24 live preset drafts match the
certified source. A one-time exact-default correction repaired one saved draft
without replacing any other settings; it adds no runtime legacy reader. See
the RCA acceptance and its redacted release/deployment records for identities,
credential availability, the preserved harness failure and opt-in limits.
