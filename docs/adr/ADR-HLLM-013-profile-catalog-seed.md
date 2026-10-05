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

## Catalog update — 2026-10-04

The original 28-entry import above records source provenance. The maintained
Harden-LLM seed now contains 26 profiles: `CPA GPT-5.4` and
`CPA GPT-5.4 Mini` are retired. An authenticated read of CPA's
`https://cpa.prls.co/v1/models` confirmed that neither model is available;
the authorized live smoke also returned `model_not_found` for both. The
independent OpenAI GPT-5.4 presets remain part of the catalog.

The trusted host configuration removes the same two profiles and their
credential references, and refreshes retained CPA profiles' model lists from
that endpoint. Model discovery metadata does not establish inference,
pricing, or reasoning capabilities for a new preset, so the discovered models
are available as editor choices without inventing full profile definitions.

Deploy the updated seed before deleting saved copies through the existing
profile DELETE endpoint; otherwise incremental seeding would recreate them.
Profile deletion already removes an unreferenced credential. Shared profile
sync remains an upsert that preserves custom profiles; retirement does not
introduce a second synchronization or migration path. `TEST-017` checks the
exact retained catalog and prepares every retained profile offline. Read-only
utility-llm evidence and the original failed live matrix remain unchanged.

## Perplexity Agent catalog update — 2026-10-04

Authenticated `GET https://api.perplexity.ai/v1/models` replaces the three
legacy Sonar presets with six current `perplexity/` models. The embedded seed
contains 29 profiles. Perplexity uses the existing Responses payload and
normalization contract at its canonical `/v1/agent` endpoint, with explicit
model selection and discovery pricing. Sonar Pro, Sonar Reasoning Pro, their
search flags, and their provider-specific chat-search path are retired.
Historical utility fixtures remain source evidence; they do not define the
current managed catalog. Router API preview access and research presets are
not required by this model catalog.

## Novita accounting and OpenRouter Pro routing amendment — 2026-10-04

Authenticated Novita discovery confirms USD-per-million input/output/cache-read
rates of 0.14/0.28/0.028 for Flash and 1.60/3.20/0.135 for Pro. Both presets now
price reasoning at the paid completion rate. `OpenRouter DeepSeek V4 Pro` uses
one `deepinfra/fp8` endpoint with `allow_fallbacks: false` through existing
native `defaultOptions`; provider-reported costs remain authoritative. This
preserves all 29 seed names and the existing structured-output contract.

The shared usage parser accepts nullable optional breakdown containers while
retaining strict actual-count, component and cost validation. Contradictory
Relace reasoning totals remain rejected; they are not clamped or discarded.
TEST-013 and TEST-017 cover the parser, rates, wire route and cache identity.
Existing saved presets receive these same values through trusted profile sync;
no profile, credential, owner, workspace state or historical accounting is
reconstructed. See [issue 92](https://github.com/prls-co/harden-llm/issues/92#issuecomment-5988967325)
for the RCA and [OpenRouter provider selection](https://openrouter.ai/docs/guides/routing/provider-selection)
for the native routing contract.
