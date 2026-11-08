# Issue #137: RCA and solution options

## 1. Scope and evidence

Analysis on 2026-11-07 against Harden `3f8a5173086ea245841551fe988f6a3b03cff7f8`. This is analysis only. No application fix, main-branch change, deployment, browser test or paid inference is claimed. Local witnesses assert the observed current behavior, not that the requested feature is fixed.

## 2. Causal chain

This is reproducible metadata loss across multiple layers, not simply one missing response field:

- [internal/gateway/httpapi/handlers.go:220–227](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/internal/gateway/httpapi/handlers.go#L220-L227) rejects every nonempty `include`, including `web_search_call.action.sources`, before dispatch. The provider mapper already requests this source projection automatically for native Responses search at [internal/providers/payload.go:603–634](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/internal/providers/payload.go#L603-L634). Therefore the initial fault is the public boundary, not that all upstream search requests omit sources.
- [internal/providers/search_result.go:44–74](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/internal/providers/search_result.go#L44-L74) reduces upstream search calls to executed status and deduplicated sources; it retains no call IDs, action/query, per-call status or original output position. The canonical [internal/runtime/search.go:12–30](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/internal/runtime/search.go#L12-L30) has no fields for these facts. Gateway-only reconstruction cannot recover them honestly.
- Search citations are admitted only for the selected text part **and `callType == "text"`** at search_result.go:68. Structured output loses valid available offsets even when raw JSON text was unchanged. Projection/repair can also change text, so simply removing this condition is unsafe.
- [internal/gateway/httpapi/responses.go:91–128](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/internal/gateway/httpapi/responses.go#L91-L128) builds fresh output items, hardcodes empty annotations and never consumes `result.Search`. [internal/gateway/httpapi/responses.go:29–33](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/internal/gateway/httpapi/responses.go#L29-L33) has no Chat citation field. `hardenExtension` at lines 190–196 does not restore it.
- [internal/gateway/httpapi/stream.go:81–135](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/internal/gateway/httpapi/stream.go#L81-L135) reuses the lossy completed response and emits its own empty annotation part. Fixing only final JSON would leave streamed items/events inconsistent.
- Chat has an additional request-side gap: [internal/gateway/httpapi/requests.go:23–43](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/internal/gateway/httpapi/requests.go#L23-L43) has no `web_search_options`; [internal/providers/payload.go:585–600](https://github.com/prls-co/harden-llm/blob/3f8a5173086ea245841551fe988f6a3b03cff7f8/internal/providers/payload.go#L585-L600) selects native search only for Responses/Gemini/Anthropic, not Chat. Its supported search surface needs an explicit contract, beyond adding output annotations.

**Local witnesses:** documented include → HTTP 400; a result with executed search/source/citation → public Responses JSON with no search call and empty annotations; the same upstream JSON fixture → text mode retains 1 citation, structured mode retains 0. Existing provider search-evidence test passed. These are deterministic owner-layer witnesses, not SDK/end-to-end/live qualification.

Official [OpenAI web-search documentation](https://developers.openai.com/api/docs/guides/tools-web-search) confirms separate Responses search-call items and message annotations, source include, and Chat's distinct nested citation shape.

## 3. Options and recommendation

1. **Recommended:** preserve bounded typed search provenance from upstream normalization through canonical result/cache to public projection. Add the supported include allowlist, retain actual call/action/status/order, emit API-specific citations, and use the same canonical output for streamed item/completion events. For structured results retain offsets only when bound to the exact returned text; invalidate/remap with demonstrated equivalence after serialization or repair. Keep JSON valid. Never invent missing calls or treat configured search as executed retrieval.
2. A small gateway-only patch can expose currently retained text citations and sources, but cannot satisfy full call/action/order or structured-output acceptance. It is a separately labeled partial delivery, not closure.
3. Raw upstream passthrough would retain provider envelopes but bypass normalization/repair/provider portability and may leak unsupported data. Not recommended.

Update OpenAPI, typed clients that need search output (the new narrow ResponsesOutput currently exposes only model/output_text/harden), canonical/cache serialization and response-projection identity together. Older cache rows cannot supply discarded provenance; choose a cache projection version/invalidation policy instead of fabricating it. Producer metadata on a cache hit is not a fresh search.

## 4. Verification and blockers

Required deterministic coverage: representative upstream fixture → public handler with diagnostics off, include propagation, call/item order, streamed/nonstreamed parity, exact citation character positions including Unicode, structured raw/serialized/repaired output, missing metadata, search off and cache replay, supported Chat request/citation shape, plus SDK standard-field decoding. Preserve provider-specific behavior and existing test oracles.

No user input is needed for deterministic implementation. Confirm the supported Chat search request/model contract before promising full Chat parity; provider account/tool availability and live acceptance are later independent gates. Priority: important wherever retrieved provenance is a product requirement; effort substantial. It is not fixed by PR #148.
