# ADR-HLLM-013: Current Utility-LLM Profile Catalog and Incremental Seeding

- Status: Accepted
- Date: 2026-08-18
- Requirements: REQ-004, REQ-007, REQ-008, REQ-009, REQ-010, REQ-011, REQ-018, REQ-019
- Verification: `SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001` `TEST-017`, `go test ./... -count=1`, and the tagged Postgres seed test

## Context

The current read-only source checkout at `/home/kirill/p/utility-llm` is clean
at revision `5c0309e2508dc5b7a87d0880c8d794123353c5b0` (`0.15.0`). Its
Trace Studio catalog at
`examples/react-trace-studio/llm-profile-catalog.json` contains 28 curated
profiles. The older Harden-LLM parity fixture contains only the two synthetic
`Primary` and `Backup` profiles, so it cannot be the product's current preset
catalog.

The source behavior also includes a profile smoke matrix that exercises every
preset profile and a temporary custom profile. Harden-LLM deterministic gates
cannot contact paid providers or require operator credentials, and the target
must not reintroduce Firebase/Firestore persistence or a second provider path.

## Decision

- Embed the exact current 28-entry source catalog as the credential-free,
  immutable seed at `internal/profiles/default-profile-catalog.json`. The Go
  process validates it with the normal profile parser at startup and never
  reads the source checkout at runtime.
- On the first and subsequent profile/catalog/runtime operations, insert every
  missing seed row through Postgres `SeedProfiles`. An owner advisory
  transaction lock makes concurrent first-use requests converge on one
  complete catalog. A row already present for that profile ID, including a
  custom or operator-edited row, is never overwritten.
- Seed rows contain no credential reference, runtime model-discovery state, or
  secret-shaped field. Profile reads expose a deterministic, non-secret
  endpoint binding ID with `configured:false`; saving a credential may use that
  binding, while runtime execution still fails closed until a credential is
  actually stored.
- Credential-free rows remain in the assembled runtime catalog so one missing
  credential cannot invalidate configured profiles. Selecting an unconfigured
  row returns HTTP `422` with stable code `credential_required`, persists the
  failed history item, and does not contact the provider.
- Translate the source all-profile smoke setup into a deterministic provider
  preparation matrix for every seeded profile, covering text and structured
  operations, endpoint/protocol selection, reasoning defaults, pricing, and
  credential non-disclosure. The paid live execution remains an opt-in release
  check rather than a deterministic gate.

## Consequences

New owners see the same current 28 presets as utility-llm without manual JSON
import, and existing owners receive any missing presets on their next profile
operation without losing their custom rows or credentials. The source catalog
is auditable by its path, revision, and embedded-file hash recorded in the
implementation status document. Catalog updates are explicit code/data
changes and require this parity test and review again.

The runtime catalog therefore remains usable for configured profiles even when
other seeded rows are not configured. The frontend can distinguish the stable
`credential_required` validation response from an ambiguous transport failure,
so an operator is told to configure the endpoint before retrying rather than
being asked to refresh history for a run that never reached a provider.

The live source smoke's provider execution and temporary custom-profile cleanup
are not run in deterministic CI; the translated preparation and seed tests
cover the portable contract without network or secrets. This is a documented
self-hosted verification adaptation, not a compatibility or fallback path.

Rollback is stateless: removing the seed wiring prevents future preset
backfills but does not delete existing rows. No production or test timeout
changed, so no new KER timeout record is required.
