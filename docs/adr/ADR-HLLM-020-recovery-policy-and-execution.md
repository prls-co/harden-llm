# ADR-HLLM-020: One Recovery Policy and Execution Loop

- Status: Implemented and locally certified; operational cutover remains separate.
- Date: 2026-09-14.
- Requirements: REQ-201 through REQ-212 in `plans/retries-repair-architecture-implementation-plan.md`.
- Verification: TEST-201 through TEST-211; WEB-TEST-071 through WEB-TEST-073.
- Supersedes: recovery/defaulting and attempt-diagnostic clauses of ADR-HLLM-014 and ADR-HLLM-018; accounting, ownership and search contracts remain unchanged.

## 1. Problem and decision

Before this change, the public client, retry package and two editors independently
interpreted retry defaults. Runtime nested retry work inside a backup graph and
tracked dispatched repair state in parallel maps. A repair request could survive
a transport retry while its repair flag and target metadata reset. Parsing changed
numeric strings and silently repaired syntax through a separate Gemini path or
jsonrepair. The provider repair envelope required metadata that was discarded.

Use one complete RecoveryPolicy, one selected target, one execution loop and one
shared editor. The public Go API exposes the policy owned by internal/retry;
profiles and runtime use the same type. Go owns one default constructor and
semantic validator. REST verifies required field presence. Execution requires a
complete policy; it does not inherit missing fields or silently restore defaults.

The policy contains maxAttempts (1..10), retryOn (a unique array of network,
rate_limit, server_error, empty_response and provider_retry), repairInvalidOutput
(Boolean), and backoff (required baseDelayMs 0..60000 and maxDelayMs 0..600000,
maximum >= base). New defaults are four attempts, all five transient categories,
repair enabled and 500/8000 ms. False, zero and an empty retry list are explicit.
GET /api/v1/profiles returns defaults.recoveryPolicy beside profiles. Phoenix
copies a complete profile/default policy and uses one local draft serializer.

Every attempt uses the original selected profile/model/endpoint/options. Remove
backup routing and repair escalation, their graph validation, fields and UI.
Invalid structured output either fails or requests repair against the original
schema. It never repeats the original prompt as an alternative repair strategy.
One JSON decoder preserves number precision and requires EOF; one validator
checks initial and repair responses without coercion or heuristic salvage.
Numeric type/enum validation compares decimal digits and exponents without
expanding powers or changing returned provider values.
Protocol response extraction and payload serialization keep their current owners.

Current prepared work carries request, operation and repair feedback together.
Transient retries repeat that exact work. Attempts, waits, target identity and
provider-used state are recorded directly from it. Each started slot consumes
the one call-wide budget; preflight failure consumes none. The caller context
bounds dispatch and waiting. The wait is max(capped exponential jittered backoff,
valid Retry-After on 429/503). Zero disables calculated delay; a server minimum
is never shortened by the backoff cap. No scheduler/cooldown registry is needed.

Repair feedback contains the original task, latest invalid output and at most
32 validation errors / 8 KiB of validation feedback, within existing request and
output limits. Prior output is data and cannot change target or tool authority.
Repair responses contain the direct original-schema value, without metadata.

## 2. Current owners and deletion inventory

| Behavior | Baseline surfaces | Resulting ownership/removal |
| --- | --- | --- |
| Policy/defaults | types.go, client.go, internal/retry/retry.go, internal/profiles/profiles.go | One policy/default/validator; remove RetryPolicy, StructuredRepairPolicy, RepairEscalation, retryConfig and runtimeRepairPolicy conversions. |
| Selection/dispatch | internal/runtime/execute.go, types.go, backup.go; client.go runtimeProfiles | Remove BuildBackupPlan, ProfileNode, BackupEligible, backup depth/cycle validation, nested retry loops and repair/target/provider-used maps. |
| Parsing/repair | internal/schema/schema.go, internal/runtime/repair.go, internal/providers/payload.go | Remove numeric-string normalization, Gemini JSON salvage, jsonrepair, ExtractRepairData and repairEnvelopeSchema; validate direct values. |
| Observations | internal/runtime/types.go, client.go, types.go, internal/traces/traces.go | Retain one global attempt number; remove retryLocalNumber and backupIndex from current records and projections. |
| REST/storage | internal/gateway/run_service.go, resources.go, profile_service.go, profile_resources.go, httpapi/resources.go; api/openapi.yaml | Complete current policies, profile/state/bundle v2, RunResult v3, strict decoding and backend defaults. Remove result read normalization after migration. |
| UI | ProfileWidgetState, ProfileDefaults, ProfileWidgetComponent, ProfilesLive, WorkspaceLive, HardenAPI, LlmDiagnosticsWire, app.css | One draft transform/serializer and rendered control/help/style owner; delete mirrored defaults, parse retry, backups and escalation controls. |
| Owned callers/configuration | profile probes; gateway run service; Go examples/tests; embedded profile catalog; config/llm-profiles.local.json; scripts/shared-profiles.mjs; cmd/harden-llm-gateway config/import paths | Supply complete current policies explicitly; update maintained files and reject old imports. Do not inspect or copy secret values into evidence. |
| History/cache | gateway resources, LlmDiagnosticsWire/LlmTraceProjection, provider response projection | One result schema for live/history/trace; no old-request execution converter. Structured projection v1 becomes v2 with no old-key lookup. |

Native/Jina web-search routing, model catalog availability, infrastructure retry
policies and unrelated use of the word fallback are outside this deletion scope.
Search/cache/accounting continue to follow ADR-HLLM-018 and ADR-HLLM-019.

## 3. Data and deployment transition

One ordinary embedded migration, 0006_recovery_policy.sql, uses the existing
PostgreSQL advisory lock, transaction and applied-version record. Profile/state
documents become version 2 with complete policies. Historical absent-field
values are mapped once using the old contract; explicit false/zero are retained.
For duplicate profile aliases, a present flat field takes the existing precedence
over its nested counterpart. Invalid types/ranges abort the transaction with a
document/field identifier. No converter service, CLI, mapping table, resolution
manifest, runtime compatibility reader or second active defaults path is added.

Canonical stored RunResult becomes v3 by removing retryLocalNumber/backupIndex
from attempts and normalizing null attempts to an empty array. All other result
values, target snapshots, output and accounting are preserved. Original request
JSON, credential ciphertext, independent preferences, ownership and immutable
artifact exports remain unchanged. History/trace/live results share v3; old
requests remain evidence and cannot run unless they meet the current input
contract. There is no alternate history decoder or implicit policy conversion.

Prepare current callers/configuration, stop old writers, take the normal
database/configuration checkpoint, migrate, then start matching components.
External configuration/bundles must be edited into the current format before
cutover. Existing sync-profiles retains its provisioning purpose. Rolling an old
binary onto migrated data is unsupported; restore the matching whole checkpoint.
This ADR does not authorize a production cutover or deletion of historical data.

## 4. Verification and accepted trade-offs

The implementation plan defines same-command RED/GREEN cases and existing fast,
integration/race and browser-free release gates. No new eval package/runner or
arbitrary case-count/percentage target is introduced. Actual provider work,
strict value preservation, repair identity, editor agreement and exact migration
changes are the acceptance oracles. Browser/live-model quality is not inferred.

All four implementation phases and their source/gate evidence are recorded in
Section 11 of `plans/retries-repair-architecture-implementation-plan.md`.
The final ownership audit removed the retired editor discriminator and its
per-kind pending maps/forwarding helpers. State and individual-profile responses
use the shared current-policy wire checker; backend field errors reach the
shared editor without a second semantic validator.

This intentionally changes utility-llm parity for fallback/escalation, implicit
defaults, provider-retry opt-out, capped Retry-After, JSON salvage/coercion and
repair envelopes. The manifest records these differences without rewriting the
captured source evidence. Automatic cross-model availability and permissive
syntax acceptance are removed; explicit policy and a smaller supported surface
are preferred. Changing these acceptance controls requires a later ADR.
