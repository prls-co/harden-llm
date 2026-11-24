# Issue 186 — gateway contract failures and remediation plan

## 1. Decision and production exposure

The [protocol-aware option forwarding implementation plan](protocol-aware-option-forwarding-plan.md) supersedes the earlier two-check proposal. It covers native option delivery, protocol translation, recovery, and both tool-related [issue #186](https://github.com/prls-co/harden-llm/issues/186) acceptance items. This document retains the production assessment, observations, and causes at the recorded revisions.

`store: false` asks a Responses provider to disable storage of the generated response for later API retrieval. Inference still works. Harden's ordinary API has no stored-response retrieval workflow and rejects `store: true`. Provider abuse-monitoring logs, Harden's own cache and job artifacts have separate controls. Existing callers can continue sending false. Dropping the field from Model Recall would break the opt-out for destinations where omission enables storage. [Ordinary API contract](../docs/adr/ADR-HLLM-032-openai-inference-contract.md) · [OpenAI data controls](https://developers.openai.com/api/docs/guides/your-data#v1responses)

On 2026-11-21, remote `main` and `dev` both pointed to `9554f5f`. The running production gateway used the certified `c86895c` release, image `sha256:b8aef0321b99cc5ef1a61d57ce7d89bd1d73151490284332f9b3f76c4150ff0a`. Only documentation and deployment evidence changed between those commits. The deployed Model Recall product container reported revision `0f0c25a`, which includes the preparation request below. This establishes deployed code exposure. The number of affected calls, their final provider routes, and historical retention are unknown. [Production receipt](evidence/harden-llm/issue-184/2026-11-20-production/deployment.json) · [Model Recall preparation](https://github.com/prls-co/masked-recall/blob/0f0c25a6798fea392f63e0ae0d40292309f381c5/product/src/model-recall-preparation.ts#L374-L388) · [Model Recall Go client](https://github.com/prls-co/masked-recall/blob/0f0c25a6798fea392f63e0ae0d40292309f381c5/internal/model/client.go#L89-L105)

The production gateway's mounted connection metadata selected CPA Responses by default. Its four other entries were Google Gemini, Novita Chat, OpenRouter Chat and Perplexity Agent. There was no direct OpenAI connection in that configuration snapshot. The running CPA image identified itself as `v8.0.69-prls.2`. Its pinned Responses-to-Codex translator sets `store` to boolean `false` even when its input omits the field. The pinned PRLS patch does not alter that translator. This mitigates one known route. Direct OpenAI Responses can retain application state for at least 30 days by default, subject to account controls. Neither a local capture nor a translator source inspection establishes what an individual provider retained. I checked deployment identities, connection metadata and pinned source. I did not inspect customer request logs or retrieve any stored provider response. For incident triage, correlate already-retained route/deployment metadata and the applicable account controls for callers that set the flag. Escalate if that review identifies affected traffic under a storage-enabled policy or evidence of retention. [Pinned CPA translator](https://github.com/router-for-me/CLIProxyAPI/blob/f7ae8feeb3710319f524730849fbe32fde1751e4/internal/translator/codex/openai/responses/codex_openai-responses_request.go#L14-L34) · [CPA build inputs](https://github.com/prls-co/CLIProxyAPI-setup/blob/a83a0af9cf3f4c8cccddb053f4bbe15ac2f471ee/images/cpa/source.json) · [OpenAI data controls](https://developers.openai.com/api/docs/guides/your-data#v1responses)

## 2. Reproduction and controls

Two local Go test overlays ran at `9554f5f` on Go 1.26.11. Both used the authenticated gateway, its concrete provider router, and a loopback TLS Responses server. The authenticated handler used an injected fixture client. Cache observations exercised Harden's real JSON codec with an in-memory backing store, so they establish the representation change independently of PostgreSQL or key isolation. The second fixture counted store reads, writes and outbound HTTP requests. All inputs were synthetic. The original issue records the minimal requests. [Issue and fixture inputs](https://github.com/prls-co/harden-llm/issues/186)

- **Plain Responses request.** Omission and explicit `store: false` each made one model request. The captured JSON had no `store` member in either case. Explicit `true` returned HTTP 400 before dispatch.
- **Wrong selector.** With only the function `lookup` declared, the explicit search selector reached the upstream as the string `required`. The fixture returned a function call and the gateway returned HTTP 200. A mixed search/function declaration already returned 400 before dispatch. For the malformed selector, the observed effect was a function call handed back to the client. The fixture never executed a client function.
- **Cache round trip.** The first overlay observed a fresh Responses `function_call` followed by a cache hit containing a text message. The second exercised both public routes independently, first writing with `refresh` and then reading with `cache`. Each route kept the same model, inputs and tools across its four requests.

| Cache setting with a declared function | Reads / writes / model calls per request | Responses result | Chat Completions result |
| --- | --- | --- | --- |
| Omitted | 0 / 0 / 1 | `function_call` | `tool_calls`, function present |
| `off` | 0 / 0 / 1 | `function_call` | `tool_calls`, function present |
| `refresh` | 0 / 1 / 1 | `function_call` | `tool_calls`, function present |
| `cache` after refresh | 1 / 0 / 0 | `message`, function absent | `stop`, function absent |

The retained baseline fixture reproduces all three failures from the repository root. Its assertions describe the historical behavior and it runs only by explicit overlay. It also decodes the text on each hit and checks that the original `lookup` call is inside it. [Baseline fixture](evidence/harden-llm/issue-186/testdata/baseline_test.go)

```sh
go test -overlay=plans/evidence/harden-llm/issue-186/overlay.json ./internal/gateway/httpapi -run '^TestIssue186Baseline$' -count=1 -v
```

Six existing controls also passed at this revision. They cover SDK function calls and streams, Model Recall structured output, search selection, job admission, search cache persistence, and ordinary cache replay. The following commands repeat those controls. The matrix above records separate baseline observations.

```sh
go test ./internal/gateway/httpapi -count=1 -run '^(TestOpenAIContractFunctionToolsAndFinalOnlyStreams|TestModelRecallStructuredResponsesPreserveOriginalOutput|TestWebSearchRequestParityGatewayToProvider|TestJobAPIValidatesBeforeAdmissionAndUsesBearerScope)$'
go test . -count=1 -run '^(TestSearchCachePersistenceProjection|TestCacheReplay)$'
```

## 3. Where each contract breaks

### 3.1 Storage opt-out disappears during request preparation

Both wire structs use `*bool`, so the decoder knows the difference between omission and false. The statelessness check rejects true. Each public handler then constructs a fresh provider-options map and leaves out `Store`. On the native Responses route the payload builder emits what it was given. The value was already lost in the gateway. The October proxy change introduced the request path. The Model Recall regression sends false but asserts the result text, schema error, and dispatch count. It never inspects this upstream field. [Wire types and storage validation](../internal/gateway/httpapi/requests.go) · [Request preparation](../internal/gateway/httpapi/handlers.go) · [Model Recall regression](../internal/gateway/httpapi/structured_output_test.go) · [October change](https://github.com/prls-co/harden-llm/commit/009051c)

Adding a map entry in the handler would fix the simplest reproduction. Two code paths make that insufficient. Model-target repair or rerun intentionally replaces the generation's provider options. Also, the Anthropic payload builder copies generic options, and some connections classified as `responses` actually send to Perplexity's `/agent`. Any future claim about opt-out forwarding must be checked at the actual upstream endpoint, including when recovery selects a different model. The router hashes the prepared operation payload, so emitting false there also keeps an explicit native opt-out from hitting an older entry produced with the field absent. [Recovery target selection](../internal/runtime/recovery_execute.go) · [Protocol and endpoint selection](../internal/providers/payload.go) · [Prepared cache identity](../internal/providers/router.go)

### 3.2 Search selection loses its tool type before validation

`decodeTools` already separates the search declaration from the function list. Since `d67c026`, `decodeToolChoice` has converted a recognized search object directly to the generic mode `required`. It never receives the decoded declaration. The core correctly accepts `required` with a declared function. By then it cannot tell which tool the caller selected. The existing parity test covers matching search declarations and intentionally accepts a preview selector with a standard search declaration. That alias behavior is part of the regression contract. [Decoder](../internal/gateway/httpapi/requests.go) · [Core validator](../tools.go) · [Existing alias and choice tests](../internal/gateway/httpapi/web_search_request_test.go) · [November change](https://github.com/prls-co/harden-llm/commit/d67c026)

### 3.3 Cached output loses its Go type

The provider normalizer returns `AssistantOutput` with typed tool calls. On a miss or refresh, the renderer can extract those calls. The cache serializes `Output any` into JSON and decodes it back into an untyped map. The integrity checks succeed because the record has valid JSON, version and identity. The shared renderer treats ordinary maps as structured output and serializes them as text. Responses consequently emits a message, and Chat emits no tool calls and chooses `finish_reason="stop"`. The hit reached neither search nor the model. SDK and cache tests previously exercised these paths independently. [Provider normalization](../internal/providers/normalize.go) · [Cache codec](../client_cache.go) · [Both renderers](../internal/gateway/httpapi/responses.go) · [SDK contract](../internal/gateway/httpapi/openai_contract_test.go)

The codec also serves direct Go clients. A public HTTP admission rule resolves the issue's requested contract at both routes. Supporting typed function replay for Go consumers would require a separately specified cache format and handling for existing records. Inferring a function from a map with a `tool_calls` key would misclassify legitimate structured JSON. [Cache contract and replay tests](../client_cache_test.go)

## 4. Implementation handoff

Use the [ordered implementation phases and acceptance gates](protocol-aware-option-forwarding-plan.md) for new work. The [earlier scope decision](https://github.com/prls-co/harden-llm/blob/3ad257b535f7f6651b4df1e9291fd2d00885501b/plans/issue-186-gateway-contract-rca-and-plan.md#4-implementation-order-and-acceptance) remains available at its original revision.
