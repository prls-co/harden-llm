# ADR-HLLM-019: Cached Web-Search Routing

- Status: Accepted
- Date: 2026-09-12
- Requirements: REQ-002, REQ-006, REQ-011, REQ-019
- Verification: TEST-011, TEST-012, TEST-025, WEB-TEST-007, WEB-TEST-044, WEB-TEST-070

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

Decision confirmed 2026-11-12 in [issue #157](https://github.com/prls-co/harden-llm/issues/157):
manual cache controls are sufficient for search answers. Keep the existing
generic expiration and `cache`, `refresh`, and `off` semantics. Callers accept
cached answers or explicitly request new execution. No search-specific TTL,
maximum-age check, or additional freshness metadata is required.

Decision confirmed 2026-11-16 in [issue #156](https://github.com/prls-co/harden-llm/issues/156):
keep Anthropic native search optional and expose observed execution. The
`required` search choice remains a documented hint on Anthropic; it does not
disable thinking, introduce a forced-tool model allowlist, retry an answer
without search, or switch providers. Consumers decide whether the returned
evidence meets their requirements. TEST-455 adds explicit live search selection
across the shipped catalog, with deterministic TEST-548 protecting its reporting
and execution settings. The decision does not establish that every preset or
provider route has passed live qualification.

The gateway has one additional fixed-host outbound dependency and must keep
its key in the protected environment. Search results are treated as
untrusted prompt material, bounded before injection, and excluded from logs,
cache identity, and browser state.

Search intent is not proof of execution. `result.search` preserves actual tool
evidence and citations on both fresh and cached results. Search fees have an
explicit unavailable cost status, separate from model token accounting. Jina
failures before the model request have `providerUsed:false`. Bounds: one query
without query-planning LLM calls, 4096 UTF-8 bytes, 20-second search timeout,
512-KiB HTTP response (`Accept: text/plain`, `X-Respond-With: no-content`) and
64-KiB injected context with the completed-retrieval status prefix, all under
the run deadline. `CPA claude-haiku-4-5-20251001` uses `anthropic-messages` (so
text search uses optional native Claude search and structured search uses the
existing Jina fallback while preserving strict JSON schema output), and the
unavailable `CPA gpt-5.5` starting preset is removed until upstream restores
that exact model ID.

For native Responses search, the adapter uses the explicit `final_answer`
message, including when it is empty. With unlabeled messages, only assistant
content following the last completed search can supply the answer. Commentary
cannot supply it. JSON and SSE use the same selected message for text,
citations and diagnostic final-text size. A completed search without an
eligible answer yields `empty_response`; the private operation and normalized
search evidence retain what was observed. An optional call that performs no
search can still return a valid answer.

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
`TestCacheReplay` verifies cache reuse, refresh bypass/write, and off bypassing
both reads and writes. `TestWebSearchCacheHitSkipsJinaFallback` verifies that a
hit invokes neither search nor the model.
`TestSearchCachePersistenceProjection` now requires native/Jina evidence and
inline citations to survive the real serialization path without another call.
It also verifies that manual refresh replaces both answer and evidence, and
that an uncached call leaves the refreshed entry available for later replay.

The Phoenix diagnostics decoder must also accept the optional `search` object
on run, History and trace responses. A successful native-search run was stored
by the gateway but rejected by the old exact-key decoder, producing the UI's
unknown-outcome warning and preventing History recovery. WEB-TEST-070 exercises
the strict decoder and the real Req-to-LiveView boundary with native/Jina
evidence, including cached evidence with zero current provider usage. Unknown
fields and malformed sources/citations remain errors; do not fix this by
discarding search metadata or weakening all response validation. Browser
verification can replay the already completed cached run without repeating
the paid search.
