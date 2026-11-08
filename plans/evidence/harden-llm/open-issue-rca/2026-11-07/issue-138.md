# Issue #138: RCA and solution options

## 1. Scope and evidence

Analysis on 2026-11-07 against Harden `3f8a5173086ea245841551fe988f6a3b03cff7f8`. This is analysis only. No application fix, main-branch change, deployment, browser test or paid inference is claimed. Local witnesses assert the observed current behavior, not that the requested feature is fixed.

## 2. Causal chain and current consumer relevance

1. [api/openapi.yaml:1807–1833](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/api/openapi.yaml#L1807-L1833) forbids extra fields, requires `userPrompt`, and defines no ordered messages. [httpclient/types.go:15–29](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/httpclient/types.go#L15-L29) and [stateful/run.go:13–26](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/stateful/run.go#L13-L26) mirror that prompt-only contract.
2. The authenticated Run handler decodes that exact type at [internal/gateway/httpapi/reference.go:379–401](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/internal/gateway/httpapi/reference.go#L379-L401); its strict decoder rejects unknown fields at [internal/gateway/httpapi/reference.go:109–114](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/internal/gateway/httpapi/reference.go#L109-L114). `validateRunInput` additionally requires a nonempty user prompt and converts to prompt-only profile inputs at [stateful/run.go:213–237](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/stateful/run.go#L213-L237).
3. **The decisive mapping is [stateful/resolve.go:287–294](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/stateful/resolve.go#L287-L294):** it constructs an optional system message followed by exactly one user message. There is no place for an assistant's prior invalid output and the following user validation feedback. Flattening them into text cannot preserve roles.
4. This is an API/composition gap, not a demonstrated provider or cache-hash bug. The existing cache hashes the prepared operation payload at [internal/cachekey/cache.go:83–97](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/internal/cachekey/cache.go#L83-L97); once full ordered messages reach that payload, preserve them there and test identity differences rather than adding a second cache scheme.

**Local witness:** the current OpenAPI validator rejects an otherwise shaped Run request with added `messages`. This confirms contract rejection; it is not full profile/store/provider execution coverage.

**Material tracker change:** [NER-Go #3](https://github.com/prls-co/ner-go/issues/3) is now CLOSED and its current body records one typed Harden Responses path, one bounded application correction, PR #148 adoption, and the compatible pin `v0.0.0-20261108004103-c884eb8f7b6d`. [httpclient/responses.go:17–46](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/httpclient/responses.go#L17-L46) supports ordered input on ordinary `/v1/responses`, without a profileId. This removes the old assumption that this Run extension blocks that NER migration. [Query Analytics #10](https://github.com/prls-co/query-analytics/issues/10) still contains the older profile-aware Run obligation; its current product acceptance must be reconciled rather than silently declared complete.

## 3. Options and recommendation

1. **Recommended next action:** reconcile the remaining Query Analytics caller against its selected NER release. If all semantic-correction execution is now worker-owned Responses, close this issue as superseded with explicit consumer confirmation and release references. This is a requirement reconciliation, not proof that ordered Run was implemented.
2. If a real consumer still requires profile selection plus ordered continuation, extend existing Run with one canonical ordered-message representation. Make prompt mode and message mode mutually exclusive, permit legacy prompt-only calls unchanged, define role/sequence/UTF-8/encoded-size rules in OpenAPI, and update the HTTP client, stateful cloning/validation/resolve, recorded-input schema and history projection together. Keep profile/schema/reasoning/search/recovery options and key scope authoritative. No automatic semantic retry.
3. Standardize that consumer on ordinary Responses only if it accepts worker-owned model/options and the absence of reference profile/history behavior. This is a product contract decision, not a transparent adapter substitution.

Do not duplicate provider execution in NER or add a continuation service. Do not reinterpret JSON repair/rerun as the consumer's one authorized semantic correction.

## 4. Verification and blockers

For option 2, exercise exact system/user/assistant/user bytes through the production provider mapper; legacy prompts; invalid/conflicting sequences; request limits; cache identity for distinct prior output/feedback; cancellation/terminal-stream/history-tail uncertainty with no replay. The existing history serializes RunInput at [stateful/run.go:185–209](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/stateful/run.go#L185-L209), so recorded-contract coverage is required too.

**Blocker:** confirmation of a remaining profile-aware consumer requirement after NER's Responses migration. No credential or paid call is needed to resolve that from current consumer code/owners. User decision is needed only if both product modes remain desired and current consumer requirements do not settle it. Priority: conditional; no longer assume this is the next NER blocker. Effort: substantial if option 2 remains required.
