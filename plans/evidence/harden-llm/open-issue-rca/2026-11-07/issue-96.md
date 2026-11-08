# Issue #96: RCA and solution options

## 1. Scope and evidence

Analysis on 2026-11-07 against Harden `3f8a5173086ea245841551fe988f6a3b03cff7f8`. This is analysis only. No application fix, main-branch change, deployment, browser test or paid inference is claimed. Local witnesses assert the observed current behavior, not that the requested feature is fixed.

## 2. Causal chain

The existing engine can bind other profiles for semantic repair, but has no rate-limit-triggered provider-target transition:

1. [internal/retry/retry.go:39–54](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/internal/retry/retry.go#L39-L54) defines `retryOn`, JSON repair and rerun, with no separate rate-limit fallback target. A rerun target is not a catch-all provider fallback.
2. [internal/runtime/recovery_execute.go:270–275](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/internal/runtime/recovery_execute.go#L270-L275) separates semantic parse transitions from transport retries. Only semantic failure reaches `prepareNextPlannedWork` at [internal/runtime/recovery_execute.go:340–355](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/internal/runtime/recovery_execute.go#L340-L355) and can select the rerun target (lines 431–441).
3. For a retryable 429, [internal/runtime/recovery_execute.go:401–423](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/internal/runtime/recovery_execute.go#L401-L423) waits using Retry-After and loops with the same prepared work. The ordinary path explicitly preserves prepared work on transport retry at [internal/runtime/execute.go:210–240](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/internal/runtime/execute.go#L210-L240). Setting a direct OpenAI rerun profile cannot implement this issue's trigger.
4. Profile-bound resolution already exists at [stateful/resolve.go:86–133](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/stateful/resolve.go#L86-L133) and should be reused. The shipped Luna preset is **CPA**, with deployment upstream `cpa` at [stateful/presets.json:3326–3329](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/stateful/presets.json#L3326-L3329); choosing its model name alone does not switch to the official API.
5. Known rejection and ambiguous outcome must stay distinct: [stateful/batch/provider_observation.go:31–48](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/stateful/batch/provider_observation.go#L31-L48) forbids retry on unconfirmed ordinary dispatch; [stateful/batch/activities.go:231–260](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/stateful/batch/activities.go#L231-L260) separates account restrictions and partial-output rejection from clean throttling. A new fallback must not route partial/unknown inference or billing/auth restriction through another provider merely because status is 429.

**Local witness:** configure maxAttempts=2, retryOn=rate_limit, rerun target `fallback`; script a 429/Retry-After=1s followed by success. Actual models are **[generation, generation]**, attempts=2, planned wait=1s. The fallback model is never prepared. Existing gateway Retry-After control passed. This is a deterministic core witness, not live provider evidence.

The [official model page](https://developers.openai.com/api/docs/models/gpt-6-luna) currently documents the exact `gpt-6-luna` ID and Responses/structured-output support. This confirms the documented model, not access for any specific credential. The earlier issue comment's queue-age overflow experiment concerned undispatched items and did not qualify per-request native-429 fallback.

## 3. Options and recommendation

1. **Recommended if fallback remains a requirement:** add one explicit, opt-in rate-limit fallback target to the existing recovery contract and engine, resolved from the same-key managed profile. Enter it at most once after a confirmed CPA rate-limit rejection, honor Retry-After/deadline, and debit every dispatch from the same global attempt budget. Preserve the original semantic request; validate the fallback's tools/options before dispatch. Keep actual serving target, usage/cost, both failure contexts, traces and cache provenance attributable. Define whether any remaining retry budget applies after fallback; never create a provider loop.
2. Return existing retry guidance and retain CPA-only execution. Smallest operational choice if CPA admission now meets needs, but requires explicitly deferring/closing this requested feature rather than claiming existing retries implement it.
3. Consumer-side resubmission/fallback or reusing JSON rerun as a throttle handler duplicates ownership and risks incorrect replay/accounting. Not recommended.

## 4. Verification and blockers

Deterministic cases must prove CPA 429 → direct OpenAI exactly within budget; non-rate-limit/auth/billing/schema errors and partial/unconfirmed outcomes never switch; Retry-After/deadline/cancel; unsupported capabilities; exhausted budgets; fallback failure retains both outcomes; actual target/accounting/cache/history/trace attribution. Cover text and structured paths rather than implementing only the staged structured executor.

**Before enabling:** select/provision a separately scoped official OpenAI managed profile/credential through the existing credential path and confirm account model access. No credential is needed for deterministic development; do not send one in an issue. **User/product choice:** retain this paid fallback requirement and define permitted extra spend/attempt behavior if current policy does not already establish it. No live call was made. Effort: substantial core/contract change; not a quick configuration-only closure.
