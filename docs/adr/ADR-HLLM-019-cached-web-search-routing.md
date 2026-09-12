# ADR-HLLM-019: Cached Web-Search Routing

- Status: Accepted
- Date: 2026-09-12
- Requirements: REQ-002, REQ-006, REQ-011, REQ-019
- Verification: TEST-011, TEST-012, TEST-025, WEB-TEST-007, WEB-TEST-044

## Context

The workspace needs an explicit, compact web-search control and the REST run
contract needs the same capability for cURL callers. Search must remain
server-owned: provider credentials and the Jina credential cannot reach the
browser. Development also relies on the operation cache to avoid repeated
provider and search spend.

## Decision

- Add an optional `webSearch` boolean to the root request, REST run request,
  client state, and workspace widget. The `🌐` control sits after Reasoning and
  before the cache and profile-config controls.
- Treat `supportsWebSearch` as a profile capability. An explicit capability is
  authoritative; omission and explicit `false` select fallback in REST and Go.
  The managed CPA, OpenAI, Google, Claude and Sonar presets declare native support.
- On a CPA/OpenAI Responses route with native capability, append the Responses
  `web_search` hosted tool and select that specific tool (not generic required).
  Gemini uses `google_search`, Claude text uses `web_search_20250305` with at most
  three uses and thinking-compatible auto choice, and Sonar uses `disable_search`.
  Claude structured output falls back because native search citations conflict
  with strict output. On every other requested
  search route, call Jina Search from the gateway with the protected
  `JINA_API_KEY`, bound to the fixed `s.jina.ai` host, and inject bounded,
  clearly marked untrusted reference text into the provider request.
- Cache lookup happens before either search or provider execution. A fallback
  operation includes its mode and original query in the cache identity;
  search results are not part of the key. A cache hit skips Jina and the LLM;
  refresh recomputes search and provider output and overwrites the same key.
  Search remains off when `webSearch` is false.
- A successful fallback result is reused across retries, repairs and backups
  within one logical call. Failed search results are not cached, and search and
  provider errors remain subject to their existing bounded retry policy.

## Consequences

The UI and cURL paths share one server-owned boolean and one cache contract.
Native-capable profiles, including CPA, avoid an additional Jina request;
non-native routes require `JINA_API_KEY` only on a cache miss. Cached search
answers can become stale until refresh, which is intentional for development
cost control.

The gateway has one additional fixed-host outbound dependency and must keep
its key in the protected environment. Search results are treated as
untrusted prompt material, bounded before injection, and excluded from logs,
cache identity, and browser state.

Search intent is not proof of execution. `result.search` preserves actual tool
evidence and citations on both fresh and cached results. Search fees have an
explicit unavailable cost status, separate from model token accounting. Jina
failures before the model request have `providerUsed:false`. Bounds: one query
without query-planning LLM calls, 4096 UTF-8 bytes, 20-second search timeout,
512-KiB HTTP response and 64-KiB injected context, all under the run deadline.

CPA v7.2.135's translator accepts hosted search on `/v1/responses`, and its local
contract fixtures assert completed search SSE events. Its translator overwrites
`include`, so citations must be collected from message annotations even when the
optional complete source list is missing. This implementation does not add the
separate `/v1/alpha/search` path or silently switch to Jina after native errors.

References: [OpenAI hosted search](https://developers.openai.com/api/docs/guides/tools-web-search),
[Gemini grounding](https://ai.google.dev/gemini-api/docs/generate-content/google-search),
[Claude search](https://platform.claude.com/docs/en/agents-and-tools/tool-use/web-search-tool),
[Sonar search controls](https://docs.perplexity.ai/docs/sonar/filters),
and `/home/kirill/p/CLIProxyAPI-setup/tests/contract/responses_contract.sh`.

## Migration, rollback, and verification

Existing requests default to search off and retain their existing cache mode.
Removing the `webSearch` field and `🌐` control disables the feature without
changing non-search cache entries. Deployments must add `JINA_API_KEY` only to
trusted gateway environments; the local ignored `.env` may contain a separate
development bearer token and Jina key. Deterministic provider, gateway, and
LiveView tests run through `make test-fast`; live provider and browser
certification remain separate opt-in gates.

The cache tests cover both the runtime lookup boundary and the public
`CacheRecord` JSON projection. A deployed regression showed that testing only
an in-memory runtime cache misses dropped fields in `client_cache.go`.
`TestSearchCachePersistenceProjection` now requires native/Jina evidence and
inline citations to survive the real serialization path without another call.
