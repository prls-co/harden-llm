# OpenAI Web-Search Parity Implementation Plan (Issue #137)

## 1. Metadata and rules

- Issue: <https://github.com/prls-co/harden-llm/issues/137>.
- Plan ID: `PLAN-HLLM-WEB-SEARCH-PARITY-001`, version 3.1, 2026-11-09.
  Version 3 resolves the plan review (section 13). Version 3.1 adds the
  accepted minimum fixes and implementation sequence in section 14.
- Base revision: `origin/main` at `33a0924`. Every `file:line` below is from
  that commit unless stated otherwise; search for the quoted symbol if lines
  have moved. Section 14 was researched at `512f6ef`; implementation starts
  from current `origin/main`, not the historical baseline.
- Owner decisions: canonical provenance (RCA option 1) with request-side
  parity; Chat search through `web_search_options` for any model (A2);
  standard `tool_choice` semantics; cache replay keeps provenance; one probe of
  citation index units, run only after an explicit confirmation.
- Test catalog: `SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001`. Test IDs are allocated
  in Phase 0 (section 5.1). This plan uses the placeholders `TEST-534` to
  `TEST-538`.

### 1.1 Implementer rules

1. Read `AGENTS.md` and `docs/liveview-go-testing-guidelines.md` first.
2. Do the phases in order. Each phase ends with its tests passing, `gofmt -l .`
   printing nothing, and one commit. Every OpenAPI change ships in the same
   commit as the wire change it describes, because the typed client validates
   requests and responses against the embedded schema
   (`httpclient/responses.go:108`, `httpclient/client.go:124`).
3. Write each phase's tests first and confirm they fail for the expected
   reason.
4. Never weaken an existing assertion. The only intended changes to existing
   behavior and oracles are listed in section 4.9.
5. Never invent provenance. Section 4.2 is the complete list of what may
   produce a search call.
6. One way of doing things. Add no compatibility branches, fallbacks, feature
   flags or adapters. Add validation only where this plan defines its outcome.
   If the code differs from this plan, stop and report the difference.
7. No browser, public network or real provider call during the phases. The
   only paid calls are the confirmed probe in section 5.4. Docker is used only
   by the final `make verify` gate in section 11.

### 1.2 Branch and coordination

`origin/dev` (`879106f`) is an ancestor of `origin/main`, 167 commits behind it,
so it is not a valid base. Branch from `main`:

```sh
git fetch origin
git switch -c feature/issue-137-web-search-parity origin/main
```

Before the first push, ask the owner whether `dev` should be fast-forwarded to
`main`, because `dev` is the deploy branch for iteration (`AGENTS.md`).

Open PR #152 (`fix/151-real-provider-qualification`) touches
`internal/providers/payload.go:971` (unrelated to search) and the test catalog,
and it uses `TEST-533`. If #152 merges first, rebase onto `main`. The only
expected conflict is in the catalog.

## 2. Problem and consumers

Standard clients cannot request or receive web-search metadata, so a
retrieved reference cannot be told apart from a recalled one.

| # | Location at `33a0924` | Loss |
|---|---|---|
| L1 | `internal/gateway/httpapi/handlers.go:224` | Every nonempty `include` returns 400 |
| L2 | `requests.go:210` (`decodeTools`), `handlers.go:271–278` | Only a bare `{"type":"web_search"}` is accepted; options, `web_search_preview` and any `tool_choice` return 400 |
| L3 | `internal/providers/search_result.go:13` (`normalizeSearch`) | Search calls collapse into `Executed` plus a source list |
| L4 | `search_result.go:68` | Citations are kept only for text calls |
| L5 | `responses.go:91`, `:57`, `stream.go` | Annotations are always `[]`; there are no search items and no Chat annotations |
| L6 | `requests.go:23` | Chat has no `web_search_options` |
| L7 | `requests.go:326–420` (`decodeResponsesInput`) | Output items sent back as input (stateless multi-turn) are rejected |
| L8 | `httpclient/responses.go:41–52` | The typed client cannot send tools, `tool_choice` or `include`, and does not expose `output` |

Consumers affected, checked in the sibling repositories on 2026-11-08 and
updated where noted on 2026-11-09:

- `ner-go`, the NER consumer named in the issue, calls `/v1/responses` through
  Harden's typed `httpclient` (`internal/ner/harden_caller.go:74,130`), not the
  OpenAI SDK. L8 blocks it.
- The local `query-analytics` candidate, rechecked on 2026-11-09, calls
  `httpclient.Client.Run` through the reference API with `WebSearch: true`
  (`internal/agent/capabilities/harden.go`). It rejects enrichment without
  observed search (`internal/agent/domain/enrichment.go`), so it depends on
  the reference API preserving forced search (section 4.4). This candidate
  source is not evidence of a deployed consumer release.
- `clustering-go` does not use search.

## 3. Supported contract

### 3.1 Request

- Tool types `web_search`, `web_search_2025_08_26`, `web_search_preview` and
  `web_search_preview_2025_03_11` are recognized. The pinned
  `openai-go v1.12.0` only sends the preview spellings.
- **Native OpenAI Responses upstream:** the client's tool object is forwarded
  unchanged. Every option OpenAI defines (`search_context_size`,
  `user_location`, `filters.allowed_domains`, `filters.blocked_domains`,
  `external_web_access`, `return_token_budget`, `search_content_types`,
  `image_settings`, and any later ones) is honored or rejected by OpenAI.
- **Other upstreams** (Anthropic, Gemini, Jina fallback): the tool object may
  contain only `type`, `search_context_size` and `user_location`. These hints
  are ignored. Any other key returns 400 with param `tools`, so no constraint
  is silently dropped.
- `include`: only `web_search_call.action.sources`. Any other value, including
  `web_search_call.results`, returns 400.
- `tool_choice`: see section 4.4.
- Chat `web_search_options`: `search_context_size` and
  `user_location{type:"approximate", approximate{country, region, city, timezone}}`.
  These are all hints, so they are accepted for every upstream.

### 3.2 Response

- `web_search_call` items: `id`, `status` and `action` (`type`, plus `query`,
  `url` and `pattern` as reported). `action.sources` (`[{"type":"url","url"}]`)
  appears only with the `include` value above.
- `url_citation` annotations (`type`, `start_index`, `end_index`, `url`,
  `title`) on the Responses message and, nested under `url_citation`, on the
  Chat message.
- **Not supported:** image results, non-URL sources (OpenAI's `oai-sports`,
  `oai-weather` and `oai-finance` feeds), Anthropic and Gemini citations, and
  sources that the producer did not associate with a call (section 4.2). These
  are omitted, and `incomplete` is set where section 4.3 says so.

Shapes were checked against `openai-go v1.12.0` and the OpenAI web-search
guide on 2026-11-08. The guide names the third action `find_in_page`, while
the pinned SDK names it `find`. Harden copies the upstream action type
unchanged, so neither name is hard-coded.

## 4. Design

### 4.1 Canonical model

`runtime.SearchResult` carries typed calls through cache, history, the
reference API and `/v1`, unchanged. Citations are bound to the exact text they
index by `CitationTextSHA256`. The gateway emits annotations only when the
returned text has that hash, which covers repair, re-serialization and cache
replay with one rule.

### 4.2 Evidence policy: what produces a call

A call is recorded only when the producer reports it or Harden itself ran it.
Nothing else creates a call.

| Producer | Evidence of a call | `ID` | `Status` | `Action` | Call sources |
|---|---|---|---|---|---|
| OpenAI Responses | Each `web_search_call` output item | Upstream `id` | Upstream value | Upstream `type`, `query`, `url`, `pattern`; `nil` when absent | `action.sources[]` URL entries |
| Anthropic | A `server_tool_use` block with `name == "web_search"` **and** a `web_search_tool_result` with matching `tool_use_id` | Upstream `id` | `completed` (a result exists; error results already fail the request at `normalize.go:52`) | `search` with `input.query` | The result's entries |
| Gemini | Each `webSearchQueries[]` entry | Empty | `completed` | `search` with that query | None (Gemini does not associate chunks with queries) |
| Jina fallback | Harden's own search request, when it returned nonempty context | Empty | `completed` | `search` with the exact query Harden sent | URLs found in the returned context |
| Perplexity and others | None reported | — | — | — | — |

- Jina policy: Harden itself performed the search during this request, so a
  `web_search_call` is a true statement about the request.
- Sources that no call owns (Gemini `groundingChunks`, Perplexity
  `search_results`) stay only in the result-level `Sources`. They appear in the
  reference API and history, not in `/v1`, because the standard shape has no
  field for them.
- An Anthropic `server_tool_use` without a matching result is not recorded,
  and `Incomplete` is set.
- Empty IDs become `ws_<callID>_<n>` at the gateway, using the helper that
  already builds `fc_<callID>_<n>` (`responses.go:108`).

### 4.3 Bounds and representability

Add these named constants to `internal/runtime/search.go`. The literal `50`
that is currently repeated at `search_result.go:20,34` and `search.go:55`
becomes `MaxSearchSources`.

```go
const (
    MaxSearchSources    = 50  // unique URLs in Sources (existing limit)
    MaxSearchCalls      = 32  // calls per result
    MaxSearchSourceRefs = 200 // per-call source entries across all calls
    MaxSearchTextBytes  = 4096 // each action query/pattern; equals maxWebSearchQueryBytes
)
```

- A per-call source must also be in `Sources`, and appears at most once per
  call. Duplicates across calls are allowed and count toward
  `MaxSearchSourceRefs`.
- Items are kept in upstream order up to each limit; the rest are dropped.
- A call is representable when its `Status` and `Action.Type` are lowercase
  tokens matching `^[a-z_]{1,32}$`, its `Query` and `Pattern` are at most
  `MaxSearchTextBytes`, and any `Action.URL` passes `ValidSearchURL`. An absent
  action is representable (`Action == nil`); an absent status is not.
- Anything dropped for a limit or because it is not representable sets
  `SearchResult.Incomplete = true`. That includes invalid or non-URL sources
  and the existing 50-URL cap, which today drops silently.
- `Incomplete` appears in the reference API and history (`incomplete`).
  `/v1` has no standard field for it; this is documented.
- One function, `validSearchCall(call SearchCall) bool`, defines
  representability. Normalization uses it to filter, and
  `ValidateSearchResult` uses it to reject cache and history rows.

### 4.4 Tool choice

Web search is a tool for the existing `ToolChoice`, and an empty mode means
the provider default (`auto`), exactly as for function tools and OpenAI.

| `ToolChoice.Mode` | OpenAI Responses (native) | Anthropic (native) | Gemini (native) | Jina fallback |
|---|---|---|---|---|
| `none` | No search tool | No search tool | No search tool | No search |
| `""` or `auto` | Tool, no `tool_choice` (model decides) | Tool, model decides | Tool, model decides | Search runs |
| `required` | `tool_choice: {"type":"web_search"}` (today's forced value) | Model decides | Model decides | Search runs |

These deviations are accepted explicitly:

- On Jina, `auto` searches. Rejecting `auto` would fail OpenAI's default
  request on those connections, and real choice would need an extra model
  round trip.
- On Anthropic and Gemini, `required` is not forced. This is today's behavior
  for the reference toggle.

In every case the response reports what actually happened, as call items, so a
client can see whether search ran.

`required` keeps the specific `{"type":"web_search"}` choice rather than the
string `"required"`. Function tools can arrive through provider options
alongside native search (`web_search_contract_test.go:143`), and `"required"`
would let the model pick one of those instead.

Go callers that relied on `WebSearch: true` forcing search must set
`ToolChoice{Mode: "required"}`:

- the reference API (`stateful/resolve.go:294`), changed in Phase 2;
- the existing tests listed in section 4.9.

The current Query Analytics candidate uses the reference API, whose adapter
sets this choice. It does not need to add a library-only field to `RunInput`.

### 4.5 Output order

- **Retained** upstream items are the `web_search_call` items and the final
  answer message (`finalResponseOutput`, `normalize.go:36`).
- **Intentionally omitted:**
  - commentary messages, by the existing hardening rule that a preamble is
    never returned as the answer;
  - every other upstream item type, which `/v1` does not emit today.
- **Emitted order:** the calls in upstream order, then the message. The final
  message is defined as the last upstream item with content. In a completed
  Responses output, any tool activity after a message makes that message
  commentary, so a call after the final message can only come from malformed
  upstream output. If one occurs, it is still emitted before the message, with
  call order preserved. A test pins this.
- **Anthropic:** calls in block order, then the single joined message, because
  the OpenAI shape has one message item.
- **Streaming:** both streams are generated from the completed response, so
  streamed and non-streamed order are identical by construction.

### 4.6 Stream lifecycle per status

The `added` item always has status `in_progress`; the `done` item is the
final item unchanged. The lifecycle is `[in_progress, searching, completed]`.

| Final status | Events between `output_item.added` and `output_item.done` |
|---|---|
| `in_progress` | `web_search_call.in_progress` |
| `searching` | `.in_progress`, `.searching` |
| `completed` | `.in_progress`, `.searching`, `.completed` |
| `failed` or any other token | `.in_progress` |

Harden buffers the completed result, so these events describe the final
status. They do not describe timing; this is documented.

### 4.7 Cache

- Bump the global `responseProjectionVersion` from `v4` to `v5`
  (`router.go:41`). Every row misses once. There is no conditional version.
- Store `AssistantText` in `cachedProviderProjection` (`client_cache.go:21`).
  Structured cache hits currently re-encode the JSON (`responses.go:145`),
  which would break the citation hash.
- A hit sets `Cache-Status: harden-llm; hit` (RFC 9211). Replayed items
  describe the original answer, and the header says it was not a fresh search.

### 4.8 Compatibility and deployment order

Typed clients validate against the OpenAPI document embedded at build time,
and the affected schemas use `additionalProperties: false`. Every response
field this plan adds therefore breaks older client binaries that receive it.

- **`/v1`:** the new fields appear only when the request asked for search.
  Older typed clients cannot send a search tool, so they are unaffected.
- **Reference API:** `calls`, `citationTextSha256` and `incomplete` appear on
  runs with search. Deploy in this order:
  1. Merge.
  2. Bump `harden-llm` in every consumer that decodes reference runs through
     `httpclient` (`decodeWire("RecordedRunOutput", …)`,
     `httpclient/stream.go:134`), and deploy those consumers. A new client
     still accepts old responses, because the added fields are optional.
  3. Deploy the gateway. The Phoenix frontend is in this repository and deploys
     with it.

### 4.9 Intended changes to existing behavior and test oracles

1. `/v1/responses` with `web_search` and no `tool_choice` means `auto`.
2. Go callers must set `ToolChoice{Mode: "required"}` to force search.
   Update the existing tests that assert the forced value
   (`internal/providers/web_search_test.go:250`,
   `web_search_contract_test.go:43,144`) to set it. Their expected upstream
   value is unchanged.
3. Responses output gains search items and annotations. Chat messages gain
   `annotations` when search was requested.
4. Structured-output citations are kept when the text is unchanged.
5. `Incomplete` is set wherever the existing code silently drops sources.
6. Every cache row misses once.
7. Earlier output items are accepted as input.

### 4.10 Rejected alternatives

| Alternative | Reason |
|---|---|
| Typed request options for every OpenAI search field | A growing per-field adapter; forwarding the tool object to OpenAI is general and smaller |
| Mapping options to Anthropic and Gemini | Extra adapters; constraints are rejected instead |
| Real `auto` on Jina | An extra model round trip for a fallback path |
| A separate search-choice type with a forced default | Duplicates `ToolChoice` and keeps a legacy meaning |
| Search-only cache version | A permanent conditional to save a one-time miss |
| Re-anchoring citations after repair | The hash rule is already correct; it drops instead |
| Assigning unassociated sources to a call, or creating a call from sources alone | Invents provenance (issue #137 section 3) |
| Search data in the `harden` extension | Not required by the issue, and it couples to the 64 KiB extension limit |

## 5. Phase 0 — allocation, catalog, fixtures, probe

### 5.1 Allocate test IDs

```sh
git fetch origin
{ git show origin/main:plans/from_utility-llm/harden-llm-self-hosted-test-spec.md
  for ref in $(gh pr list --state open --json headRefName --jq '.[].headRefName'); do
    git show "origin/$ref:plans/from_utility-llm/harden-llm-self-hosted-test-spec.md" 2>/dev/null
  done; } | grep -o 'TEST-[0-9]\+' | sed 's/TEST-//' | sort -n | tail -1
```

Allocated on 2026-11-09 after fetching `origin`: the highest catalog ID was
`TEST-533`, with no open PRs. The five IDs are `TEST-534` to `TEST-538`.
Recheck for collisions if the base changes before merge.

### 5.2 Catalog

Append section `35. OpenAI web-search parity (issue #137)` to
`plans/from_utility-llm/harden-llm-self-hosted-test-spec.md`, with one entry per
row of section 11 in the existing format (see `TEST-224`). Each new test file
starts with `// SPEC-HARDEN-LLM-SELF-HOSTED-TESTS-001 TEST-5xx`.

### 5.3 Fixtures

Create `internal/providers/testdata/web_search/` with a `README.md` saying
"hand-written from documented OpenAI, Anthropic and Gemini shapes; no live
output". Compute every index with a throwaway Go snippet; never count by hand.

| File | Content |
|---|---|
| `openai_responses.json` | Calls `ws_a1` (`search`, query `q one`, two sources) and `ws_b2` (`open_page`, url `https://example.org/b`), separated by a commentary message, then a final message `Alpha café 🚲 beta.` with annotations over `café` and `🚲` |
| `openai_structured.json` | The same, but the final text is `{"answer":"Alpha café","n":1}` with one annotation over `Alpha café` |
| `openai_edge.json` | A call with no `action`; a call with status `cancelled`; a call with no `status`; a `find_in_page` action; a call **after** the final message; a source `{"type":"api","name":"oai-sports"}`; the same URL in two calls |
| `openai_oversized.json` | Generated by a test helper rather than checked in: 33 calls, 60 distinct URLs, and 210 per-call references |
| `anthropic.json` | Paired `server_tool_use` `srvtoolu_1` and `web_search_tool_result` with two results; an unpaired `server_tool_use` `srvtoolu_2`; a `text` block |
| `gemini.json` | `webSearchQueries ["q one","q two"]` and two `groundingChunks` |

### 5.4 Index-unit probe (paid; run only after explicit confirmation)

Purpose: find the unit of `start_index`/`end_index` for each API
independently. Canonical offsets are code points (today's code,
`search_result.go:68`) regardless of the outcome.

- File: `internal/providers/web_search_unit_live_test.go`, `//go:build live`.
  Never part of CI, `make verify` or deployment.
- Budget: exactly two calls.
  1. Responses, non-streamed, a current web-search model, with
     `tool_choice: "required"`.
  2. Chat Completions, **streamed**, `gpt-5-search-api`, with
     `web_search_options: {}`. Collect every chunk. This one call gives both
     the indices and where streamed annotations appear.
- Prompt: "Begin your answer with exactly `🚲é🚲é — ` and then answer in one
  sentence with a citation: who won the most recent Tour de France?"
- Analysis, per annotation: take the substring under each candidate unit
  (code points, UTF-16 units, UTF-8 bytes). A unit is consistent when, for
  every annotation, its indices are in range, fall on character boundaries, and
  the substring contains the annotation URL's host. OpenAI places inline
  citation links in the text.
- An API's result is **conclusive** only if at least one annotation starts
  after the emoji, so the three units give different offsets, and exactly one
  unit is consistent. Otherwise it is **inconclusive**.
- Outcomes per API:

  | Result | Action |
  |---|---|
  | Code points | No code change |
  | UTF-16 units or bytes | Add one conversion where that API's offsets enter Harden (Responses: normalization) and where they leave it (projection for that API) |
  | Inconclusive | Keep code points and record "unverified" in the docs. Do not re-run without a new confirmation. |

- The result is evidence for those models on that date only. Docs say
  "observed on <date> with <model>", not "guaranteed".
- Chat stream placement: record which chunk carries `annotations`. Harden's
  Chat stream follows the observation. If no annotations were streamed, put
  them in the last content chunk's `delta`.
- Record the models, date, indices, analysis and conclusion in
  `plans/evidence/harden-llm/web-search-parity/<UTC timestamp>/probe.md`. Store
  no keys and no full responses.

## 6. Phase 1 — canonical provenance (`TEST-534`)

### 6.1 Types (`internal/runtime/search.go:12`)

Add to `SearchResult`:

```go
// Calls are the search invocations reported by the producer or run by Harden,
// in upstream order (plan section 4.2). Their sources are a subset of Sources.
Calls []SearchCall `json:"calls,omitempty"`
// CitationTextSHA256 is the hex SHA-256 of the exact text Citations index.
CitationTextSHA256 string `json:"citationTextSha256,omitempty"`
// Incomplete reports that provenance was dropped by a limit or was not
// representable (plan section 4.3).
Incomplete bool `json:"incomplete,omitempty"`
```

```go
type SearchCall struct {
    ID      string         `json:"id,omitempty"`
    Status  string         `json:"status"`
    Action  *SearchAction  `json:"action,omitempty"`
    Sources []SearchSource `json:"sources,omitempty"`
}

type SearchAction struct {
    Type    string `json:"type"`
    Query   string `json:"query,omitempty"`
    URL     string `json:"url,omitempty"`
    Pattern string `json:"pattern,omitempty"`
}
```

Add the constants from section 4.3 and `validSearchCall`. Note on
`SearchCitation` that offsets are code points into the text hashed by
`CitationTextSHA256`.

### 6.2 Validation (`ValidateSearchResult`, `search.go:45`)

Add only these checks:

- `len(Calls) <= MaxSearchCalls`.
- Every call passes `validSearchCall`.
- The total number of per-call sources is at most `MaxSearchSourceRefs`.
- Every per-call source URL is in `Sources`.
- `CitationTextSHA256` is nonempty exactly when `Citations` is nonempty.

Replace the literal `50` at `search.go:55` with `MaxSearchSources`.

### 6.3 Normalization (`internal/providers/search_result.go`)

1. Change `add(raw, title string)` to return whether the URL is in `Sources`
   afterwards. That is true when it was newly added or was already present.
   When it rejects a URL because it is invalid or the 50-URL cap is reached, it
   sets `result.Incomplete = true`. Replace the literals `50` at `:20` and
   `:34` with `runtime.MaxSearchSources`.
2. Add `addCall(call SearchCall, sources []SearchSource)`. It returns without
   recording and sets `Incomplete` when `len(Calls) == MaxSearchCalls` or
   `!validSearchCall(call)`. Otherwise, for each source in order: skip it if it
   is already on this call; append it if `add` returns true and the reference
   total is below `MaxSearchSourceRefs`; otherwise set `Incomplete`. Then append
   the call.
3. Per producer, follow section 4.2 exactly, inside the existing loops:
   - OpenAI `web_search_call`: the source entries are `action.sources[]`
     objects with a string `url`. Any other entry (no `url`, or
     `type != "url"`) sets `Incomplete`.
   - Anthropic: first collect results by `tool_use_id`, then record each paired
     `server_tool_use`. An unpaired one sets `Incomplete`.
   - Gemini: one `addCall` per query, with no sources. Chunks keep using `add`.
   - Jina: one `addCall` with the stored query (6.4) and the URLs found in the
     context.
4. Citations: delete `prepared.callType == "text" &&` from the condition at
   `:68`. When at least one citation is admitted, set `CitationTextSHA256` to
   the hex SHA-256 of that content part's text.
5. Keep the existing `Executed` rules unchanged.
6. Apply section 14.2 in this phase: normalize evidence before structured
   parsing, preserve it on the router's specific parse-failure path, and let
   Jina's source-admission helper see all URL matches.

### 6.4 Jina query computed once (`internal/providers/web_search.go`)

`boundedWebSearchQuery(lastUserText(call.Messages))` is computed at `:50` and
again at `:80`. Add `query string` to `preparedWebSearch`, set it once in
`fallbackWebSearch` (`:44`), and use `search.query` in `results`, in the memo
key and in normalization.

### 6.5 Cache (`router.go:41`, `client_cache.go:21`)

- Set `responseProjectionVersion = "v5"`.
- Add `AssistantText string \`json:"assistantText,omitempty"\`` to
  `cachedProviderProjection`. Write it in `Set` and restore it in `Get`.
  `internal/runtime/execute.go:194` and `recovery_execute.go:159,381` already
  copy it into the record.

### 6.6 Reference contract (same commit)

Add `calls`, `citationTextSha256` and `incomplete` to `ReferenceWebSearchResult`
(`api/openapi.yaml:1239`) with the section 6.1 shapes. Keep
`additionalProperties: false` and leave `action.type`/`status` as plain
strings (section 3.2). Copy the file to
`frontend/packages/harden_llm_ui/priv/openapi.yaml` and check with
`cmp api/openapi.yaml frontend/packages/harden_llm_ui/priv/openapi.yaml`.

### 6.7 Tests — `TEST-534` `TestSearchProvenance*`

Files: `internal/providers/search_provenance_test.go`,
`internal/runtime/search_test.go` and `client_cache_test.go`.

1. `openai_responses.json` through `normalizeDecodedResponse`: exact calls in
   order. The `Sources` union equals the one computed on `main` (capture it
   first).
2. Text and structured fixtures: exact code-point offsets, and the hash equals
   the SHA-256 of the final text. This is the L4 regression.
3. `openai_edge.json`, with one assertion per row:
   - the call without `action` is kept with `Action == nil`;
   - the `cancelled` call is kept unchanged;
   - the call without `status` is dropped and sets `Incomplete`;
   - the `find_in_page` call is kept unchanged;
   - the call after the final message is kept in order;
   - the `api` source sets `Incomplete`;
   - the duplicate URL appears once in `Sources` and on both calls.
4. Oversized input: exactly 32 calls, 50 `Sources` and 200 references are
   kept, in upstream order, and `Incomplete` is true.
5. Anthropic: one call `srvtoolu_1` with two sources, `srvtoolu_2` absent, and
   `Incomplete` true. Gemini: two calls without sources; chunks only in
   `Sources`. Jina: one call with the stored query. Perplexity: no calls;
   sources only in `Sources`.
6. Search off: `nil`, unchanged.
7. `ValidateSearchResult`: one accepting and one rejecting case per 6.2 rule.
8. A cache round trip keeps calls, citations, hash, `Incomplete` and the
   structured `AssistantText` byte-for-byte.

## 7. Phase 2 — request parity (`TEST-535`)

### 7.1 Library request (`types.go:77`, `internal/runtime/types.go:88`, `client.go`)

Add to `hardenllm.Request` and `runtime.Call`:

```go
// WebSearchTool is the caller's OpenAI web_search tool object. Nil means
// {"type":"web_search"}. See plan section 3.1 for how upstreams treat it.
WebSearchTool json.RawMessage `json:"webSearchTool,omitempty"`
```

Copy it where `WebSearch` is copied (`client.go:146`). In `client.go`, reject
it unless `WebSearch` is true and it is a JSON object whose `type` is one of
the four recognized spellings. Define that set once, as
`runtime.WebSearchToolTypes`, and use it everywhere.

### 7.2 Tool choice treats search as a tool

- `tools.go:10`: change `validateTools(tools, choice)` to
  `validateTools(tools, choice, webSearch bool)`. The no-tools branch applies
  only when `len(tools) == 0 && !webSearch`. With search and no function
  tools, modes `""`, `auto`, `none` and `required` are valid. Update the single
  caller, `client.go:104`.
- `internal/providers/payload.go:107`: the guard becomes
  `… && len(call.Tools) == 0 && !call.WebSearch`.
- Add this helper and use it at every place where `call.WebSearch` decides
  whether search runs (`nativeWebSearchEnabled`, `fallbackWebSearch`,
  `operationPayload`, and `router.go:164`):

  ```go
  func searchRequested(call runtime.Call) bool {
      return call.WebSearch && call.ToolChoice.Mode != "none"
  }
  ```

- `addNativeWebSearch` (`payload.go:603`):
  - Append the caller's tool object (decoded), or `{"type":"web_search"}`
    when it is nil.
  - Set `tool_choice: {"type":"web_search"}` only when
    `call.ToolChoice.Mode == "required"`.
  - Keep `addWebSearchSources` unchanged.
- `Router.Prepare` (`router.go:118`): when search is requested but is not
  native OpenAI Responses, return
  `&retry.ValidationError{Field: "tools", Message: "<key> requires an upstream with native OpenAI web search"}`
  for the first tool key outside `type`, `search_context_size` and
  `user_location`. Confirm that `writeCallError` maps it to 400
  (`handlers.go:350`).
- `stateful/resolve.go:294`: set `ToolChoice: hardenllm.ToolChoice{Mode: "required"}`
  when `input.WebSearch` is true.
- Update the tests from section 4.9 item 2.
- Apply the repair transition and regression from section 14.3 in this same
  phase, before committing the newly accepted `required` search behavior.

### 7.3 Gateway (`requests.go`, `handlers.go`)

- `decodeTools` (`requests.go:210`): decode `{"type": string}` first, without
  strict mode. If the request is Responses and
  `runtime.WebSearchToolTypes[type]` is true, keep the raw object as the search
  tool; a second search tool returns 400. Otherwise use the existing strict
  function-tool path. Return the raw search tool instead of the boolean.
- `decodeToolChoice` (`requests.go:245`): accept `{"type": t}` for a recognized
  search type, mapping it to `ToolChoice{Mode: "required"}`.
- `prepareResponses`: delete the `tool_choice` rejection (`handlers.go:275`).
  Keep the search-plus-function-tools rejection (`:271`). Replace the
  `include` rejection (`:224`) with the allowlist from section 3.1, returning
  code `unsupported_value` and param `include`. Store
  `IncludeSearchSources bool` in `preparedResponses` (`handlers.go:211`).

### 7.4 Output items as input (`decodeResponsesInput`, `requests.go:326`)

- `message`: also allow `id` and `status`.
- `function_call`: also allow `status`.
- `output_text` parts (`requests.go:415`): also allow `annotations`.
- `web_search_call`: accept the item and skip it; search results are not model
  context.

These fields are ignored and never forwarded.

### 7.5 OpenAPI and typed client request (same commit)

- `api/openapi.yaml` `/v1` Responses request (around `:2236`):
  - the web-search tool object with the four types and
    `additionalProperties: true`, since OpenAI validates its fields;
  - the `include` enum;
  - the search `tool_choice` object;
  - the input item fields from 7.4;
  - the `include` description at `:184`.
- `httpclient/responses.go:41` `ResponsesInput`: add
  `Tools []json.RawMessage \`json:"tools,omitempty"\``,
  `ToolChoice json.RawMessage \`json:"tool_choice,omitempty"\`` and
  `Include []string \`json:"include,omitempty"\``. The client validates the
  request against `ResponseRequest` (`:108`), which the schema change above
  covers.
- UTF-8: `json.Marshal` silently replaces invalid UTF-8 in strings with
  U+FFFD and copies `json.RawMessage` bytes unchecked, and `api.Validate`
  (`gjson.ValidBytes`) accepts invalid UTF-8. This was verified locally.
  Extend the existing rejection (`:99–106`, `&ProtocolError{}`, no dispatch)
  in two ways:
  - Add each `Include` entry to the existing `utf8.ValidString` checks.
  - Add `!utf8.Valid(body)` to the `:108` condition. That single check covers
    `Tools` and `ToolChoice`, because marshalling makes every other byte valid.
- Scope: the typed client supports structured output only, because `:165`
  validates `output_text` against the schema. It therefore models search calls
  and the message, not `function_call` items.

### 7.6 Tests — `TEST-535` `TestWebSearchRequestParity*`

File: `internal/gateway/httpapi/web_search_request_test.go`, using
`newContractAPI` (`openai_contract_test.go:239`) with a body-recording
upstream. Add a second connection for non-native cases.

1. Each of the four types → the upstream receives the client's tool object
   unchanged, including `filters.blocked_domains` and `external_web_access`.
2. `tool_choice`, following section 4.4: absent or `auto` → no upstream
   `tool_choice`; `required` or `{"type":"web_search_preview"}` →
   `{"type":"web_search"}`; `none` → no search tool.
3. `include` with the sources value → 200. `web_search_call.results` → 400
   naming it, with zero dispatches.
4. A non-native connection with `filters` → 400 `param:"tools"` and zero
   dispatches. With only hints → 200.
5. A previous response's `output` plus a new user message as `input` → 200;
   the upstream receives the message texts only.
6. A reference run with search → upstream `tool_choice: {"type":"web_search"}`.
7. Typed client: `ResponsesInput` with `Tools`, `ToolChoice` and `Include`
   passes `ResponseRequest` validation and reaches the upstream unchanged.
8. Typed client: an `Include` entry with `"\xff"`, or a `Tools` raw message
   containing `"\xff"` inside a string, returns `*ProtocolError` with zero
   requests to the test server.

## 8. Phase 3 — Responses projection, stream, typed client output (`TEST-536`)

### 8.1 Shared helpers (`internal/gateway/httpapi/responses.go`)

```go
func outputItemID(id, prefix, callID string, index int) string
func searchCallItems(result hardenllm.Result, includeSources bool) []any
func annotations(result hardenllm.Result, text string, shape func(hardenllm.SearchCitation) any) []any
```

- `outputItemID` replaces the inline `fc_` code (`responses.go:108`, `stream.go`).
- `searchCallItems` returns one item per call, using the action fields exactly
  as stored. `sources` is `[{"type":"url","url":u}]`, included only when
  `includeSources` is true.
- `annotations` returns `[]any{}` unless the SHA-256 of `text` equals
  `CitationTextSHA256`.

### 8.2 `responsesResponse` (`responses.go:91`)

Add an `includeSources bool` parameter. The output is the search items, then
the message (with `annotations(result, content, responsesCitation)`), then the
function calls.

### 8.3 Cache status

Add `writeCacheStatus(writer, result)` once. It sets
`Cache-Status: harden-llm; hit` when `result.Cache.Served`. Both handlers call
it before writing any body or SSE headers.

### 8.4 Stream from the completed response (`stream.go:81`)

`streamResponses` takes the `final` map from `responsesResponse` and walks
`final["output"]`:

- `web_search_call`: emit the events in the section 4.6 table.
- `message`: emit `output_item.added`, `content_part.added`, the deltas
  (`outputChunks`), one `output_text.annotation.added` per annotation, then
  `output_text.done`, `content_part.done` and `output_item.done`. Every event
  carries `item_id`; the current text events lack it (`stream.go:112`).
- `function_call`: emit the existing events.

Finish with `response.completed` carrying `final`. Delete the second
`outputContent` call and the item rebuilding.

### 8.5 OpenAPI and typed client output (same commit)

- `/v1` Responses output schema (around `:2380`): add the `web_search_call`
  variant (`id`, `status` and `action.type` as strings, the optional action
  fields, and `sources`). Type the `url_citation` annotation in place of
  `{type: object}` (`:2400`). Copy to the frontend `priv/` path.
- `httpclient/responses.go:48`: add `Output []ResponseOutputItem` with these
  types:

  ```go
  type ResponseOutputItem struct {
      ID      string                  `json:"id"`
      Type    string                  `json:"type"`
      Status  string                  `json:"status"`
      Action  *ResponseSearchAction   `json:"action,omitempty"`
      Content []ResponseOutputContent `json:"content,omitempty"`
  }
  type ResponseSearchAction struct {
      Type    string                 `json:"type"`
      Query   string                 `json:"query,omitempty"`
      URL     string                 `json:"url,omitempty"`
      Pattern string                 `json:"pattern,omitempty"`
      Sources []ResponseSearchSource `json:"sources,omitempty"`
  }
  type ResponseSearchSource struct {
      Type string `json:"type"`
      URL  string `json:"url"`
  }
  type ResponseOutputContent struct {
      Type        string               `json:"type"`
      Text        string               `json:"text"`
      Annotations []ResponseAnnotation `json:"annotations"`
  }
  type ResponseAnnotation struct {
      Type       string `json:"type"`
      StartIndex int    `json:"start_index"`
      EndIndex   int    `json:"end_index"`
      URL        string `json:"url"`
      Title      string `json:"title"`
  }
  ```

### 8.6 Tests — `TEST-536` `TestResponsesSearchProjection*`

File: `internal/gateway/httpapi/web_search_projection_test.go`. Run the fixtures
through the real provider path with diagnostics off.

1. Text fixture: items `[ws_a1, ws_b2, message]`, no `sources` key, and exact
   annotations. With `include`, `ws_a1` carries its two sources.
2. Structured fixture: annotations are present, and `output_text` parses.
3. A forced structured repair (existing repair helpers): items are present,
   and annotations are `[]`.
4. Edge fixture: the call after the final message is emitted before it, in
   order; the action-less item has no `action` key.
5. Search off: the body equals the golden captured on `main`.
6. Search requested, upstream reports none: no items, annotations `[]`.
7. Cache: a second identical request returns the same `output`, sets
   `Cache-Status: harden-llm; hit`, and makes no upstream request.
8. Gemini: generated IDs `ws_<callID>_0` and `_1`.
9. Stream:
   - rebuilding `output` from the `output_item.done` and annotation events
     equals the non-streamed `output`;
   - the event sequence for each status in section 4.6 (`in_progress`,
     `searching`, `completed`, `failed`, `cancelled`) is exact;
   - every text event has `item_id`;
   - `sequence_number` increases strictly;
   - `response.completed.response` equals the non-streamed body.
10. Typed client: `Client.Responses` decodes `Output` with exact calls and
    annotations through the real handler.

## 9. Phase 4 — Chat search (`TEST-537`)

### 9.1 Request (`requests.go:23`, `handlers.go:44`)

Add `WebSearchOptions *chatWebSearchOptions \`json:"web_search_options,omitempty"\``
with `search_context_size` and
`user_location{type, approximate{country, region, city, timezone}}`. Strict
top-level decoding (`requests.go:143`) rejects unknown nested fields.

When the field is present, including `{}`:

- set `WebSearch: true` and `ToolChoice{Mode: "required"}`, because OpenAI's
  Chat search models always search;
- set `WebSearchTool` to
  `{"type":"web_search", "search_context_size": …, "user_location": {"type":"approximate", …}}`.

Function tools combined with `web_search_options` → 400 with param
`web_search_options`.

### 9.2 Response (`responses.go:29,57`) and stream (`stream.go:12`)

- Add `Annotations []any \`json:"annotations,omitempty"\`` to
  `chatMessageOutput`, filled by `annotations(result, content, chatCitation)`.
  `chatCitation` returns
  `{"type":"url_citation","url_citation":{start_index,end_index,url,title}}`.
  Non-search bodies stay byte-identical.
- Generate `streamChat` from the `chatResponse` value. Place the annotations as
  section 5.4 records.
- OpenAPI, same commit: Chat `web_search_options` and message `annotations`.

### 9.3 Tests — `TEST-537` `TestChatWebSearch*`

File: `internal/gateway/httpapi/chat_web_search_test.go`.

1. `{}` with a Responses upstream: the upstream receives the native tool and
   `{"type":"web_search"}` as its tool choice, and the response has exact
   `message.annotations`.
2. Options are converted into the tool object.
3. A Chat-protocol upstream: Jina runs once (local searcher stub), and the
   response has no annotations.
4. With function tools → 400. With an unknown nested field → 400. Both make
   zero dispatches.
5. The non-search body equals the `main` golden.
6. Stream: the content and annotations equal the non-streamed message.

## 10. Phase 5 — SDK contract, docs, consumers (`TEST-538`)

### 10.1 Schema validation

Extend `openapi_contract_test.go` so the bodies from `TEST-536` and `TEST-537`
validate against the schema.

### 10.2 OpenAI SDK (`openai_contract_test.go`, `openai-go v1.12.0`)

- `AsWebSearchCall()` and `Action.AsSearch().Query`.
- `AsURLCitation()` indices.
- `action.sources` through `Action.JSON.ExtraFields`, since v1.12 has no typed
  field.
- The streamed `response.web_search_call.completed` and
  `response.output_text.annotation.added` events.
- Chat `message.annotations`.

Do not upgrade the SDK.

### 10.3 Docs

Update `docs/api-and-library.md`, and the `/v1` section found with
`grep -rn "include" docs README.md`, with:

- the supported contract (section 3);
- the tool-choice table and its deviations (section 4.4), and the Go migration
  (`ToolChoice{Mode: "required"}`);
- the evidence policy (section 4.2) and generated IDs;
- `Incomplete`;
- `Cache-Status`;
- the index unit with its evidence basis (section 5.4);
- that streamed lifecycle events describe the final status, not timing;
- the deployment order (section 4.8).

### 10.4 Consumers (release checklist in the PR description)

1. `query-analytics`: keep `RunInput.WebSearch` and verify that the reference
   API supplies the required search choice. Its enrichment requires observed
   search. Update the typed-client dependency as required by section 4.8;
   do not add a library-only `ToolChoice` field to the reference request.
2. `ner-go`: to use search, add the search tool and `include` to the
   `ResponsesInput` built at `internal/ner/harden_caller.go:130`, and read
   `Output`. That is the consumer's change and is not part of this PR.
3. Every consumer that decodes reference runs through `httpclient` bumps its
   dependency before the gateway deploys (section 4.8). Find them with
   `grep -rn "RecordedRunOutput\|StreamRun\|RunOutcome" --include=*.go` in each
   sibling repository.

## 11. Tests and verification

| ID | Name | Command |
|---|---|---|
| TEST-534 | Canonical provenance, normalization and cache | `go test . ./api ./internal/runtime ./internal/providers -run 'TestSearchProvenance' -count=1` |
| TEST-535 | Request parity | `go test ./api ./httpclient ./internal/gateway/httpapi ./internal/providers ./internal/runtime ./stateful -run 'TestWebSearchRequestParity|TestExplicitRecoveryPlanRunsBothBranchesWithFlatHistory' -count=1` |
| TEST-536 | Responses projection, stream and typed output | `go test ./internal/gateway/httpapi ./httpclient -run 'TestResponsesSearchProjection' -count=1` |
| TEST-537 | Chat search | `go test ./internal/gateway/httpapi -run 'TestChatWebSearch' -count=1` |
| TEST-538 | OpenAPI and SDK contract | `go test ./internal/gateway/httpapi -run 'TestOpenAIWebSearchContract' -count=1` |

Before the PR:

```sh
PATH=/home/kirill/.local/elixir-1.20.2/bin:/home/kirill/.local/otp-28.4.3/bin:$PATH
gofmt -l .
go vet ./...
go test ./... -count=1
go test -race ./internal/gateway/httpapi ./internal/providers ./internal/runtime ./httpclient . -count=1
make test-fast
make verify        # Docker; the only Docker use in this plan
git diff HEAD --check
```

There is no UI change and no browser test; state "browser layout not checked".

Acceptance against issue #137 section 4:

| Issue item | Covered by |
|---|---|
| (1) `include` acceptance and propagation | `TEST-535` (3), `TEST-536` (1) |
| (2) Search calls and citations without diagnostics | `TEST-534`, `TEST-536` |
| (3) Streamed and non-streamed parity | `TEST-536` (9), `TEST-537` (6) |
| (4) Structured JSON with valid citation positions | `TEST-534` (2), `TEST-536` (2–3) |
| (5) Search off, enabled-but-unreported, missing metadata, cache replay | `TEST-534` (3–6), `TEST-536` (5–7) |
| (6) Chat Completions citations | `TEST-537` |
| (7) OpenAI SDK consumption | `TEST-538` |

## 12. Out of scope (open one follow-up issue each)

1. Anthropic and Gemini citation offsets.
2. Function tools combined with `web_search` on `/v1`.
3. `web_search_call.results`, image results and non-URL sources.
4. Forcing `required` on Anthropic.
5. A freshness limit for cached search answers.
6. The `reasoning.encrypted_content` include value.

## 13. Review resolutions (version 3)

| Finding | Resolution |
|---|---|
| Stale base branch and colliding test IDs | Base on `origin/main` (`dev` is 167 commits behind); allocate IDs by command against `main` and open PRs (sections 1.2 and 5.1) |
| Invented calls and source attribution | Evidence policy (4.2): no call from sources alone; unassociated sources stay at result level; explicit Jina policy |
| Source bound did not hold | Named limits for calls, unique URLs and total references; truncation is represented by `Incomplete`; duplicate and oversized cases tested (4.3, `TEST-534` 3–4) |
| Search-choice semantics incomplete | Per-upstream table with explicit, documented deviations; `required` keeps the specific tool choice; Go callers, including `query-analytics`, are listed (4.4, 10.4) |
| Ordering and lifecycle undefined | Retained, omitted and normalized items defined (4.5); events per status for all four statuses plus unknown ones (4.6), each tested |
| Probe could not support its claims | A streamed Chat call within the two-call budget, a conclusiveness criterion, a defined inconclusive outcome, a separate result per API, and "observed", not "guaranteed" (5.4) |
| Docker rule contradiction | Docker only for the final `make verify` (1.1 rule 7) |
| Absent or unknown upstream fields | Defined by representability (4.3) and tested (`TEST-534` 3) |
| Implied full OpenAI parity | Explicit supported subset (3); the tool object is forwarded unchanged to OpenAI; other upstreams accept only hints |
| Found during this revision: typed client gap (L8) | `ner-go` uses `httpclient`, so input and output fields are added (7.5, 8.5) |
| Found during this revision: strict-schema clients reject added fields | OpenAPI changes ship with their wire change; deployment order defined (1.1 rule 2, 4.8) |
| Found after this revision: invalid UTF-8 passes through the new typed-client fields | Raw bytes are rejected before dispatch (7.5) and tested (`TEST-535` 8) |

## 14. Accepted minimum fixes (version 3.1)

### 14.1 Scope and evidence

Implement the accepted choices `1A`, `2A`, `3B` as local changes in the existing
functions. The [production-impact research](evidence/harden-llm/web-search-parity/2026-11-09-production-impact-research.md)
reproduced the failures through the provider router, runtime recovery and cache.
It did not establish production incident frequency. The local scratch patch is
an experiment, not a patch to apply blindly or a new test framework.

These corrections add no public types, configuration, dependencies, persistence
fields or telemetry. Reuse the provenance fields, test IDs and fixtures already
specified by this plan. Do not introduce a repair framework or custom URL
scanner. The exploratory patch was small; its line count is evidence of scope,
not a target or limit for implementation.

### 14.2 Phase 1: preserve evidence and correct Jina extraction

1. Extend `TestSearchProvenance*` (`TEST-534`) before editing application code.
   Use the real router with a local `httptest` upstream, the existing runtime
   repair policy and a process-local cache. Reproduce searched malformed JSON
   followed by successful repair. Keep valid JSON that fails schema validation
   as a separate control. Cover ordinary JSON and completed SSE responses.
2. In `normalizeDecodedResponse` (`internal/providers/normalize.go`), invoke
   `normalizeSearch` once after accounting, completion, envelope, refusal and
   empty-output checks, before structured JSON parsing. Attach it to the
   partial result and reuse it on success. Preserve the original parse error.
3. At the router's three normalization-error exits (JSON, streamed SSE and
   buffered SSE), preserve normalized search only for `STRUCTURED_PARSE` from
   a completed, admitted envelope. Keep output absent. Use one small private
   helper/condition shared by these exits. Leave transport/read failures and
   interrupted-stream clearing on the existing `clearProviderOutput` path.
4. For Jina in `normalizeSearch`, use `FindAllString(context, -1)`. Let the
   existing admission helper perform validation, deduplication and the source
   cap. Test repeated URLs followed by a distinct URL, duplicate-only input,
   and 51 distinct URLs. With the parent phase's `Incomplete` field, only
   actual omissions/invalid evidence set the flag; duplicates alone do not.
5. Require repaired results to retain observed search and sources. A repeated
   request must replay that evidence from cache without another provider or
   Jina dispatch. Keep HTTP errors, incomplete responses, refusals and
   unterminated streams rejected with neither output nor search evidence.

The Jina change is a small adjacent correction. If it requires a broader
redesign, defer that redesign rather than expanding native-search parity.
Do not skip or weaken a failing assertion to complete the phase.

### 14.3 Phase 2: keep required search out of repair

1. Extend `TestWebSearchRequestParity*` (`TEST-535`) with required search followed
   by malformed-JSON repair and schema-invalid-JSON repair. The generation must
   send the required search tool. First confirm that admitting canonical
   `required` without the transition fix rejects repair before its dispatch.
2. In `prepareNextPlannedWork` (`internal/runtime/recovery_execute.go`), after
   selecting the repair call and before `Executor.Prepare`, disable search and
   clear its `WebSearchTool` and search-specific required choice together.
   Mutate the repair copy only. Keep saved generation settings for reruns;
   do not alter function-tool choice semantics or relax provider validation.
3. Assert that generation plus successful repair makes exactly two provider
   dispatches with one search, and that repair has no search tool or search
   choice. Cover a generation-sourced repair and an explicit model target
   through the existing recovery test fixtures. Check that genuine rerun
   generation still receives the original search settings.

Do not add canonical required-search support as an independent prerequisite
patch solely to fix its repair path. Ship the guard and transition together
with the parent phase's request-parity changes.

### 14.4 Phase 3: public projection and completion

Extend the existing `TEST-536` repair case to exercise both malformed JSON and
schema-invalid JSON through the public handler. Request source inclusion and
require retained calls and sources on the repaired result, valid final JSON,
and empty annotations when
the returned text differs from the citation hash. Check the same behavior on
cache replay. Reuse section 8's JSON/SSE parity coverage rather than adding a
second projection implementation or test harness.

Run the focused `TEST-534` to `TEST-536` commands from section 11 during their
phases, then the changed provider/runtime packages under the race detector:

```sh
go test -race ./internal/providers ./internal/runtime -count=1
```

Complete the parent plan's normal gates before its PR; these corrections add
no extra Docker, release-certification, browser or paid-provider gate. Record
test commands/results and remaining release gates. Planning and local tests
are not deployment evidence. This amendment authorizes no production promotion.
